import type { On, ProcessRunResult, TurnCompleteInput } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine, MockClock } from 'claude-code/testing'

// companion (#368): main のターンが終わった後に timer が作業の記録を読み、turns が足りて推定の題が無ければ $.model.fork で
// 1 行の題を推定して work set に書く。

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const POLL = 20_000

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

type Record = { session_id: string; cwd: string; subject?: string; origin?: string; inferred?: string; turns: number; started_at: string; updated_at: string }

// world は plugin の下の engine の代わり。work get は state.record (null なら記録が無い) を返し、work set は inferred を記録に
// 写す。model.fork は state.replies を順に返す。呼ばれた argv と fork の回数を残す。
function world(on: On, record: Record | null, replies: unknown[] = [{ isAnswered: true, text: '「推定の題」\n補足は無視', usage: {} }]) {
  const state = { record, replies, ran: [] as unknown[][], forks: 0 }
  mock.env(on, { HOME: '/home/u' })
  const clock = mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    const req = JSON.parse(e.init?.stdin ?? '{}') as { inferred?: string }
    state.ran.push([...e.argv, req])
    if (e.argv[1] === 'event-log') return { value: output('') }
    if (state.record === null) return { value: output('', 1, '記録が無い') }
    if (e.argv[2] === 'set') state.record = { ...state.record, inferred: req.inferred }
    return { value: output(JSON.stringify(state.record)) }
  })
  on('model.fork', async () => {
    state.forks++
    return { value: state.replies.shift() } as never
  })
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  on('turn.complete', async (_$, e) => ({ text: e.answer }) as never)
  return { ...state, clock, get record() { return state.record }, get ran() { return state.ran }, get forks() { return state.forks } }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

const record = (turns: number, extra: Partial<Record> = {}): Record => ({
  session_id: 'SELF', cwd: CWD, subject: '最初の prompt', origin: 'prompt', turns, started_at: '2026-11-07T13:00:00Z', updated_at: '2026-11-07T13:00:00Z', ...extra,
})

const answered = ($: Engine, extra: Partial<TurnCompleteInput> = {}) => $.turn.complete({ turnId: 't1', answer: '済', durationMs: 1, isAborted: false, reason: 'answer', ...extra } as never)

const workCalls = (ran: unknown[][]) => ran.filter(argv => argv[1] === 'work').map(argv => [argv[2], argv[3]])

test('main のターンが終わり、turns が 3 以上で推定の題が無ければ fork で題を推定して work set に書く', async ($, on) => {
  const w = world(on, record(3))
  await $.session.start(START)
  await answered($)
  expect(w.forks).toBe(0)

  await w.clock.advance(POLL)
  expect(w.forks).toBe(1)
  expect(workCalls(w.ran)).toEqual([
    ['get', { session_id: 'SELF' }],
    ['set', { session_id: 'SELF', inferred: '推定の題' }],
  ])
  expect(w.record?.inferred).toBe('推定の題')
  expect(w.ran.some(argv => argv[1] === 'event-log' && JSON.stringify(argv[2]).includes('mod.work.inferred'))).toBe(true)

  // 題がある記録には起こさない (次のターンの後も fork しない)。
  await answered($)
  await w.clock.advance(POLL)
  expect(w.forks).toBe(1)
})

test('turns が足りない記録には起こさず、印が無ければ記録も読まない', async ($, on) => {
  const w = world(on, record(2))
  await $.session.start(START)
  await w.clock.advance(POLL)
  expect(workCalls(w.ran)).toEqual([])

  await answered($)
  await w.clock.advance(POLL)
  expect(w.forks).toBe(0)
  expect(workCalls(w.ran)).toEqual([['get', { session_id: 'SELF' }]])
})

test('記録の無いセッション (work get が exit 1) には起こさない', async ($, on) => {
  const w = world(on, null)
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  expect(w.forks).toBe(0)
  expect(workCalls(w.ran)).toEqual([['get', { session_id: 'SELF' }]])
})

test('subagent のターンと answer でない終わり方では印を置かない', async ($, on) => {
  const w = world(on, record(5))
  await $.session.start(START)
  await answered($, { agentId: 'agent-1' })
  await answered($, { reason: 'aborted' })
  await w.clock.advance(POLL)
  expect(w.forks).toBe(0)
  expect(workCalls(w.ran)).toEqual([])
})

test('推定が失敗したら書かずに理由をログに残し、やり直しは上限 2 回まで (InferBounded)', async ($, on) => {
  const w = world(on, record(4), [{ isAnswered: false, reason: 'api-error', status: 500, error: 'server', usage: {} }, { isAnswered: true, text: '\n\n', usage: {} }, { isAnswered: true, text: '3 回目', usage: {} }])
  await $.session.start(START)
  for (let i = 0; i < 3; i++) {
    await answered($)
    await w.clock.advance(POLL)
  }
  expect(w.forks).toBe(2)
  expect(workCalls(w.ran).filter(([sub]) => sub === 'set')).toEqual([])
  const failed = w.ran.filter(argv => argv[1] === 'event-log' && JSON.stringify(argv[2]).includes('mod.work.infer.failed'))
  expect(failed).toHaveLength(2)
  expect(w.record?.inferred).toBeUndefined()
})

test('返事は引用符と空白を落として 80 字で切る', async ($, on) => {
  const long = 'あ'.repeat(90)
  const w = world(on, record(3), [{ isAnswered: true, text: `  '${long}'  `, usage: {} }])
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  const set = workCalls(w.ran).find(([sub]) => sub === 'set')
  expect((set?.[1] as { inferred: string }).inferred).toBe('あ'.repeat(80))
})
