import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'

const SELF_ARGS = ['--session', 'SELF', '--cwd', CWD]
const DURING = (task: string) => [BIN, 'park', 'list', '--status', 'open', '--task', task, ...SELF_ARGS]
const STATUS = [BIN, 'status', '--board', ...SELF_ARGS]

// memo はメモ 1 つ。ボードの parking (sources は新しい順) にも park list の答え (sources は置いた順) にも使えるよう、
// source は 1 つにする。
function memo(id: string, title: string, at: string) {
  return { id, domain: 'home', repo: CWD, kind: 'aside', title, sources: [{ session_id: 'SELF', session: 'self', live: true, cwd: CWD, at }], status: 'open' }
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

// 置いた順に古いものから。band には新しいものを先に 3 件まで出す。
const MEMOS = [
  memo('m1', '古いメモ', '2026-10-17T09:00:00+09:00'),
  memo('m2', '二つ目', '2026-10-18T10:00:00+09:00'),
  memo('m3', '三つ目', '2026-10-18T11:00:00+09:00'),
  memo('m4', '新しいメモ', '2026-10-18T12:00:00+09:00'),
]

// board は status --board の答え。here がこのセッションの行を指し、その行の parking が band と hint 行のメモになる。
function board(links: unknown[] = LINKS, parking: unknown[] = MEMOS) {
  return {
    domain: 'home',
    daemon_running: true,
    columns: [],
    here: CWD,
    rows: [{ repo: CWD, title: 'agents-daemon', cards: [], parking }],
    graph: { nodes: NODES, links, longest_chain: ['A/1', 'A/2', 'B/1'], rows: [], rows_with_completed: [] },
  }
}

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

test('session.start で依存の数と longest chain の 1 行と、メモの行を band に出す。図は描かない', async ($, on) => {
  const w = world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  // hint 行の数と band のメモは同じボードから取り、park list は呼ばない。
  expect(w.ran).toEqual([STATUS])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /■ 1 {2}○ 2 {2}▸ 1 {2}↵ 0 {2}✓ 1 {2}◇ 4/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /longest chain: \? 論点 → ○ 段5 → ○ リリース/ })).toBeDefined()
    // 段の列も軸も、ノードの Button も無い。
    expect(await ui.find({ type: 'Text', text: /earlier/ })).toBeUndefined()
    expect(await ui.find({ key: 'node:A/1' })).toBeUndefined()
    expect(await ui.find({ key: 'fold' })).toBeUndefined()
    // メモは新しいものを 3 件まで。残りは数だけ。
    // 種類は park tool の kind の語のまま出す。
    expect(await ui.find({ key: 'park:m4', text: /\[aside\] 新しいメモ/ })).toBeDefined()
    expect(await ui.find({ key: 'park:m2' })).toBeDefined()
    expect(await ui.find({ key: 'park:m1' })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /\+1/ })).toBeDefined()
    // 今の見出しと「閉じる」は無い。
    expect(await ui.find({ type: 'Text', text: /この repo のパーキングのメモ/ })).toBeUndefined()
    expect(await ui.find({ key: 'park-band-close' })).toBeUndefined()
    await ui.unmount()
  }
})

test('辺もメモも無ければ band を出さない。メモだけあればメモの行を出す', async ($, on) => {
  const answers: Record<string, unknown> = { [STATUS.join(' ')]: board([], []) }
  const w = world(on, answers)
  await $.session.start(START)
  const empty = await mount($)
  expect(await empty.find({ type: 'Text', text: /■/ })).toBeUndefined()
  await empty.unmount()

  // 別のセッションが置いたメモも、10 秒の読み直しで出る。
  answers[STATUS.join(' ')] = board([], [MEMOS[3]])
  await w.clock.advance(10000)
  const memoOnly = await mount($)
  expect(await memoOnly.find({ key: 'park:m4' })).toBeDefined()
})

test('open でなくなったメモは、完了したタスクの途中で置いたものでも band から消え、全部片付いたら band ごと消える', async ($, on) => {
  const answers: Record<string, unknown> = { [STATUS.join(' ')]: board([], MEMOS), [DURING('3').join(' ')]: [MEMOS[0]] }
  const w = world(on, answers)
  await $.session.start(START)
  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', status: 'completed' } as never)
  const before = await mount($)
  expect(await before.find({ key: 'park:m1' })).toBeDefined()
  await before.unmount()

  // m1 をボードから捨てた (open でなくなった)。先頭に寄せたメモでも、次の読み直しで消える。
  answers[STATUS.join(' ')] = board([], MEMOS.slice(1))
  await w.clock.advance(10000)
  const after = await mount($)
  expect(await after.find({ key: 'park:m1' })).toBeUndefined()
  expect(await after.find({ key: 'park:m4' })).toBeDefined()
  await after.unmount()

  // 全部片付いたら band ごと消える。
  answers[STATUS.join(' ')] = board([], [])
  await w.clock.advance(10000)
  const gone = await mount($)
  expect(await gone.find({ key: 'park:m4' })).toBeUndefined()
  expect(await gone.find({ type: 'Text', text: /◇/ })).toBeUndefined()
})

test('survey が band を持っている間は譲る', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  const ui = await mount($, 'terminal', { ...BAND_PROPS, hasSurvey: true })
  expect(await ui.find({ type: 'Text', text: /■/ })).toBeUndefined()
})

test('band で選んだメモを、B の pane でも開く', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  const band = await mount($)
  await band.press({ key: 'park:m4' })
  const pane = await $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })
  expect(await pane.find({ key: 'discard' })).toBeDefined()
})

test('TaskUpdate で completed になったタスクの途中で置いたメモを先頭に寄せ、ボードを読み直す', async ($, on) => {
  const w = world(on, { [STATUS.join(' ')]: board(), [DURING('3').join(' ')]: [MEMOS[0]] })
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

test('メモは枠なしで描き、選んだものは行頭の › と太字で示す', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board() })
  await $.session.start(START)
  const ui = await mount($)
  expect((await ui.find({ key: 'park:m4' }))?.props.plain).toBe(true)
  expect((await ui.find({ key: 'box:park:m4' }))?.text).toMatch(/^ {2}\[aside\]/)
  await ui.press({ key: 'park:m4' })
  expect((await ui.find({ key: 'box:park:m4' }))?.text).toMatch(/^› \[aside\]/)
  // Text は key を持てないので、Button の子に太字の Text があるかで見る。
  const hasBold = async (key: string) =>
    ((await ui.find({ key }))?.children ?? []).some(c => typeof c === 'object' && c !== null && (c as { props?: { bold?: boolean } }).props?.bold === true)
  expect(await hasBold('park:m4')).toBe(true)
  expect(await hasBold('park:m3')).toBe(false)
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
