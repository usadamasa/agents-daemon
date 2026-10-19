import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'

const SELF_ARGS = ['--session', 'SELF', '--cwd', CWD]
const HERE = [BIN, 'park', 'list', '--here', '--status', 'open', ...SELF_ARGS]
const DURING = (task: string) => [BIN, 'park', 'list', '--status', 'open', '--task', task, ...SELF_ARGS]
const STATUS = [BIN, 'status', '--board', ...SELF_ARGS]

// memo は park list が出すメモ 1 つ。at は sources の最後 (いちばん新しく置かれたとき)。
function memo(id: string, title: string, at: string) {
  return { id, domain: 'home', repo: CWD, kind: 'aside', title, sources: [{ session_id: 'SELF', cwd: CWD, at }], status: 'open' }
}

function node(key: string, state: string, title: string, extra: Record<string, unknown> = {}) {
  const [sid = '', tid = ''] = key.split('/')
  return {
    key,
    kind: 'task',
    state,
    title,
    ref: `task:${sid}/${tid}`,
    cwd: CWD,
    session_id: sid,
    session: sid.toLowerCase(),
    live: true,
    depth: 0,
    downstream: 0,
    parked: 0,
    blocked_by: [] as string[],
    deadlock: false,
    ...extra,
  }
}

const NODES = [
  node('A/1', 'question', '論点', { downstream: 2 }),
  node('W/1', 'working', 'edge CLI', { downstream: 1, parked: 1 }),
  node('A/2', 'pending', '段5', { depth: 1, downstream: 1, blocked_by: ['A/1'] }),
  node('W/2', 'pending', 'skill', { depth: 1, blocked_by: ['W/1'] }),
  node('B/1', 'pending', 'リリース', { depth: 2, blocked_by: ['A/2', 'A/1'] }),
  node('C/1', 'completed', '済んだ'),
]

const LINKS = [
  { before: 'A/1', after: 'A/2', kind: 'task' },
  { before: 'A/2', after: 'B/1', kind: 'edge', edge: { id: 'e1', before: { ref: 'task:A/2', cwd: CWD, repo: CWD }, after: { ref: 'task:B/1', cwd: CWD, repo: CWD } } },
  { before: 'W/1', after: 'W/2', kind: 'task' },
]

function board(links: unknown[] = LINKS, parking: unknown[] = []) {
  return {
    domain: 'home',
    daemon_running: true,
    columns: [],
    rows: [{ repo: CWD, title: 'agents-daemon', cards: [], parking }],
    graph: { nodes: NODES, links, longest_chain: ['A/1', 'A/2', 'B/1'] },
  }
}

// 置いた順に古いものから。band には新しいものを先に 3 件まで出す。
const MEMOS = [
  memo('m1', '古いメモ', '2026-10-17T09:00:00+09:00'),
  memo('m2', '二つ目', '2026-10-18T10:00:00+09:00'),
  memo('m3', '三つ目', '2026-10-18T11:00:00+09:00'),
  memo('m4', '新しいメモ', '2026-10-18T12:00:00+09:00'),
]

function output(stdout: string, exitCode = 0): ProcessRunResult {
  return { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下にいる engine の代わりを立てる。process.run は argv を key にした答えを返し (無ければ [])、
// TaskUpdate は status を書き換えた結果を返す。
function world(on: On, answers: Record<string, unknown>) {
  const ran: string[][] = []
  mock.env(on, { HOME: '/home/u' })
  const clock = mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    if (e.argv[1] !== 'board-log') ran.push([...e.argv])
    return { value: output(JSON.stringify(answers[e.argv.join(' ')] ?? [])) }
  })
  // band を出さないときに engine が描くもの。
  on('ui.render', { component: 'AbovePrompt' }, async () => ({ type: 'engine', ref: 1 }))
  on('ui.render', { component: 'PromptHint' }, async () => ({ type: 'engine', ref: 1 }))
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  on('turn.complete', async () => ({ text: '' }))
  on('tool.call', { tool: 'TaskUpdate' }, async (_$, e) => {
    const input = e as unknown as { taskId: string; status?: string }
    const statusChange = input.status === undefined ? undefined : { from: 'in_progress', to: input.status }
    return { result: { success: true, taskId: input.taskId, updatedFields: input.status ? ['status'] : ['subject'], statusChange } } as never
  })
  return { ran, clock }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

const BAND_PROPS = {
  hasSurvey: false,
  isWorking: false,
  maxRows: 20,
  bodyColumns: 120,
  scroll: { offset: 0, bodyRows: 19 },
  view: {},
} as const

type T = Parameters<Parameters<typeof test>[1]>[0]

const mount = ($: T, surface: 'terminal' | 'desktop' = 'terminal', props: object = BAND_PROPS) =>
  $.ui.mount({ plugin: 'agents-daemon', surface, component: 'AbovePrompt', props: props as typeof BAND_PROPS })

const PANE_PROPS = { title: 'Tasks', isFocused: true, bodyColumns: 80, placement: 'dock', scroll: { offset: 0, bodyRows: 40 }, view: {} } as const

test('session.start で依存の数と longest chain、earlier → later の段、メモの行を band に出す', async ($, on) => {
  const w = world(on, { [HERE.join(' ')]: MEMOS, [STATUS.join(' ')]: board() })
  await $.session.start(START)
  // hint 行の数と band で同じメモの一覧を使い、list を 2 度呼ばない。
  expect(w.ran).toEqual([HERE, STATUS])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /■ 1 {2}○ 2 {2}▸ 1 {2}✓ 1 {2}◇ 4/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /longest chain: \? 論点 → ○ 段5 → ○ リリース/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /earlier/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /later/ })).toBeDefined()
    // 止まっているノードには下流の数、途中で置いたメモの数。
    expect(await ui.find({ key: 'node:A/1', text: /\? 論点 ↓2/ })).toBeDefined()
    expect(await ui.find({ key: 'node:W/1', text: /▸ edge CLI ◇1/ })).toBeDefined()
    expect(await ui.find({ key: 'node:B/1', text: /○ リリース/ })).toBeDefined()
    // メモは新しいものを 3 件まで。残りは数だけ。
    expect(await ui.find({ key: 'park:m4', text: /新しいメモ/ })).toBeDefined()
    expect(await ui.find({ key: 'park:m2' })).toBeDefined()
    expect(await ui.find({ key: 'park:m1' })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /\+1/ })).toBeDefined()
    // 今の見出しと「閉じる」は無い。
    expect(await ui.find({ type: 'Text', text: /この repo のパーキングのメモ/ })).toBeUndefined()
    expect(await ui.find({ key: 'park-band-close' })).toBeUndefined()
    await ui.unmount()
  }
})

test('完了は既定で畳み、Show completed で出して Hide completed で畳む', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  const ui = await mount($)
  expect(await ui.find({ key: 'node:C/1' })).toBeUndefined()
  await ui.press({ key: 'fold', text: /Show completed/ })
  expect(await ui.find({ key: 'node:C/1', text: /✓ 済んだ/ })).toBeDefined()
  await ui.press({ key: 'fold', text: /Hide completed/ })
  expect(await ui.find({ key: 'node:C/1' })).toBeUndefined()
})

test('辺もメモも無ければ band を出さない。メモだけあればメモの行を出す', async ($, on) => {
  const answers: Record<string, unknown> = { [STATUS.join(' ')]: board([]) }
  const w = world(on, answers)
  await $.session.start(START)
  const empty = await mount($)
  expect(await empty.find({ key: 'fold' })).toBeUndefined()
  await empty.unmount()

  answers[HERE.join(' ')] = [MEMOS[3]]
  await $.turn.complete({ answer: '', durationMs: 1, isAborted: false, turnId: 't1', reason: 'answer' })
  await w.clock.advance(10000)
  const memoOnly = await mount($)
  expect(await memoOnly.find({ key: 'park:m4' })).toBeDefined()
})

test('survey が band を持っている間は譲る', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  const ui = await mount($, 'terminal', { ...BAND_PROPS, hasSurvey: true })
  expect(await ui.find({ key: 'fold' })).toBeUndefined()
})

test('band で選んだノードとメモを、B の pane でも開く', async ($, on) => {
  world(on, { [HERE.join(' ')]: MEMOS, [STATUS.join(' ')]: board([...LINKS], [{ ...MEMOS[3], sources: [{ session_id: 'SELF', session: 'self', live: true, at: MEMOS[3]?.sources[0]?.at }] }]) })
  await $.session.start(START)
  const band = await mount($)
  await band.press({ key: 'node:A/2' })
  const pane = await $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })
  expect(await pane.find({ type: 'Text', text: /task:A\/2 · a/ })).toBeDefined()
  await pane.unmount()

  await band.press({ key: 'park:m4' })
  const again = await $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })
  expect(await again.find({ key: 'discard' })).toBeDefined()
})

test('TaskUpdate で completed になったタスクの途中で置いたメモを先頭に寄せ、ボードを読み直す', async ($, on) => {
  const w = world(on, { [HERE.join(' ')]: MEMOS, [STATUS.join(' ')]: board(), [DURING('3').join(' ')]: [MEMOS[0]] })
  await $.session.start(START)
  w.ran.length = 0

  // completed 以外の更新では引かない。
  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', subject: 'x' } as never)
  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', status: 'in_progress' } as never)
  expect(w.ran).toEqual([])

  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', status: 'completed' } as never)
  expect(w.ran).toEqual([DURING('3'), STATUS])

  const ui = await mount($)
  // 古くて 3 件から漏れていたメモも、完了したタスクの途中で置いたものなら先頭に出る。
  expect(await ui.find({ key: 'park:m1', text: /古いメモ/ })).toBeDefined()
  expect(await ui.find({ key: 'park:m4' })).toBeDefined()
  expect(await ui.find({ key: 'park:m2' })).toBeUndefined()
})

// cells は全角を 2 桁と数えた幅。
function cells(text: string): number {
  return [...text].reduce((n, ch) => n + ((ch.codePointAt(0) ?? 0) >= 0x3000 ? 2 : 1), 0)
}

test('ノードは段の列の幅に合わせて 1 行に切り詰める', async ($, on) => {
  const long = node('L/1', 'working', 'とても長いタイトル'.repeat(5), { depth: 1 })
  world(on, { [STATUS.join(' ')]: { ...board(), graph: { ...board().graph, nodes: [...NODES, long] } } })
  await $.session.start(START)
  // 段は 3 列。列の間に 1 桁ずつ空けて、1 列は (60 - 2) / 3 = 19 桁。枠の「[ 」「 ]」を除いて 15 桁に収める。
  const ui = await mount($, 'terminal', { ...BAND_PROPS, bodyColumns: 60 })
  const button = await ui.find({ key: 'node:L/1' })
  expect(button?.text).toMatch(/…$/)
  expect(cells(button?.text ?? '')).toBeLessThanOrEqual(15)
})

test('列のノードが band の高さに収まらなければ、残りを +n にまとめる', async ($, on) => {
  const extra = ['E/1', 'E/2', 'E/3'].map(k => node(k, 'pending', k))
  world(on, { [STATUS.join(' ')]: { ...board(), graph: { ...board().graph, nodes: [...NODES, ...extra] } } })
  await $.session.start(START)
  // 高さ 5 行から 1 行目と軸の 2 行を除いた 3 行。段 0 の 5 つのうち 2 つを出し、残りの 3 つを +3 にする。
  const ui = await mount($, 'terminal', { ...BAND_PROPS, maxRows: 5 })
  expect(await ui.find({ key: 'node:A/1' })).toBeDefined()
  expect(await ui.find({ key: 'node:W/1' })).toBeDefined()
  expect(await ui.find({ key: 'node:E/1' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /^\+3$/ })).toBeDefined()
})

test('10 秒ごとにボードを読み直す', async ($, on) => {
  const w = world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  w.ran.length = 0
  await w.clock.advance(9999)
  expect(w.ran).toEqual([])
  await w.clock.advance(1)
  expect(w.ran).toEqual([STATUS])
})
