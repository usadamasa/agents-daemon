import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const TOOL = 'mcp__agents-daemon__park'

type Ran = { argv: string[]; stdin?: string }

// world は plugin の下にいる engine の代わりを立てる。park の呼び出しは run が答える。
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
  // deps.tsx も同じ session.start で /deps を登録する。
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  return { ran, tools }
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

function item(id: string) {
  return { id, kind: 'aside', title: 't', sources: [{ session_id: 'SELF', cwd: CWD, at: '2026-10-19T01:00:00Z' }] }
}

// park add に答える。
function answer(created: boolean) {
  return () => output(JSON.stringify({ created, item: item('20261019T010000-abcd1234') }))
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

type Registered = { name: string; description: string; isDeferred: boolean; inputSchema: any }

test('session.start で park の tool を登録する', async ($, on) => {
  const w = world(on, answer(true))
  await $.session.start(START)

  expect(w.tools.map(t => t.name)).toEqual(['park', 'unpark'])
  const tool = w.tools[0] as Registered
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
  // 置いたメモの前提が作業の中で消えたら、unpark で取り下げる。
  expect(tool.description).toMatch(/前提が.*消えたら.*unpark/)

  // unpark はメモの id だけを受ける。
  const unpark = w.tools[1] as Registered
  expect(unpark.isDeferred).toBe(false)
  expect(unpark.inputSchema.required).toEqual(['id'])
  expect(Object.keys(unpark.inputSchema.properties)).toEqual(['id'])

  // session.start ではメモの一覧も依存図も読まない。
  expect(w.ran.map(r => r.argv)).toEqual([])
})

test('tool の呼び出しは park add に JSON を stdin で渡し、新しく置いたことを返す', async ($, on) => {
  const w = world(on, answer(true))
  await $.session.start(START)
  w.ran.length = 0

  const res = await $.tool.call({
    tool: TOOL,
    kind: 'aside',
    title: '保持期間の根拠が無い',
    detail: 'housekeeping.go の定数',
    default_taken: 'やらない',
  } as never)

  expect(w.ran[0]?.argv).toEqual([BIN, 'park', 'add'])
  expect(JSON.parse(w.ran[0]?.stdin ?? '')).toEqual({
    session_id: 'SELF',
    cwd: CWD,
    memo: { kind: 'aside', title: '保持期間の根拠が無い', detail: 'housekeeping.go の定数', default_taken: 'やらない' },
  })
  expect(String(res.result)).toMatch(/^メモを置きました \(20261019T010000-abcd1234\)。既定 \(やらない\) で進めてください/)
  // 置いた後に依存図は読み直さない (pane は開いている間だけ自分で読み直す)。
  expect(w.ran).toHaveLength(1)
})

test('既に置かれていたメモにまとまったら、そう伝える', async ($, on) => {
  const w = world(on, answer(false))
  await $.session.start(START)

  const res = await $.tool.call({ tool: TOOL, kind: 'scope', title: 't' } as never)
  expect(String(res.result)).toMatch(/^既に置かれていたメモ \(20261019T010000-abcd1234\) にまとめました。既定で進めてください/)
  expect(w.ran.at(-1)?.argv).toEqual([BIN, 'park', 'add'])
})

test('model が session_id を入れてきても、このセッションの ID で上書きする', async ($, on) => {
  const w = world(on, answer(true))
  await $.session.start(START)
  w.ran.length = 0

  await $.tool.call({ tool: TOOL, kind: 'aside', title: 't', session_id: 'OTHER' } as never)
  const req = JSON.parse(w.ran[0]?.stdin ?? '')
  expect(req.session_id).toBe('SELF')
  expect(req.memo.session_id).toBeUndefined()
})

test('park add が失敗したら、置けなかった理由を model へ返す', async ($, on) => {
  world(on, () => output('', 1, 'kind "decision" は aside / scope / preference のどれでもない'))
  await $.session.start(START)

  const res = await $.tool.call({ tool: TOOL, kind: 'decision', title: 't' } as never)
  // deny の文面は model にエラーの結果として届く。
  expect(res.deny).toMatch(/^メモを置けませんでした: kind "decision"/)
})

const UNPARK = 'mcp__agents-daemon__unpark'
const ID = '20261019T010000-abcd1234'

test('unpark はメモの id とこのセッションの ID を park withdraw に渡す', async ($, on) => {
  const w = world(on, () => output(JSON.stringify({ ...item(ID), status: 'discarded' })))
  await $.session.start(START)
  w.ran.length = 0

  const res = await $.tool.call({ tool: UNPARK, id: ID, session_id: 'OTHER' } as never)

  // 置いたセッションかと open かの判定はバイナリが持つ。session_id は model に書かせない。
  expect(w.ran[0]?.argv).toEqual([BIN, 'park', 'withdraw'])
  expect(JSON.parse(w.ran[0]?.stdin ?? '')).toEqual({ id: ID, session_id: 'SELF' })
  expect(String(res.result)).toMatch(new RegExp(`^メモ ${ID} を取り下げました`))
})

test('unpark が失敗したら、取り下げられなかった理由を model へ返す', async ($, on) => {
  world(on, () => output('', 1, `メモ ${ID} はほかのセッションも置いたので取り下げない。pane の Discard で捨てる`))
  await $.session.start(START)

  const res = await $.tool.call({ tool: UNPARK, id: ID } as never)
  expect(res.deny).toMatch(/^メモを取り下げられませんでした: .*pane の Discard/)
})
