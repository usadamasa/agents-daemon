import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

import type { EventLogRequest } from '../types'

// mod の出来事を `agents-daemon event-log` で出来事のログに書く (hooks/log.ts、eventlog)。

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// world は engine の代わりを立てる。event-log の呼び出しは logged に [event, 'key=value'...] で残し、ほかは run が答える。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult, register?: () => never) {
  const logged: string[][] = []
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    if (e.argv[1] === 'event-log') {
      expect([...e.argv]).toEqual([BIN, 'event-log'])
      const req = JSON.parse(e.init?.stdin ?? '') as EventLogRequest
      expect(req.session_id).toBe('SELF')
      logged.push([req.event, ...(req.fields ?? []).map(f => `${f.key}=${f.value}`)])
      return { value: output('') }
    }
    return { value: run(e.argv) }
  })
  on('tool.register', async (_$, e) => {
    if (register) register()
    return { value: { tool: `mcp__agents-daemon__${e.name}` } }
  })
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  return { logged }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

// engine は tool.register の失敗を session.start の呼び出し元へ返さず、plugin の session.start の hook を飛ばして
// 画面に一度出すだけにする。理由をログに残し、hook を飛ばす扱いは変えない。
// cc-premise: 2.1.302 mods tool.register が失敗すると engine が session.start の hook を skipped にする (claude plugin test で確認)
test('session.start で park の tool を登録できなかったら、理由をログに残す', async ($, on) => {
  const { logged } = world(
    on,
    () => output('[]'),
    () => {
      throw new Error('Failed to start server. Is port 0 in use?')
    },
  )

  await $.session.start(START)
  expect(logged).toHaveLength(1)
  expect(logged[0]?.[0]).toBe('mod.tool-register.failed')
  expect(logged[0]?.[1]).toMatch(/^reason=.*tool\.register/)
})
