import type { EngineInterface } from 'claude-code'

import type { DepsData, GraphNode } from '../types'
import { runDaemon } from './daemon-bin'
import { reason } from './log'

// 終了したセッションのタスクを、依存図の一覧から記録ごと外す (#337)。外すのはバイナリの `task dismiss` で、
// セッションの終了を status.json で確かめ直してから記録を消す。

// canDismiss はノードを一覧から外せるか。終了したセッションのタスク (完了しているかを問わない) だけ。TodoWrite の
// 項目はセッションの 1 リストなので 1 項目では消せず、置き場のノードには消す記録が無い。daemon が status.json を
// 書いていない依存図は生きているセッションも live でないと描くので出さない。
export function canDismiss(node: GraphNode, self: string, data: DepsData): boolean {
  return data.updated_at !== undefined && node.kind === 'task' && !node.live && node.session_id !== undefined && node.session_id !== self
}

// dismissOne はノード 1 つを外し、結果の文面を返す。
export async function dismissOne($: EngineInterface, node: GraphNode): Promise<string> {
  const failure = await dismiss($, node.key)
  return failure === '' ? `タスク「${node.title}」を一覧から外しました` : `一覧から外せませんでした: ${failure}`
}

// dismissAll は key を 1 つずつ外し、外した数と、外せなかった数と最初の理由を返す。1 つが外せなくても残りは続ける。
export async function dismissAll($: EngineInterface, refs: string[]): Promise<string> {
  const failures: string[] = []
  for (const ref of refs) {
    const failure = await dismiss($, ref)
    if (failure !== '') failures.push(failure)
  }
  const failed = failures[0] === undefined ? '' : ` (${failures.length} つは外せませんでした: ${failures[0]})`
  return `終了したセッションのタスク ${refs.length - failures.length} つを一覧から外しました${failed}`
}

// dismiss は ref を外し、失敗の理由を返す (外せたら空)。
async function dismiss($: EngineInterface, ref: string): Promise<string> {
  try {
    const { exitCode, stderr } = await runDaemon($, 'task dismiss', { ref, session_id: await $.session.id() })
    return exitCode === 0 ? '' : stderr.trim() || `exit ${exitCode}`
  } catch (err) {
    return reason(err, 'task dismiss')
  }
}
