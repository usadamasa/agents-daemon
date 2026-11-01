import type { EngineInterface } from 'claude-code'

import type { DepsData, GraphNode, TaskDismissed } from '../types'
import { runDaemon } from './daemon-bin'
import { reason } from './log'

// 終了したセッションのタスクを、依存図の一覧から記録ごと外す (#337)。外すのはバイナリの `task dismiss` で、
// セッションの終了を status.json で確かめ直してから記録を消す。

// canDismiss はノードを一覧から外せるか。終了したセッションのタスク (完了しているかを問わない) だけ。TodoWrite の
// 項目はセッションの 1 リストなので 1 項目では消せず、置き場のノードには消す記録が無い。daemon が status.json を
// 書いていない依存図は生きているセッションも live でないと描くので出さない (canAdopt と同じ)。
export function canDismiss(node: GraphNode, self: string, data: DepsData): boolean {
  return data.updated_at !== undefined && node.kind === 'task' && !node.live && node.session_id !== undefined && node.session_id !== self
}

// dismissOne はノード 1 つを外し、結果の文面を返す。
export async function dismissOne($: EngineInterface, node: GraphNode): Promise<string> {
  const ran = await dismiss($, [node.key])
  if ('failure' in ran) return `一覧から外せませんでした: ${ran.failure}`
  const failure = ran.result.failures[0]
  return failure === undefined ? `タスク「${node.title}」を一覧から外しました` : `一覧から外せませんでした: ${failure.reason}`
}

// dismissAll はノードの key をまとめて外し、外した数と、外せなかった数と最初の理由を返す。
export async function dismissAll($: EngineInterface, refs: string[]): Promise<string> {
  const ran = await dismiss($, refs)
  if ('failure' in ran) return `一覧から外せませんでした: ${ran.failure}`
  const { dismissed, failures } = ran.result
  const failed = failures[0] === undefined ? '' : ` (${failures.length} つは外せませんでした: ${failures[0].reason})`
  return `終了したセッションのタスク ${dismissed.length} つを一覧から外しました${failed}`
}

async function dismiss($: EngineInterface, refs: string[]): Promise<{ result: TaskDismissed } | { failure: string }> {
  try {
    const { exitCode, stdout, stderr } = await runDaemon($, 'task dismiss', { refs, session_id: await $.session.id() })
    if (exitCode !== 0) return { failure: stderr.trim() || `exit ${exitCode}` }
    return { result: JSON.parse(stdout) as TaskDismissed }
  } catch (err) {
    return { failure: reason(err, 'task dismiss') }
  }
}
