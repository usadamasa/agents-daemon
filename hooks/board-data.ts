import { atom, update } from 'claude-code'
import type { EngineInterface } from 'claude-code'

import type { BoardData, ParkedMemo } from '../types'
import { daemonBin } from './daemon-bin'
import { logChange, reason, settle } from './log'
import { lastPlacedAt } from './parking'

// `agents-daemon status --board` を読んで board に置く。B の pane (hooks/board.tsx) と A の band (hooks/band.tsx) が
// 同じボードを描くので、読み込みはここ 1 箇所にする。ログの書き方は読み手ごとに違うので呼び出し側が持つ。

// atom は読み書きする module ごとに書く (state の scan が import を辿らない)。
const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')
// parked はこのセッションの repo の open のメモ。hooks/band.tsx がメモの行に出す。
const parked = atom({ plugin: 'agents-daemon', key: 'parked' } as const, [])

// loadBoard はボードを読み直して board に置き、失敗の理由を返す (読めたら空)。失敗は error に置き、前のボードは残す。
// 開いたセッションの cwd を渡し、その repo の行を here にさせる。
// あわせて、here の行の parking (この repo の open のメモ) を parked に置く。open でなくなったメモはここで消える。
export async function loadBoard($: EngineInterface): Promise<{ data: BoardData } | { failure: string }> {
  const failed = async (failure: string) => {
    await update($, error, () => failure)
    return { failure }
  }
  try {
    const argv = [await daemonBin($), 'status', '--board', '--cwd', await $.session.cwd()]
    const { exitCode, stdout, stderr } = await $.process.run(argv, { timeoutMs: 10000 })
    if (exitCode !== 0) return failed(stderr.trim() || `exit ${exitCode}`)
    const data = JSON.parse(stdout) as BoardData
    if (!Array.isArray(data.graph?.nodes)) return failed('status --board の JSON に graph が無い')
    await update($, board, () => data)
    await update($, parked, () => hereMemos(data))
    await update($, error, () => '')
    return { data }
  } catch (err) {
    // バイナリがまだ build されていない間もここに来る。
    return failed(reason(err, 'status --board'))
  }
}

// reloadForBand はボードを読み直し、読めなかった理由を band の slot に残す (読めたら slot を戻す)。band の 10 秒ごとの
// 読み直しと、メモを変える park add の後が呼ぶ。band は補助なので、失敗は理由をログに残すだけにする。
export async function reloadForBand($: EngineInterface) {
  const loaded = await loadBoard($)
  if ('failure' in loaded) {
    await logChange($, 'band-board', 'mod.band.failed', { reason: loaded.failure })
    return
  }
  await settle($, 'band-board')
}

// hereMemos は here の行の parking を band に出す形にする。行が無ければ (この repo にメモが無い) 空。
function hereMemos(data: BoardData): ParkedMemo[] {
  const row = data.rows.find(r => r.repo === data.here)
  return (row?.parking ?? []).map(memo => ({ id: memo.id, kind: memo.kind, title: memo.title, at: lastPlacedAt(memo) }))
}
