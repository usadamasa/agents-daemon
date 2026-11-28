import type { On, ProcessRunResult, TurnCompleteInput } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

// #722: 共有の rex session にタブとして起きたセッションで、main のターンが answer で終わった後に timer がタブの名前を読み、
// $.model.fork で今の作業に合うかを問い、合わなければ tab-label で付け直す。最初の answer の後に 1 回、以後 4 ターンごと。

const POLL = 20_000
// rex の場所は mod が持たず、tab-label が自分の環境から読む。mod が見るのは共有のタブの印だけ。
const SHARED_ENV = { HOME: '/home/u', AGENTS_DAEMON_SHARED_REX_SESSION: '1' }

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

type TabReq = { label?: string }

// world は plugin の下の engine の代わり。tab-label は state.label を読み、label があれば付ける (形が外れれば exit 1)。
// model.fork は state.replies を順に返す。tab-label の stdin と fork の prompt を残す。
function world(on: On, env: Record<string, string> = SHARED_ENV, replies: unknown[] = []) {
  const state = { label: 'release-restart-verify', replies, tabs: [] as TabReq[], prompts: [] as string[], events: [] as string[] }
  mock.env(on, env)
  const clock = mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: '/src/x' }))
  on('process.run', async (_$, e) => {
    const req = JSON.parse(e.init?.stdin ?? '{}')
    if (e.argv[1] === 'event-log') {
      state.events.push((req as { event: string }).event)
      return { value: output('') }
    }
    if (e.argv[1] !== 'tab-label') return { value: output('', 1, 'unexpected') }
    const tab = req as TabReq
    state.tabs.push(tab)
    if (tab.label === undefined) return { value: output(JSON.stringify({ window_id: 'window:w', label: state.label, status: 'current' })) }
    if (tab.label.trim() !== tab.label || /[\u0000-\u001f\u007f]/.test(tab.label) || [...tab.label].length > 64) {
      return { value: output('', 1, '形が外れた') }
    }
    const status = tab.label === state.label ? 'unchanged' : 'renamed'
    state.label = tab.label
    return { value: output(JSON.stringify({ window_id: 'window:w', label: state.label, status })) }
  })
  on('model.fork', async (_$, e) => {
    state.prompts.push((e as { prompt: string }).prompt)
    return { value: state.replies.shift() ?? { isAnswered: true, text: 'SAME', usage: {} } } as never
  })
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  on('turn.complete', async (_$, e) => ({ text: e.answer }) as never)
  return { state, clock }
}

const START = { cwd: '/src/x', surface: 'terminal', isInteractive: true } as const

const answered = ($: Engine, extra: Partial<TurnCompleteInput> = {}) => $.turn.complete({ turnId: 't1', answer: '済', durationMs: 1, isAborted: false, reason: 'answer', ...extra } as never)

const reply = (text: string) => ({ isAnswered: true, text, usage: {} })

const sets = (tabs: TabReq[]) => tabs.filter(t => t.label !== undefined).map(t => t.label)

test('最初の answer の後にタブの名前を読み、作業に合わなければ fork の返した日本語の名前で付け直す', async ($, on) => {
  const w = world(on, SHARED_ENV, [reply('「タブ名の追従」\n補足')])
  await $.session.start(START)
  await w.clock.advance(POLL)
  expect(w.state.tabs).toEqual([])

  await answered($)
  await w.clock.advance(POLL)
  expect(w.state.tabs[0]).toEqual({})
  expect(w.state.prompts).toHaveLength(1)
  expect(w.state.prompts[0]).toContain('release-restart-verify')
  expect(sets(w.state.tabs)).toEqual(['タブ名の追従'])
  expect(w.state.label).toBe('タブ名の追従')
  expect(w.state.events).toContain('mod.tab.renamed')
})

test('SAME の返事では付け直さない', async ($, on) => {
  const w = world(on, SHARED_ENV, [reply('SAME')])
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  expect(w.state.prompts).toHaveLength(1)
  expect(sets(w.state.tabs)).toEqual([])
  expect(w.state.label).toBe('release-restart-verify')
})

test('2 回目からは 4 ターンごとに問い、そのたびに今の名前を読み直す (人が付け直した名前も基準にする)', async ($, on) => {
  const w = world(on, SHARED_ENV, [reply('SAME'), reply('Go のソートの違い')])
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  expect(w.state.prompts).toHaveLength(1)

  w.state.label = 'my-own-name'
  for (let i = 0; i < 3; i++) {
    await answered($)
    await w.clock.advance(POLL)
  }
  expect(w.state.prompts).toHaveLength(1)

  await answered($)
  await w.clock.advance(POLL)
  expect(w.state.prompts).toHaveLength(2)
  expect(w.state.prompts[1]).toContain('my-own-name')
  expect(sets(w.state.tabs)).toEqual(['Go のソートの違い'])
})

test('共有のタブの印が無ければ何もしない', async ($, on) => {
  const w = world(on, { HOME: '/home/u' })
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  expect(w.state.tabs).toEqual([])
  expect(w.state.prompts).toEqual([])
})

test('subagent のターンと answer でない終わり方は数えない', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await answered($, { agentId: 'agent-1' })
  await answered($, { reason: 'aborted' })
  await w.clock.advance(POLL)
  expect(w.state.prompts).toEqual([])
})

test('長い返事は 64 文字で切って付け直す (モデルは頼んだ字数を守らない)', async ($, on) => {
  const w = world(on, SHARED_ENV, [reply('あ'.repeat(70))])
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  expect(sets(w.state.tabs)).toEqual(['あ'.repeat(64)])
})

// 実機の fork が返した題 (頼んだ字数を超える、英単語と空白が混ざる) も、そのまま付く。
for (const title of ['Go の slices.Sort と sort.Slice の違い', 'Python の dataclass の機能の説明']) {
  test(`実機の返事「${title}」で付け直す`, async ($, on) => {
    const w = world(on, SHARED_ENV, [reply(title)])
    await $.session.start(START)
    await answered($)
    await w.clock.advance(POLL)
    expect(sets(w.state.tabs)).toEqual([title])
    expect(w.state.label).toBe(title)
    expect(w.state.events).not.toContain('mod.tab.failed')
  })
}

test('形の外れた返事や fork の失敗では付け直さず、理由をログに残す', async ($, on) => {
  const w = world(on, SHARED_ENV, [reply('制御\u0007文字'), { isAnswered: false, reason: 'api-error', status: 500, error: 'server', usage: {} }])
  await $.session.start(START)
  await answered($)
  await w.clock.advance(POLL)
  for (let i = 0; i < 4; i++) {
    await answered($)
    await w.clock.advance(POLL)
  }
  expect(w.state.prompts).toHaveLength(2)
  expect(w.state.label).toBe('release-restart-verify')
  expect(w.state.events.filter(e => e === 'mod.tab.failed')).toHaveLength(2)
})
