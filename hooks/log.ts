import { atom, read, update } from 'claude-code'
import type { EngineInterface } from 'claude-code'

import { daemonBin } from './daemon-bin'

// mod の出来事を出来事のログ (eventlog) に残す。mod はファイルを直接書けないので
// `agents-daemon event-log` に渡し、このセッションの ID を添える。
// 出来事の名前と欄の一覧は eventlog の package doc が持つ。subject、title、送った文面は渡さない。

// logged は slot ごとに最後に書いた中身。同じ失敗や同じ数が続く間は書き直さない (依存図は数秒ごとに読み直す)。
const logged = atom({ plugin: 'agents-daemon', key: 'logged' } as const, {})

export type Fields = Record<string, string | number | boolean>

// logEvent は出来事を 1 行書く。書けなくても (バイナリがまだ無いなど) 呼び出し元を止めない。
export async function logEvent($: EngineInterface, event: string, fields: Fields = {}) {
  try {
    const args = Object.entries(fields).map(([key, value]) => `${key}=${value}`)
    const argv = [await daemonBin($), 'event-log', '--session', await $.session.id(), '--', event, ...args]
    await $.process.run(argv, { timeoutMs: 10000 })
  } catch {
    // ログは補助なので、書けなかったことはどこにも出さない。
  }
}

// logChange は slot の中身 (fields) が前に書いたものと違うときだけ書く。event は見ないので、開いたときと同じ数の
// 読み直しは「変わった」として書かない。
export async function logChange($: EngineInterface, slot: string, event: string, fields: Fields = {}) {
  const key = JSON.stringify(fields)
  if ((await read($, logged))[slot] === key) return
  await update($, logged, prev => ({ ...prev, [slot]: key }))
  await logEvent($, event, fields)
}

// settle は slot を戻す。読めるようになった後に同じ失敗が起きたら、また書く。
export async function settle($: EngineInterface, slot: string) {
  if ((await read($, logged))[slot] === undefined) return
  await update($, logged, prev => {
    const { [slot]: _, ...rest } = prev
    return rest
  })
}

// reason は失敗をログに書く理由にする。JSON.parse の失敗の文面は読んだ中身 (メモの title など) を含みうるので、
// 固定の文面にする。
export function reason(err: unknown, what: string): string {
  if (err instanceof SyntaxError) return `${what} の JSON を読めない`
  return String(err)
}
