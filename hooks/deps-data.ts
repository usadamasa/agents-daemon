import { atom, update } from 'claude-code'
import type { EngineInterface } from 'claude-code'

import type { DepsData } from '../types'
import { runDaemon } from './daemon-bin'
import { reason } from './log'

// `agents-daemon deps` を読んで deps に置く。渡した宛先の辺を確かめる hooks/handoff-notice.ts が読む。
// 依存図を描くのはアプリ (gui/) で、mod はここで読んだ依存図を描かない。

// atom は読み書きする module ごとに書く (state の scan が import を辿らない)。
const deps = atom({ plugin: 'agents-daemon', key: 'deps' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')

// loadDeps は依存図を読み直して deps に置き、失敗の理由を返す (読めたら空)。失敗は error に置き、前の依存図は残す。
export async function loadDeps($: EngineInterface): Promise<{ data: DepsData } | { failure: string }> {
  const failed = async (failure: string) => {
    await update($, error, () => failure)
    return { failure }
  }
  try {
    const { exitCode, stdout, stderr } = await runDaemon($, 'deps', {})
    if (exitCode !== 0) return failed(stderr.trim() || `exit ${exitCode}`)
    const data = JSON.parse(stdout) as DepsData
    if (!Array.isArray(data.graph?.nodes)) return failed('deps の JSON に graph が無い')
    await update($, deps, () => data)
    await update($, error, () => '')
    return { data }
  } catch (err) {
    // バイナリがまだ build されていない間もここに来る。
    return failed(reason(err, 'deps'))
  }
}
