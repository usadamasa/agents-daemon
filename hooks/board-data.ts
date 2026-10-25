import { atom, update } from 'claude-code'
import type { EngineInterface } from 'claude-code'

import type { BoardData } from '../types'
import { daemonBin } from './daemon-bin'
import { reason } from './log'

// `agents-daemon status --board` を読んで board に置く。B の pane (hooks/board.tsx) と、渡した宛先の辺を確かめる
// hooks/handoff-notice.ts が同じボードを読むので、読み込みはここ 1 箇所にする。ログの書き方は読み手ごとに違うので
// 呼び出し側が持つ。

// atom は読み書きする module ごとに書く (state の scan が import を辿らない)。
const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')

// loadBoard はボードを読み直して board に置き、失敗の理由を返す (読めたら空)。失敗は error に置き、前のボードは残す。
// 開いたセッションの cwd を渡し、その repo の行を here にさせる。
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
    await update($, error, () => '')
    return { data }
  } catch (err) {
    // バイナリがまだ build されていない間もここに来る。
    return failed(reason(err, 'status --board'))
  }
}
