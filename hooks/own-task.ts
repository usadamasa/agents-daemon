import type { EngineInterface } from 'claude-code'

import type { GraphNode } from '../types'
import { logEvent } from './log'
import { park, tellSelf } from './parking'
import type { Destination } from './parking'

// 依存図の pane から、開いたセッション自身のタスクを動かす操作 (#296)。mod は TaskUpdate を呼べないので、どれも
// 自分の入力へ頼む文を渡し、タスクを動かすのは自分の model に任せる。描くのは hooks/deps.tsx。

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

export function startText(node: GraphNode): string {
  return `${ref(node)}に着手してください。TaskUpdate で in_progress にしてから進めてください。`
}

export function statusText(node: GraphNode, status: 'completed' | 'deleted'): string {
  return `${ref(node)}を TaskUpdate で ${status} にしてください。`
}

// handOff は生きているほかのセッションへタスクを渡す。model に SendMessage で渡させると、ListAgents の名前が依存図の
// どの欄 (表示名、session_name、session ID) とも一致しないセッションへ届かないので、mod が session ID で title と
// description を送り、届いたら自分にタスクを消すよう頼む。進み具合は mod が知らないので送らない。
export async function handOff($: EngineInterface, node: GraphNode, to: Extract<Destination, { route: 'peer' }>): Promise<string> {
  const lines = [`${node.session ?? ''} (session ID ${node.session_id ?? ''}) からタスク「${node.title}」を渡します。TaskCreate で作って進めてください。`]
  if (node.description) lines.push(`詳細: ${node.description}`)
  const event = { via: 'hand-off', to: to.session_id }
  let reason: string
  try {
    const result = await $.session.send({ to: { sessionId: to.session_id }, text: lines.join('\n') })
    reason = result.isDelivered ? '' : result.reason
  } catch (err) {
    reason = String(err)
  }
  await logEvent($, 'mod.send', reason === '' ? { ...event, delivered: true } : { ...event, delivered: false, reason })
  if (reason !== '') return `${to.session} へ送れませんでした: ${reason}`
  const failure = await tellSelf($, `${ref(node)}を ${to.session} へ渡しました (title と description を送った)。TaskUpdate で deleted にしてください。`)
  return failure === '' ? `${to.session} へ渡し、${SAID}` : `${to.session} へ渡しましたが、このセッションの入力へ渡せませんでした (タスクは残っています): ${failure}`
}

// spawnText は新しいセッションを起こして渡すよう頼む文面。起こしたセッションの名前は自分が付けるので、SendMessage で
// 渡すのも自分に任せる。
export function spawnText(node: GraphNode): string {
  return `${ref(node)}を新しいセッションへ渡してください。agents-daemon:peer-sessions skill の手順で peer を立て、SendMessage の本文にタスクの description と今の進み具合を入れて送り、届いたら TaskUpdate で deleted にしてください。`
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
    const failure = await park($, ['set', memo.id, '--status', 'open', '--from', 'handed'])
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
    const failure = await park($, ['set', id, '--status', 'handed', '--from', 'open', '--to-session', self])
    if (failure !== '') failed.push(`${id}: ${failure}`)
  }
  return failed.length === 0 ? '。メモは handed に戻しました' : `。メモを handed に戻せませんでした (${failed.join('; ')})`
}
