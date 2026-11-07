import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const CWD = '/src/home/agents-daemon'
const APP = '/home/u/Applications/agent-tasks.app'
const OPEN = '/usr/bin/open'

function output(exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout: '', stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下の engine の代わり。open の呼び出しを記録し、exitCode で答える。
function world(on: On, opts: { exitCode?: number; env?: Record<string, string> } = {}) {
  const opened: string[][] = []
  const commands: Record<string, unknown>[] = []
  mock.env(on, { HOME: '/home/u', ...opts.env })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    if (e.argv[0] === OPEN) opened.push([...e.argv])
    return { value: e.argv[0] === OPEN ? output(opts.exitCode ?? 0, opts.exitCode ? 'not found' : '') : output() }
  })
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => {
    commands.push({ ...e })
    return { value: { command: e.name } }
  })
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  return { opened, commands }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

test('対話のセッションが始まると agent-tasks を前面を奪わずに開く (開いていれば open は何もしない)', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  expect(w.opened).toEqual([[OPEN, '-g', APP]])
})

test('-p や SDK のセッションでは開かない', async ($, on) => {
  const w = world(on)
  await $.session.start({ ...START, surface: null, isInteractive: false })
  expect(w.opened).toEqual([])
})

test('AGENTS_DAEMON_OPEN_APP=0 なら起動時には開かない', async ($, on) => {
  const w = world(on, { env: { AGENTS_DAEMON_OPEN_APP: '0' } })
  await $.session.start(START)
  expect(w.opened).toEqual([])
})

test('アプリがまだ無く open が失敗しても、セッションの開始は止めない', async ($, on) => {
  const w = world(on, { exitCode: 1 })
  const res = await $.session.start(START)
  expect(res.cwd).toBe(CWD)
  expect(w.opened).toHaveLength(1)
})

test('/agent-tasks を登録し、打つと前面に開く', async ($, on) => {
  const w = world(on, { env: { AGENTS_DAEMON_OPEN_APP: '0' } })
  await $.session.start(START)
  expect(w.commands.map(c => c.name)).toEqual(['agent-tasks'])

  const res = await $.command.run({ command: 'agent-tasks', args: '' } as never)
  expect(w.opened).toEqual([[OPEN, APP]])
  expect(res.text).toMatch(/agent-tasks/)
})

test('/agent-tasks で開けなければ理由と建て直しの案内を返す', async ($, on) => {
  world(on, { exitCode: 1, env: { AGENTS_DAEMON_OPEN_APP: '0' } })
  await $.session.start(START)

  const res = await $.command.run({ command: 'agent-tasks', args: '' } as never)
  expect(res.text).toMatch(/開けません/)
  expect(res.text).toMatch(/not found/)
  expect(res.text).toMatch(/build\.log/)
})
