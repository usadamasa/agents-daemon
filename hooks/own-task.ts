import type { EngineInterface } from 'claude-code'

import type { GraphNode } from '../types'
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

// HAND_OFF は渡す文面の後半。description と進み具合は mod が知らないので、タスクを持つ自分に書かせる。
const HAND_OFF = 'SendMessage の本文にタスクの description と今の進み具合を入れて送り、届いたら TaskUpdate で deleted にしてください。'

// handText は生きているほかのセッションへ渡すよう頼む文面。同名のセッションを見分けられるよう session ID を添える。
export function handText(node: GraphNode, to: Extract<Destination, { route: 'peer' }>): string {
  return `${ref(node)}を ${to.session} (session ID ${to.session_id}) へ渡してください。${HAND_OFF}`
}

export function spawnText(node: GraphNode): string {
  return `${ref(node)}を新しいセッションへ渡してください。agents-daemon:peer-sessions skill の手順で peer を立て、${HAND_OFF}`
}

// toParking はタスクをパーキングロットへ戻す。メモから作ったタスク (handed のメモがある) は新しいメモを作らず、
// 元のメモを open に戻してからタスクを消すよう頼む。入力へ渡せなければメモを handed に取り直す (宛先は戻らない)。
// 扱うメモが無ければ、park tool でメモに置いてから消すよう頼む。
export async function toParking($: EngineInterface, node: GraphNode): Promise<string> {
  const handed = node.memos.filter(m => m.status === 'handed')
  if (handed.length === 0) {
    return ask($, `${ref(node)}をパーキングロットへ戻してください。park tool でタスクの title と description をメモ (kind は scope) に置き、置けたら TaskUpdate で deleted にしてください。`)
  }
  const reopened: string[] = []
  for (const memo of handed) {
    const failure = await park($, ['set', memo.id, '--status', 'open', '--from', 'handed'])
    if (failure !== '') return `メモ ${memo.id} を open に戻せませんでした: ${failure}${await reclaim($, reopened)}`
    reopened.push(memo.id)
  }
  const ids = reopened.join(', ')
  const failure = await tellSelf($, `${ref(node)}の元のメモ (${ids}) をパーキングロットの open に戻しました。TaskUpdate で deleted にしてください。`)
  if (failure === '') return `メモ ${ids} を open に戻し、${SAID}`
  return `このセッションの入力へ渡せませんでした: ${failure}${await reclaim($, reopened)}`
}

// reclaim は open に戻したメモを handed に取り直し、結果の文面 (前の文に続ける) を返す。
async function reclaim($: EngineInterface, ids: string[]): Promise<string> {
  if (ids.length === 0) return ''
  const failed: string[] = []
  for (const id of ids) {
    const failure = await park($, ['set', id, '--status', 'handed', '--from', 'open'])
    if (failure !== '') failed.push(`${id}: ${failure}`)
  }
  return failed.length === 0 ? '。メモは handed に戻しました' : `。メモを handed に戻せませんでした (${failed.join('; ')})`
}
