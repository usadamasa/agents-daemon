import type { EngineInterface } from 'claude-code'

import type { GraphNode } from '../types'
import { logEvent } from './log'
import { park, spawnSession, tellSelf, withNote } from './parking'
import type { Destination, SpawnDestination } from './parking'

// 依存図の pane から、開いたセッション自身のタスクを動かす操作 (#296)。mod は TaskUpdate を呼べないので、どれも
// 自分の入力へ頼む文を渡し、タスクを動かすのは自分の model に任せる。渡す相手へ送ることと新しいセッションを起こす
// ことだけは mod が行う。描くのは hooks/deps.tsx。

// isOwn は開いたセッションの完了していないタスクか。
export function isOwn(node: GraphNode, self: string): boolean {
  return node.kind === 'task' && node.session_id === self && node.state !== 'completed'
}

// SAID は入力へ渡せたときの文面。入力はセッションが idle になってから入るので、済んだとは言わない。
const SAID = 'このセッションの入力へ渡しました'

// ask は text を自分の入力へ渡し、結果の文面を返す。
export async function ask($: EngineInterface, text: string): Promise<string> {
  const failure = await tellSelf($, text)
  return failure === '' ? SAID : `このセッションの入力へ渡せませんでした: ${failure}`
}

function ref(node: GraphNode): string {
  return `タスク #${node.task_id ?? ''}「${node.title}」`
}

export function startText(node: GraphNode, note = ''): string {
  return withNote(`${ref(node)}に着手してください。TaskUpdate で in_progress にしてから進めてください。`, note)
}

export function statusText(node: GraphNode, status: 'completed' | 'deleted'): string {
  return `${ref(node)}を TaskUpdate で ${status} にしてください。`
}

// handOff は生きているほかのセッションへタスクを渡す。model に SendMessage で渡させると、ListAgents の名前が依存図の
// どの欄 (表示名、session_name、session ID) とも一致しないセッションへ届かないので、mod が session ID で title と
// description を送り、届いたら自分にタスクを消すよう頼む。進み具合は mod が知らないので送らない。補足 (note) は
// 相手へ送る文面にだけ足す。
export async function handOff($: EngineInterface, node: GraphNode, to: Extract<Destination, { route: 'peer' }>, note = ''): Promise<string> {
  const event = { via: 'hand-off', to: to.session_id }
  let reason: string
  try {
    const result = await $.session.send({ to: { sessionId: to.session_id }, text: handText(node, note) })
    reason = result.isDelivered ? '' : result.reason
  } catch (err) {
    reason = String(err)
  }
  await logEvent($, 'mod.send', reason === '' ? { ...event, delivered: true } : { ...event, delivered: false, reason })
  if (reason !== '') return `${to.session} へ送れませんでした: ${reason}`
  const failure = await tellSelf($, `${ref(node)}を ${to.session} へ渡しました (title と description を送った)。TaskUpdate で deleted にしてください。`)
  return failure === '' ? `${to.session} へ渡し、${SAID}` : `${to.session} へ渡しましたが、このセッションの入力へ渡せませんでした (タスクは残っています): ${failure}`
}

// handText は渡す相手へ送る文面 (title と description)。補足 (note) は末尾に足す。
function handText(node: GraphNode, note: string): string {
  const lines = [`${node.session ?? ''} (session ID ${node.session_id ?? ''}) からタスク「${node.title}」を渡します。TaskCreate で作って進めてください。`]
  if (node.description) lines.push(`詳細: ${node.description}`)
  return withNote(lines.join('\n'), note)
}

// handOffToNew は repo で新しいセッションを起こしてタスクを渡す (#309)。peer へ渡すときと同じ文面を最初のターンに入れ、
// 起きたら自分にタスクを消すよう頼む。起きたか分からない (window はできたが claude が前景にならなかった) ときは、
// 消すと誰も持たないタスクになりうるので頼まずに残す。
export async function handOffToNew($: EngineInterface, node: GraphNode, to: SpawnDestination, note = ''): Promise<string> {
  const text = `このタスクを渡すため、${to.title} (${to.repo}) で新しいセッションとして起こしました。\n${handText(node, note)}`
  const delivered = await spawnSession($, to.repo, text)
  const event = { via: 'hand-off', repo: to.repo, to: delivered.to }
  if ('unknown' in delivered) {
    await logEvent($, 'mod.send', { ...event, delivered: false, reason: delivered.unknown })
    return `${to.title} で window を作りましたが、claude が前景になりませんでした: ${delivered.unknown}。タスクは残してあります (起きていれば TaskUpdate で deleted にする)`
  }
  if ('failure' in delivered) {
    await logEvent($, 'mod.send', { ...event, delivered: false, reason: delivered.failure })
    return `${to.title} で新しいセッションを起こせませんでした: ${delivered.failure}`
  }
  await logEvent($, 'mod.send', { ...event, delivered: true })
  const failure = await tellSelf($, `${ref(node)}を ${to.title} で起こした新しいセッションへ渡しました (title と description を送った)。TaskUpdate で deleted にしてください。`)
  return failure === ''
    ? `${to.title} で新しいセッションを起こして渡し、${SAID}`
    : `${to.title} で新しいセッションを起こして渡しましたが、このセッションの入力へ渡せませんでした (タスクは残っています): ${failure}`
}

// toParking はタスクをパーキングロットへ戻す。扱うメモ (捨てていないもの) があるタスクは新しいメモを作らず、
// handed のメモを open に戻してからタスクを消すよう頼む。入力へ渡せなければ戻したメモを自分への handed に取り直す。
// 扱うメモが無ければ、park tool でメモに置いてから消すよう頼む。
export async function toParking($: EngineInterface, node: GraphNode): Promise<string> {
  const kept = node.memos.filter(m => m.status !== 'discarded')
  if (kept.length === 0) {
    return ask($, `${ref(node)}をパーキングロットへ戻してください。park tool でタスクの title と description をメモ (kind は scope) に置き、置けたら TaskUpdate で deleted にしてください。`)
  }
  const reopened: string[] = []
  for (const memo of kept.filter(m => m.status === 'handed')) {
    const failure = await park($, 'park set', { id: memo.id, status: 'open', from: 'handed' })
    if (failure !== '') return `メモ ${memo.id} を open に戻せませんでした: ${failure}${await reclaim($, reopened)}`
    reopened.push(memo.id)
  }
  const ids = kept.map(m => m.id).join(', ')
  const failure = await tellSelf($, `${ref(node)}の元のメモ (${ids}) をパーキングロットの open に戻しました。TaskUpdate で deleted にしてください。`)
  if (failure === '') return `メモ ${ids} を open に戻し、${SAID}`
  return `このセッションの入力へ渡せませんでした: ${failure}${await reclaim($, reopened)}`
}

// reclaim は open に戻したメモを自分への handed に取り直し、結果の文面 (前の文に続ける) を返す。
async function reclaim($: EngineInterface, ids: string[]): Promise<string> {
  if (ids.length === 0) return ''
  const self = await $.session.id()
  const failed: string[] = []
  for (const id of ids) {
    const failure = await park($, 'park set', { id, status: 'handed', from: 'open', to_session: self })
    if (failure !== '') failed.push(`${id}: ${failure}`)
  }
  return failed.length === 0 ? '。メモは handed に戻しました' : `。メモを handed に戻せませんでした (${failed.join('; ')})`
}
