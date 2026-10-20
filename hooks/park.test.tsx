import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const TOOL = 'mcp__agents-daemon__park'

type Ran = { argv: string[]; stdin?: string }

// world は plugin の下にいる engine の代わりを立てる。park と status の呼び出しは run が答え、status 行は statuses に、
// prompt の hint 行へ足した tail は tails に溜める。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult) {
  const ran: Ran[] = []
  const statuses: (string | undefined)[] = []
  const tails: (string | undefined)[] = []
  on('ui.render', { component: 'PromptHint' }, async (_$, e) => {
    tails.push((e.props as { tail?: string }).tail)
    return { type: 'engine', ref: 1 }
  })
  const tools: Record<string, unknown>[] = []
  mock.env(on, { HOME: '/home/u' })
  const clock = mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    ran.push({ argv: [...e.argv], stdin: e.init?.stdin })
    return { value: run(e.argv) }
  })
  on('ui.status', async (_$, e) => {
    statuses.push(e.text)
    return { value: undefined }
  })
  on('tool.register', async (_$, e) => {
    tools.push({ ...e })
    return { value: { tool: `mcp__agents-daemon__${e.name}` } }
  })
  // board.tsx も同じ session.start で /board を登録する。
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  return { ran, statuses, tails, tools, clock }
}

const HINT = { isDraft: false, isWorking: false, hint: '? for shortcuts' } as const

// hint は prompt の hint 行を描き、plugin がエンジンの行の末尾に足した tail を返す。
async function hint($: Parameters<Parameters<typeof test>[1]>[0], w: ReturnType<typeof world>) {
  const ui = await $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'PromptHint', props: HINT })
  await ui.unmount()
  return w.tails.at(-1)
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

const STATUS = [BIN, 'status', '--board', '--session', 'SELF', '--cwd', CWD]

function item(id: string) {
  return { id, kind: 'aside', title: 't', sources: [{ session_id: 'SELF', session: 'self', live: true, at: '2026-10-19T01:00:00Z' }] }
}

// board は status --board の答え。here の行の parking がこの repo の open のメモ (count 個)。
function board(count: number) {
  return {
    domain: 'home',
    daemon_running: true,
    columns: [],
    here: CWD,
    rows: [{ repo: CWD, title: 'agents-daemon', cards: [], parking: Array.from({ length: count }, (_, i) => item(`id${i}`)) }],
    graph: { nodes: [], links: [], longest_chain: [] },
  }
}

// add と status --board に答える。
function answer(count: number, created: boolean) {
  return (argv: readonly string[]) => {
    if (argv[2] === 'add') return output(JSON.stringify({ created, item: item('20261019T010000-abcd1234') }))
    return output(JSON.stringify(board(count)))
  }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

test('session.start で park の tool を登録し、その repo の open のメモの数を hint 行の末尾に出す', async ($, on) => {
  const w = world(on, answer(2, true))
  await $.session.start(START)

  expect(w.tools.length).toBe(1)
  const tool = w.tools[0] as { name: string; description: string; isDeferred: boolean; inputSchema: any }
  expect(tool.name).toBe('park')
  // 「聞く」の代わりとして prompt の tool 一覧に並べる。ToolSearch の裏に回さない。
  expect(tool.isDeferred).toBe(false)
  expect(tool.inputSchema.properties.kind.enum).toEqual(['aside', 'scope', 'preference'])
  expect(tool.inputSchema.required).toEqual(['kind', 'title'])
  // session_id と cwd は model に書かせない。
  expect(tool.inputSchema.properties.session_id).toBeUndefined()
  expect(tool.inputSchema.properties.cwd).toBeUndefined()
  expect(tool.description).toMatch(/進めるのに要る判断/)
  expect(tool.description).toMatch(/危ない操作の確認/)

  // メモの数は band (hooks/band.tsx) が読む status --board の here の行から取り、park list は呼ばない。
  expect(w.ran.map(r => r.argv)).toEqual([STATUS])
  // status 行はエンジンが警告の印付きで描くので使わない。hint 行は置き換えず、末尾に足す。
  expect(await hint($, w)).toBe('パーキングのメモ 2')
  expect(w.statuses).toEqual([])
})

test('tool の呼び出しは park add に JSON を stdin で渡し、新しく置いたことを返す', async ($, on) => {
  const w = world(on, answer(1, true))
  await $.session.start(START)
  w.ran.length = 0

  const res = await $.tool.call({
    tool: TOOL,
    kind: 'aside',
    title: '保持期間の根拠が無い',
    detail: 'housekeeping.go の定数',
    default_taken: 'やらない',
  } as never)

  expect(w.ran[0].argv).toEqual([BIN, 'park', 'add', '--cwd', CWD])
  expect(JSON.parse(w.ran[0].stdin ?? '')).toEqual({
    kind: 'aside',
    title: '保持期間の根拠が無い',
    detail: 'housekeeping.go の定数',
    default_taken: 'やらない',
    session_id: 'SELF',
  })
  expect(String(res.result)).toMatch(/^メモを置きました \(20261019T010000-abcd1234\)。既定 \(やらない\) で進めてください/)
  // 置いた後にボードを読み直し、数を更新する。
  expect(w.ran[1].argv).toEqual(STATUS)
  expect(await hint($, w)).toBe('パーキングのメモ 1')
})

test('既に置かれていたメモにまとまったら、そう伝える', async ($, on) => {
  const w = world(on, answer(1, false))
  await $.session.start(START)

  const res = await $.tool.call({ tool: TOOL, kind: 'scope', title: 't' } as never)
  expect(String(res.result)).toMatch(/^既に置かれていたメモ \(20261019T010000-abcd1234\) にまとめました。既定で進めてください/)
  expect(await hint($, w)).toBe('パーキングのメモ 1')
})

test('model が session_id を入れてきても、このセッションの ID で上書きする', async ($, on) => {
  const w = world(on, answer(1, true))
  await $.session.start(START)
  w.ran.length = 0

  await $.tool.call({ tool: TOOL, kind: 'aside', title: 't', session_id: 'OTHER' } as never)
  expect(JSON.parse(w.ran[0].stdin ?? '').session_id).toBe('SELF')
})

test('park add が失敗したら、置けなかった理由を model へ返す', async ($, on) => {
  const w = world(on, argv => (argv[2] === 'add' ? output('', 1, 'kind "decision" は aside / scope / preference のどれでもない') : output(JSON.stringify(board(0)))))
  await $.session.start(START)

  const res = await $.tool.call({ tool: TOOL, kind: 'decision', title: 't' } as never)
  // deny の文面は model にエラーの結果として届く。
  expect(res.deny).toMatch(/^メモを置けませんでした: kind "decision"/)
  expect(await hint($, w)).toBeUndefined()
})

test('open のメモが無ければ hint 行に足さず、band の 10 秒ごとの読み直しで数が変わる', async ($, on) => {
  let count = 0
  const w = world(on, () => output(JSON.stringify(board(count))))
  await $.session.start(START)
  expect(await hint($, w)).toBeUndefined()

  // 別のセッションが置いたメモも、ボードの読み直しで数に入る。
  count = 3
  await w.clock.advance(10000)
  expect(await hint($, w)).toBe('パーキングのメモ 3')
})

test('ほかの repo のメモは hint 行に数えない', async ($, on) => {
  const elsewhere = { ...board(0), rows: [{ repo: '/src/home/other', title: 'other', cards: [], parking: [item('x')] }] }
  const w = world(on, () => output(JSON.stringify(elsewhere)))
  await $.session.start(START)
  expect(await hint($, w)).toBeUndefined()
})
