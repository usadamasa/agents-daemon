import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

import type { EdgeAddRequest } from '../types'

// model が SendMessage で渡した宛先に依存の辺が無ければ、その tool の結果の context で辺の書き方を伝える (#172)。

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const PEER_CWD = '/src/home/agents-daemon/.claude/worktrees/edge-cli'
const DEPS = [BIN, 'deps', {}]

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
    stale_memo_days: 30,
    repos: [],
    recent_repos: [],
    graph: { nodes, links, longest_chain: [], folded: { keys: [], links: [] }, rows: [], rows_with_completed: [] },
  }
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下の engine の代わり。deps は state.deps (Error なら失敗) を返し、呼ばれた argv の後に stdin の
// JSON を読んだものを並べて残す。SendMessage は state.failing が立っていればエラーにし、そうでなければ届いたことにする。
function world(on: On, initial: unknown) {
  const state = { deps: initial, failing: false, ran: [] as unknown[][] }
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    state.ran.push([...e.argv, JSON.parse(e.init?.stdin ?? '{}')])
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

const depsCalls = (state: { ran: unknown[][] }) => state.ran.filter(argv => argv[1] === 'deps')

// shWord は sh の単語のうち、単引用符の区間と `\'` だけで組んだものを sh と同じ規則で読む。それ以外の形なら落とす。
function shWord(word: string): string {
  const parts = word.match(/'[^']*'|\\'/g) ?? []
  if (parts.join('') !== word) throw new Error(`sh の単語として読めない: ${word}`)
  return parts.map(p => (p === "\\'" ? "'" : p.slice(1, -1))).join('')
}

// edgeAdds は context の中の `printf '%s' <引用した JSON> | <bin> edge add` の行 (向きごとに 1 行) から、stdin に渡す
// JSON を sh が読むとおりに、並びの順 (peer が待つ向き、自分のタスクが待つ向き) に読む。2 行でなければ落とす。
function edgeAdds(text: string): [EdgeAddRequest, EdgeAddRequest] {
  const reqs = text
    .split('\n')
    .filter(l => l.endsWith(`| ${BIN} edge add`))
    .map(l => JSON.parse(shWord(l.replace(/^\s*printf '%s' /, '').replace(` | ${BIN} edge add`, ''))) as EdgeAddRequest)
  const [peerWaits, selfWaits] = reqs
  if (reqs.length !== 2 || peerWaits === undefined || selfWaits === undefined) throw new Error(`edge add の行が ${reqs.length} 行`)
  return [peerWaits, selfWaits]
}

test('辺の無い生きているセッションへ渡したら、辺の書き方を両方の向きで結果の context で model に伝える', async ($, on) => {
  const w = world(on, deps([node('PEER', '1','edge-cli')]))
  await $.session.start(START)
  w.ran.length = 0

  const res = await send($, 'edge-cli')
  const text = context(res)
  expect(depsCalls(w)).toEqual([DEPS])
  expect(text).toMatch(/edge-cli/)
  const [peerWaits, selfWaits] = edgeAdds(text)
  // 渡した peer がほかの作業を待つ向き。宛先は依存図のノードの session_id で sid:<id> と指し、cwd もノードから埋める。
  // 名前 (session:<名前>) は --name の無いセッションでは起こす側が決められず、同名の起こし直しで別のセッションへ結びつく。
  expect(peerWaits.after).toEqual({ ref: 'sid:PEER', cwd: PEER_CWD })
  expect(peerWaits.before.ref).not.toMatch(/^sid:PEER$/)
  // 自分のタスクが peer の完了を待つ向き。自分のタスクは task 欄の id だけで指し、session_id と cwd はバイナリが埋める。
  expect(selfWaits.before).toEqual({ ref: 'sid:PEER', cwd: PEER_CWD })
  expect(Object.keys(selfWaits.after)).toEqual(['task'])
  expect(text).toMatch(/agents-daemon:peer-sessions/)
  expect(text).toMatch(/返事や報告/)
  expect(res.context).toHaveLength(1)
  // tool の結果そのものは変えない。
  expect(res.result).toEqual({ success: true })
})

test("cwd に ' があっても、案内の行を sh が読むと cwd がそのまま渡る", async ($, on) => {
  const cwd = "/src/home/it's"
  world(on, deps([node('PEER', '1', 'edge-cli', { cwd })]))
  await $.session.start(START)

  const [peerWaits, selfWaits] = edgeAdds(context(await send($, 'edge-cli')))
  expect(peerWaits.after).toEqual({ ref: 'sid:PEER', cwd })
  expect(selfWaits.before).toEqual({ ref: 'sid:PEER', cwd })
})

test('同名のセッションに付く末尾の [ref] を落として宛先を当てる', async ($, on) => {
  world(on, deps([node('PEER', '1','edge-cli')]))
  await $.session.start(START)

  const [peerWaits, selfWaits] = edgeAdds(context(await send($, 'edge-cli [ab12]')))
  expect(peerWaits.after.ref).toBe('sid:PEER')
  expect(selfWaits.before.ref).toBe('sid:PEER')
})

test('名前を付けずに起動したセッションは、表示名 (題) ではなく session_name (ListAgents の名前) で宛先を当てる', async ($, on) => {
  world(on, deps([node('PEER', '1', 'agents-daemon-71', { session: 'agents-daemon Issue #296' })]))
  await $.session.start(START)

  const [peerWaits] = edgeAdds(context(await send($, 'agents-daemon-71')))
  expect(peerWaits.after).toEqual({ ref: 'sid:PEER', cwd: PEER_CWD })
  expect(context(await send($, 'agents-daemon Issue #296'))).toBe('')
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
