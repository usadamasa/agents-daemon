import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

// model が SendMessage で渡した宛先に依存の辺が無ければ、その tool の結果の context で辺の書き方を伝える (#172)。

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const PEER_CWD = '/src/home/agents-daemon/.claude/worktrees/edge-cli'
const DEPS = [BIN, 'deps', '--cwd', CWD]

// node はタスクのノード。sid と tid から key (task:<sid>/<tid>) を組む。
function node(sid: string, tid: string, sessionName: string, extra: Record<string, unknown> = {}) {
  return {
    key: `task:${sid}/${tid}`,
    kind: 'task',
    state: 'working',
    title: 't',
    endpoint: true,
    cwd: PEER_CWD,
    task_id: tid,
    session_id: sid,
    session: sessionName,
    session_name: sessionName,
    live: true,
    depth: 0,
    downstream: 0,
    parked: 0,
    memos: [],
    blocked_by: [],
    deadlock: false,
    halted: false,
    stuck: false,
    ...extra,
  }
}

function deps(nodes: unknown[], links: unknown[] = []) {
  return {
    daemon_running: true,
    here: CWD,
    stale_memo_days: 30,
    repos: [],
    graph: { nodes, links, longest_chain: [], rows: [], rows_with_completed: [] },
  }
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下の engine の代わり。deps は state.deps (Error なら失敗) を返し、呼ばれた argv を残す。
// SendMessage は state.failing が立っていればエラーにし、そうでなければ届いたことにする。
function world(on: On, initial: unknown) {
  const state = { deps: initial, failing: false, ran: [] as string[][] }
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    state.ran.push([...e.argv])
    if (e.argv[1] !== 'deps') return { value: output('[]') }
    if (state.deps instanceof Error) return { value: output('', 1, state.deps.message) }
    return { value: output(JSON.stringify(state.deps)) }
  })
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  on('tool.call', { tool: 'SendMessage' }, async () =>
    (state.failing ? { isError: true, result: 'No agent named edge-cli', text: 'No agent named edge-cli' } : { result: { success: true } }) as never,
  )
  return state
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

const send = ($: Engine, to: string, extra: Record<string, unknown> = {}) => $.tool.call({ tool: 'SendMessage', to, message: '引き継ぎ', ...extra } as never)

const context = (res: { context?: readonly string[] }) => (res.context ?? []).join('\n')

const depsCalls = (state: { ran: string[][] }) => state.ran.filter(argv => argv[1] === 'deps')

test('辺の無い生きているセッションへ渡したら、辺の書き方を結果の context で model に伝える', async ($, on) => {
  const w = world(on, deps([node('PEER', '1','edge-cli')]))
  await $.session.start(START)
  w.ran.length = 0

  const res = await send($, 'edge-cli')
  const text = context(res)
  expect(depsCalls(w)).toEqual([DEPS])
  expect(text).toMatch(/edge-cli/)
  expect(text).toMatch(/edge add --before .* --before-cwd .* --after session:edge-cli --after-cwd/)
  // 宛先の cwd は依存図のノードから埋める。
  expect(text).toContain(`--after-cwd ${PEER_CWD}`)
  expect(text).toMatch(/agents-daemon:peer-sessions/)
  expect(text).toMatch(/返事や報告/)
  expect(res.context).toHaveLength(1)
  // tool の結果そのものは変えない。
  expect(res.result).toEqual({ success: true })
})

test('同名のセッションに付く末尾の [ref] を落として宛先を当てる', async ($, on) => {
  world(on, deps([node('PEER', '1','edge-cli')]))
  await $.session.start(START)

  expect(context(await send($, 'edge-cli [ab12]'))).toMatch(/session:edge-cli /)
})

test('宛先が辺の端点にあれば伝えない (ノードへ解決された端点でも、元の参照でも)', async ($, on) => {
  const resolved = { before: 'task:OTHER/1', after: 'task:PEER/1', kind: 'edge', edge: { id: 'e1', before: { ref: 'task:OTHER/1', cwd: CWD, repo: CWD }, after: { ref: 'session:edge-cli', cwd: PEER_CWD, repo: CWD } } }
  const w = world(on, deps([node('PEER', '1','edge-cli'), node('OTHER', '1', 'other')], [resolved]))
  await $.session.start(START)
  expect(context(await send($, 'edge-cli'))).toBe('')

  const byRef = { before: 'pr:o/r#1', after: 'x', kind: 'edge', edge: { id: 'e2', before: { ref: 'pr:o/r#1', cwd: CWD, repo: CWD }, after: { ref: 'session:edge-cli', cwd: PEER_CWD, repo: CWD } } }
  w.deps = deps([node('PEER', '1','edge-cli')], [byRef])
  expect(context(await send($, 'edge-cli'))).toBe('')
})

test('依存図に生きていない宛先 (subagent、teammate、済んだセッション) には伝えない', async ($, on) => {
  world(on, deps([node('PEER', '1','edge-cli', { state: 'completed' }), node('GONE', '1', 'gone', { live: false })]))
  await $.session.start(START)

  expect(context(await send($, 'main'))).toBe('')
  expect(context(await send($, 'edge-cli'))).toBe('')
  expect(context(await send($, 'gone'))).toBe('')
})

test('送信がエラーになったら、依存図を読まずに結果をそのまま返す', async ($, on) => {
  const w = world(on, deps([node('PEER', '1','edge-cli')]))
  await $.session.start(START)
  w.ran.length = 0
  w.failing = true

  const res = await send($, 'edge-cli')
  expect(context(res)).toBe('')
  expect(res.isError).toBe(true)
  expect(depsCalls(w)).toEqual([])
})

test('subagent の送信と、文面の無い送信 (notify_when_idle だけ、shutdown の要求) では伝えない', async ($, on) => {
  const w = world(on, deps([node('PEER', '1','edge-cli')]))
  await $.session.start(START)
  w.ran.length = 0

  expect(context(await send($, 'edge-cli', { agentId: 'agent-1' }))).toBe('')
  expect(context(await send($, 'edge-cli', { message: undefined, notify_when_idle: true }))).toBe('')
  expect(context(await send($, 'edge-cli', { message: { type: 'shutdown_request' } }))).toBe('')
  expect(depsCalls(w)).toEqual([])
})

test('依存図を読めなかったら送信の結果をそのまま返す', async ($, on) => {
  world(on, new Error('status snapshot のパースに失敗'))
  await $.session.start(START)

  const res = await send($, 'edge-cli')
  expect(context(res)).toBe('')
  expect(res.result).toEqual({ success: true })
})
