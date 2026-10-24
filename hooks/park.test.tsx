import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const TOOL = 'mcp__agents-daemon__park'

type Ran = { argv: string[]; stdin?: string }

// world は plugin の下にいる engine の代わりを立てる。park と status の呼び出しは run が答える。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult) {
  const ran: Ran[] = []
  const tools: Record<string, unknown>[] = []
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    ran.push({ argv: [...e.argv], stdin: e.init?.stdin })
    return { value: run(e.argv) }
  })
  on('tool.register', async (_$, e) => {
    tools.push({ ...e })
    return { value: { tool: `mcp__agents-daemon__${e.name}` } }
  })
  // board.tsx も同じ session.start で /board を登録する。
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  return { ran, tools }
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

test('session.start で park の tool を登録する', async ($, on) => {
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

  // メモの一覧は band (hooks/band.tsx) が読む status --board から取り、park list は呼ばない。
  expect(w.ran.map(r => r.argv)).toEqual([STATUS])
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
  // 置いた後にボードを読み直し、band のメモを更新する。
  expect(w.ran[1].argv).toEqual(STATUS)
})

test('既に置かれていたメモにまとまったら、そう伝える', async ($, on) => {
  const w = world(on, answer(1, false))
  await $.session.start(START)

  const res = await $.tool.call({ tool: TOOL, kind: 'scope', title: 't' } as never)
  expect(String(res.result)).toMatch(/^既に置かれていたメモ \(20261019T010000-abcd1234\) にまとめました。既定で進めてください/)
  expect(w.ran.at(-1)?.argv).toEqual(STATUS)
})

test('model が session_id を入れてきても、このセッションの ID で上書きする', async ($, on) => {
  const w = world(on, answer(1, true))
  await $.session.start(START)
  w.ran.length = 0

  await $.tool.call({ tool: TOOL, kind: 'aside', title: 't', session_id: 'OTHER' } as never)
  expect(JSON.parse(w.ran[0].stdin ?? '').session_id).toBe('SELF')
})

test('park add が失敗したら、置けなかった理由を model へ返す', async ($, on) => {
  world(on, argv => (argv[2] === 'add' ? output('', 1, 'kind "decision" は aside / scope / preference のどれでもない') : output(JSON.stringify(board(0)))))
  await $.session.start(START)

  const res = await $.tool.call({ tool: TOOL, kind: 'decision', title: 't' } as never)
  // deny の文面は model にエラーの結果として届く。
  expect(res.deny).toMatch(/^メモを置けませんでした: kind "decision"/)
})
