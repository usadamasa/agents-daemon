import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

import { CHAIN_COLOR, COLOR } from './graph'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
// CHAIN は longest chain の罫線の色 (hooks/graph.ts の CHAIN_COLOR)。
const CHAIN = 'success'
const CWD = '/src/home/agents-daemon'

// world は plugin の下にいる engine の代わりを立てる。process.run は run が答え、呼ばれた argv (バイナリの後) を
// ran に残す。出来事のログ (hooks/log.ts) は log.test.tsx が見るので残さない。now は mod の時計の始点
// (古いメモの判定に使う)。submitted は自セッションの入力へ渡した文面 (prompt.submit)。drop を渡すと入らなかった
// ことにする。undelivered を渡すと peer へのメッセージが届かなかったことにする。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult = () => output('{}'), now = Date.parse('2026-10-19T00:00:00Z'), drop = '', undelivered = '') {
  const ran: string[][] = []
  const opened: string[] = []
  const closed: string[] = []
  const sent: { to: unknown; text: string }[] = []
  const submitted: string[] = []
  // fed は spawn の stdin に渡した本文。
  const fed: string[] = []
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on, { now })
  on('ui.open', async (_$, e) => {
    opened.push(e.id)
    return { value: { isPlaced: true } }
  })
  on('ui.close', async (_$, e) => {
    closed.push(e.id)
    return { value: undefined }
  })
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    if (e.argv[1] !== 'event-log') ran.push([...e.argv])
    if (e.argv[1] === 'spawn') fed.push(e.init?.stdin ?? '')
    return { value: run(e.argv) }
  })
  on('session.send', async (_$, e) => {
    sent.push({ to: e.to, text: e.text })
    if (undelivered !== '') return { isDelivered: false as const, reason: undelivered }
    return { isDelivered: true as const }
  })
  on('prompt.submit', async (_$, e) => {
    if (drop !== '') return { drop }
    submitted.push(e.text)
    return { text: e.text }
  })
  return { ran, opened, closed, sent, submitted, fed }
}

// SPAWNED は `agents-daemon spawn` の stdout。
const SPAWNED = '{"session_id":"NEW","pane_id":"block:b1","foreground":true}'

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// operations は ran のうち、操作の後の読み直し (deps) を除いたもの。
function operations(ran: string[][]): string[][] {
  return ran.filter(argv => argv[1] !== 'deps')
}

// depsWorld は deps に data を返し、spawn は起こしたセッションを、それ以外 (park、edge) は成功を返す。
function depsWorld(on: On, data: unknown = DEPS, now?: number, drop?: string, undelivered?: string) {
  return world(on, argv => (argv[1] === 'deps' ? output(JSON.stringify(data)) : argv[1] === 'spawn' ? output(SPAWNED) : output('{"created":true}')), now, drop, undelivered)
}

// node はタスクのノード。key は task:<sid>/<tid> の Ref で、session_id と task_id はそこから埋める。
function node(key: string, state: string, title: string, extra: Record<string, unknown> = {}) {
  const [sid = '', tid] = key.replace(/^task:/, '').split('/')
  return {
    key,
    kind: 'task',
    state,
    title,
    endpoint: true,
    task_id: tid,
    cwd: CWD,
    session_id: sid,
    session: sid.toLowerCase(),
    live: state !== 'exited',
    depth: 0,
    downstream: 0,
    parked: 0,
    memos: [] as { id: string; title: string; status: string }[],
    blocked_by: [] as string[],
    deadlock: false,
    halted: false,
    stuck: false,
    ...extra,
  }
}

const ep = (ref: string) => ({ ref, cwd: CWD, repo: CWD })

// 依存の DAG (internal/deps の graph)。Q/1 が質問で止まり、その下流に 3 つ。X/1 と X/2 は待ちの輪。
// W/1 は進行中で、途中で置いたメモが 1 つ。D/1 はターンを終えた。issue:o/r#9 は置き場のノードで、その下流に P/1。
const NODES = [
  node('task:Q/1', 'question', '論点を決める', { session: 'design', downstream: 3, halted: true, stuck: true }),
  node('task:X/1', 'pending', 'x を待つ', { session: 'loop', downstream: 1, blocked_by: ['task:X/2'], deadlock: true, stuck: true }),
  node('task:X/2', 'pending', 'y を待つ', { session: 'loop', downstream: 1, blocked_by: ['task:X/1'], deadlock: true, stuck: true }),
  node('task:Q/2', 'pending', '段5', { session: 'design', downstream: 2, blocked_by: ['task:Q/1'] }),
  node('task:W/1', 'working', 'edge の CLI', { session: 'edge-cli', downstream: 1, parked: 1 }),
  {
    ...node('issue:o/r#9', 'pending', 'issue:o/r#9', { downstream: 1 }),
    kind: 'ref',
    task_id: undefined,
    session_id: undefined,
    session: undefined,
    live: false,
  },
  node('task:R/1', 'pending', 'リリース', { session: 'release', downstream: 1, blocked_by: ['task:Q/2', 'task:Q/1'] }),
  node('task:D/1', 'done', '要約を書いた', { session: 'doner' }),
  node('task:W/2', 'pending', 'skill から辺を書く', { session: 'edge-cli', blocked_by: ['task:W/1'] }),
  node('task:P/1', 'pending', '後続', { session: 'peer', blocked_by: ['issue:o/r#9'] }),
  node('task:V/1', 'pending', '確認', { session: 'verify', blocked_by: ['task:R/1', 'task:Q/2', 'task:Q/1'] }),
  node('task:G/1', 'exited', '残った作業', { session: 'gone' }),
  node('task:C/1', 'completed', '済んだ'),
  node('task:SELF/1', 'working', 'このセッションの作業', { session: 'self', description: '作業の詳細' }),
]

const LINKS = [
  { before: 'task:Q/1', after: 'task:Q/2', kind: 'task' },
  { before: 'task:Q/2', after: 'task:R/1', kind: 'edge', edge: { id: 'e1', before: ep('session:design'), after: ep('task:R/1') } },
  { before: 'task:R/1', after: 'task:V/1', kind: 'edge', edge: { id: 'e2', before: ep('task:R/1'), after: ep('task:V/1') } },
  { before: 'task:W/1', after: 'task:W/2', kind: 'task' },
  { before: 'task:X/1', after: 'task:X/2', kind: 'task' },
  { before: 'task:X/2', after: 'task:X/1', kind: 'task' },
  { before: 'issue:o/r#9', after: 'task:P/1', kind: 'edge', edge: { id: 'e3', before: ep('issue:o/r#9'), after: ep('task:P/1') } },
]

// レーンの行 (internal/deps/dag の Rows を Nodes の順で組んだもの)。鎖は Q → R → V と W、issue → P。輪の X は最後。
const ROWS = [
  'task:Q/1',
  'task:Q/2',
  'task:R/1',
  'task:V/1',
  'task:W/1',
  'task:W/2',
  'issue:o/r#9',
  'task:P/1',
  'task:D/1',
  'task:G/1',
  'task:SELF/1',
  'task:X/1',
  'task:X/2',
].map(node => ({ lanes: '*', node }))
const ROWS_WITH_COMPLETED = [...ROWS.slice(0, 10), { lanes: '*', node: 'task:C/1' }, ...ROWS.slice(10)]

// メモは W/1 の途中と、終了した G が置いた。
const MEMO = {
  id: '20261018T090000-aaaa',
  kind: 'aside',
  origin: 'placed',
  title: '保持期間の根拠が無い',
  detail: '定数にコメントが無い',
  files: ['internal/parking/parking.go'],
  default_taken: 'やらない',
  sources: [
    { session_id: 'W', task_ref: 'task:W/1', session: 'edge-cli', live: true, detail: '別の定数も同じ', at: '2026-10-18T19:30:00+09:00' },
    { session_id: 'G', session: 'gone', live: false, at: '2026-10-18T16:28:44Z' },
  ],
}

function session(session_id: string, session: string, subject: string) {
  return { session_id, session, subject, live: true }
}

// repos はメモの送り先 (repo の生きているセッション) を決めるのに使う。終了した G は sessions に載らない。
const DEPS = {
  daemon_running: true,
  updated_at: '2026-10-18T09:00:00Z',
  here: CWD,
  stale_memo_days: 30,
  repos: [
    {
      repo: CWD,
      title: 'agents-daemon',
      sessions: [session('W', 'edge-cli', 'edge の CLI'), session('SELF', 'self', 'このセッションの作業')],
      memos: [MEMO],
    },
    { repo: '/src/home/dotfile', title: 'dotfile', sessions: [session('P', 'peer', '後続')], memos: [] },
  ],
  graph: { nodes: NODES, links: LINKS, longest_chain: ['task:Q/1', 'task:Q/2', 'task:R/1', 'task:V/1'], rows: ROWS, rows_with_completed: ROWS_WITH_COMPLETED },
}

// withNodes は依存図にノードを足し、レーンの行の末尾に 1 本ずつ並べる。
function withNodes(...extra: Record<string, unknown>[]) {
  const rows = extra.map(n => ({ lanes: '*', node: n.key as string }))
  return {
    ...DEPS,
    graph: { ...DEPS.graph, nodes: [...NODES, ...extra], rows: [...ROWS, ...rows], rows_with_completed: [...ROWS_WITH_COMPLETED, ...rows] },
  }
}

// 利用者が composer で /deps を打ったときの command.run。
const OPEN_DEPS = {
  command: 'deps',
  args: '',
  origin: { kind: 'composer' },
  presentation: { isFullscreen: true, columns: 160 },
} as const

const PANE_PROPS = {
  title: 'Tasks',
  isFocused: true,
  bodyColumns: 80,
  placement: 'dock',
  scroll: { offset: 0, bodyRows: 40 },
  view: {},
} as const

type T = Engine

const mount = ($: T, surface: 'terminal' | 'desktop' | 'mobile' = 'terminal', props: object = PANE_PROPS) =>
  $.ui.mount({ plugin: 'agents-daemon', surface, component: 'Pane', props: props as typeof PANE_PROPS, requestId: 'agents-daemon-deps' })

type UI = Awaited<ReturnType<typeof mount>>

// options は Select の選択肢。
async function options(ui: UI, key: string): Promise<unknown> {
  return (await ui.find({ key }))?.props.options
}

// pickAndPress は宛先の Select (pick:<slot>) で value を選び、その操作のボタン (<slot>) を押す。
async function pickAndPress(ui: UI, slot: string, value: string) {
  await ui.select({ key: `pick:${slot}`, value })
  await ui.press({ key: slot })
}

const SELF_ARGS = ['--session', 'SELF', '--cwd', CWD]
// claim は送る前にメモを取る park set (宛先を同じ park set で書く)、REVERT は届かなかったメモを戻す park set。
const claim = (...to: string[]) => [BIN, 'park', 'set', MEMO.id, '--status', 'handed', '--from', 'open', ...to, ...SELF_ARGS]
const CLAIM_W = claim('--to-session', 'W')
const CLAIM_SELF = claim('--to-session', 'SELF')
const CLAIM_DOTFILE = claim('--to-repo', '/src/home/dotfile')
const REVERT = [BIN, 'park', 'set', MEMO.id, '--status', 'open', '--from', 'handed', ...SELF_ARGS]

test('fullscreen でなければ /deps は開かず、その旨を返す', async ($, on) => {
  const w = depsWorld(on)
  const result = await $.command.run({ ...OPEN_DEPS, presentation: { isFullscreen: false, columns: 160 } })
  expect(result.text).toMatch(/fullscreen/)
  expect(w.opened).toEqual([])
  expect(w.ran).toEqual([])
})

test('/deps は開いたセッションの cwd で deps を読み、dock に開く', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  expect(w.ran[0]).toEqual([BIN, 'deps', '--cwd', CWD])
  expect(w.opened).toEqual(['agents-daemon-deps'])
})

test('入力欄の上 (inline) に描かれたら pane を閉じ、閉じたことを toast で知らせる', async ($, on) => {
  const w = depsWorld(on)
  const toasts: string[] = []
  on('ui.toast', async (_$, e) => {
    toasts.push(e.text)
    return { value: undefined }
  })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($, 'terminal', { ...PANE_PROPS, placement: 'inline' })
  expect(w.closed).toEqual(['agents-daemon-deps'])
  expect(toasts[0]).toMatch(/110 桁/)
  expect(await ui.find({ key: 'row:task:Q/1' })).toBeUndefined()
  await ui.unmount()
  // 閉じるまでに inline の描画がもう一度走っても、知らせは 1 回だけ。
  const again = await mount($, 'terminal', { ...PANE_PROPS, placement: 'inline' })
  await again.unmount()
  expect(toasts).toHaveLength(1)
})

test('レーンの行を上から描く: 罫線は薄く、ノードは記号と title、つなぐだけの行は罫線だけ', async ($, on) => {
  // W/1 → R/1 の辺 (セッションをまたぐ、破線) が Q/1 → R/1 に合流し、R/1 の右を別のレーンが通る見本。
  const rows = [
    { lanes: '*', node: 'task:Q/1' },
    { lanes: '│ *', node: 'task:W/1' },
    { lanes: '├┄┘' },
    { lanes: '* │', node: 'task:R/1' },
  ]
  depsWorld(on, { ...DEPS, graph: { ...DEPS.graph, rows, rows_with_completed: rows } })
  await $.command.run(OPEN_DEPS)
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect((await ui.find({ key: 'box:row:task:Q/1' }))?.text).toMatch(/^ {2}\? 論点を決める ↓3$/)
    expect((await ui.find({ key: 'box:row:task:W/1' }))?.text).toMatch(/^ {2}│ ▸ edge の CLI ◇1$/)
    expect((await ui.find({ key: 'box:lane:2' }))?.text).toMatch(/^ {2}├┄┘$/)
    expect((await ui.find({ key: 'box:row:task:R/1' }))?.text).toMatch(/^ {2}○ │ リリース$/)
    // 行に無いノードは描かない。節の見出しも無い。
    expect(await ui.find({ key: 'row:task:V/1' })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /blocked \d/ })).toBeUndefined()
    await ui.unmount()
  }
})

test('longest chain の辺の罫線 (marks の桁) は色を付けて描き、ほかの罫線は薄いまま (#195)', async ($, on) => {
  const rows = [
    { lanes: '*', node: 'task:Q/1' },
    { lanes: '├─┐', marks: [0, 1, 2] },
    { lanes: '* ┆', node: 'task:W/1', marks: [2] },
    { lanes: '┌─┘', marks: [0, 1, 2] },
    { lanes: '│ *', node: 'task:Q/2', marks: [0] },
  ]
  depsWorld(on, { ...DEPS, graph: { ...DEPS.graph, rows, rows_with_completed: rows } })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect((await ui.find({ key: 'box:lane:1' }))?.text).toMatch(/^ {2}├─┐$/)
  expect((await ui.find({ type: 'Text', text: /^├─┐$/ }))?.props.color).toBe(CHAIN)
  expect((await ui.find({ type: 'Text', text: /^┌─┘$/ }))?.props.color).toBe(CHAIN)
  // ノードの右のレーンも、左のレーンも、印の桁だけを色の Text に分ける。
  expect((await ui.find({ key: 'box:row:task:W/1' }))?.text).toMatch(/^ {2}▸ ┆ edge の CLI ◇1$/)
  expect((await ui.find({ type: 'Text', text: /^┆$/ }))?.props.color).toBe(CHAIN)
  expect((await ui.find({ key: 'box:row:task:Q/2' }))?.text).toMatch(/^ {2}│ ○ 段5$/)
  expect((await ui.find({ type: 'Text', text: /^│$/ }))?.props.color).toBe(CHAIN)
})

test('色は 1 つの鍵に 1 つの意味: 鎖の色はノードの状態の色と重ならず、メモの見出しには色を付けない', async ($, on) => {
  expect(Object.values(COLOR)).not.toContain(CHAIN_COLOR)
  expect(CHAIN_COLOR).toBe(CHAIN)
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  const heading = await ui.find({ type: 'Text', text: /^◇ parking 1$/ })
  expect(heading?.props.bold).toBe(true)
  expect(heading?.props.color).toBeUndefined()
})

test('長い title は pane の幅からレーンの分を除いて 1 行に切り詰める', async ($, on) => {
  const long = node('task:L/1', 'working', 'とても長いタイトル'.repeat(10), { session: 'long' })
  const rows = [...ROWS, { lanes: '│ *', node: 'task:L/1' }]
  depsWorld(on, { ...withNodes(long), graph: { ...withNodes(long).graph, rows, rows_with_completed: rows } })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($, 'terminal', { ...PANE_PROPS, bodyColumns: 40 })
  const row = await ui.find({ key: 'box:row:task:L/1' })
  expect(row?.text).toMatch(/^ {2}│ ▸ (とても長いタイトル)+.*…$/)
  // 行頭の印とレーンを含めて pane の幅に収める (全角は 2 桁)。行は枠を描かないので、その分も title に使う。
  const cells = [...(row?.text ?? '')].reduce((n, ch) => n + ((ch.codePointAt(0) ?? 0) >= 0x3000 ? 2 : 1), 0)
  expect(cells).toBeLessThanOrEqual(40)
  expect(cells).toBeGreaterThan(38)
})

test('行は枠なしで描き、選んだ行は行頭の › と太字で示す', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  for (const key of ['row:task:G/1', `park:${MEMO.id}`]) {
    expect((await ui.find({ key }))?.props.plain).toBe(true)
  }
  expect((await ui.find({ key: 'box:row:task:G/1' }))?.text).toMatch(/^ {2}⊘/)
  await ui.press({ key: 'row:task:G/1' })
  expect((await ui.find({ key: 'box:row:task:G/1' }))?.text).toMatch(/^› ⊘/)
  // Text は key を持てないので、行の Button の子に太字の Text があるかで見る。
  const hasBold = async (key: string) =>
    ((await ui.find({ key }))?.children ?? []).some(c => typeof c === 'object' && c !== null && (c as { props?: { bold?: boolean } }).props?.bold === true)
  expect(await hasBold('row:task:G/1')).toBe(true)
  expect(await hasBold('row:task:W/1')).toBe(false)
})

test('選んだノードの詳細は枠で囲み、操作ボタンは枠の最後の行に枠付きで並べる', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:G/1' })
  expect((await ui.find({ key: 'detail:task:G/1' }))?.props.borderStyle).toBe('round')
  const actions = await ui.find({ key: 'actions:task:G/1' })
  expect(actions?.props.flexDirection).toBe('row')
  expect(actions?.text).toMatch(/\+ Add blocker.*To parking/)
  for (const key of ['add-blocker', 'adopt']) {
    expect((await ui.find({ key }))?.props.plain).toBeUndefined()
  }
})

test('選んだメモの詳細も枠で囲み、操作ボタンを枠の最後の行に並べる', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect((await ui.find({ key: 'memo' }))?.props.borderStyle).toBe('round')
  const actions = await ui.find({ key: 'actions:memo' })
  expect(actions?.props.flexDirection).toBe('row')
  // 宛先は Select に畳み、操作のボタンは宛先の数によらず 3 つ (#308)。
  expect(actions?.text).toMatch(/^Discard\s*To task\s*Ask for issue$/)
})

test('辺の端点にできないノード (TodoWrite の項目) には Add blocker の代わりに理由を出す', async ($, on) => {
  const todo = { ...node('todo:T/0', 'working', 'todo の項目', { session: 'todo' }), kind: 'todo', endpoint: false, task_id: undefined, session_id: 'T' }
  depsWorld(on, withNodes(todo))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:todo:T/0' })
  expect(await ui.find({ key: 'add-blocker' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /辺の端点にできないタスク/ })).toBeDefined()
})

test('数の行の下にレーンの行を全部描き、完了は Show completed で出す', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /の依存図/ })).toBeUndefined()
    // 数: 止まっているノード、その下流、進行中、待っている、完了、メモ。
    expect(await ui.find({ type: 'Text', text: /■ 2 {2}○ 4 {2}▸ 2 {2}↵ 1 {2}✓ 1 {2}◇ 1/ })).toBeDefined()
    // 止まっているノードには下流の数、途中で置いたメモの数、待ちの輪の印。
    expect(await ui.find({ key: 'row:task:Q/1', text: /\? 論点を決める ↓3/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:Q/2', text: /^○ 段5$/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:X/1', text: /○ x を待つ ↓1 ⟳/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:X/2', text: /⟳/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:W/1', text: /^▸ edge の CLI ◇1$/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:D/1', text: /↵ 要約を書いた/ })).toBeDefined()
    expect(await ui.find({ key: 'row:issue:o/r#9', text: /^○ issue:o\/r#9$/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:P/1', text: /○ 後続/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:G/1', text: /⊘ 残った作業/ })).toBeDefined()
    expect(await ui.find({ key: 'row:task:V/1', text: /○ 確認/ })).toBeDefined()
    // 完了は既定で出さない。
    expect(await ui.find({ key: 'row:task:C/1' })).toBeUndefined()
    await ui.unmount()
  }

  const ui = await mount($)
  expect(await ui.find({ key: 'fold', text: /Show completed/ })).toBeDefined()
  await ui.press({ key: 'fold' })
  expect(await ui.find({ key: 'row:task:C/1', text: /✓ 済んだ/ })).toBeDefined()
  expect(await ui.find({ key: 'fold', text: /Hide completed/ })).toBeDefined()
  await ui.press({ key: 'fold' })
  expect(await ui.find({ key: 'row:task:C/1' })).toBeUndefined()
})

test('Show completed は枠なしで凡例の枠の中に置き、数の行には置かない', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect((await ui.find({ key: 'fold' }))?.props.plain).toBe(true)
  expect((await ui.find({ key: 'legend' }))?.text).toMatch(/Show completed/)
  expect((await ui.find({ key: 'content' }))?.text).not.toMatch(/Show completed/)
})

test('完了したノードが無ければ Show completed を出さない', async ($, on) => {
  depsWorld(on, { ...DEPS, graph: { ...DEPS.graph, nodes: NODES.filter(n => n.state !== 'completed') } })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ key: 'fold' })).toBeUndefined()
})

test('記号の凡例を pane の一番下に薄く置き、依存図が短くても底に寄せる', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  // root は中身と凡例の 2 つだけを持ち、pane の高さに満たないときは凡例を底へ寄せる。
  const root = await ui.find({ key: 'root' })
  expect(root?.props.minHeight).toBe(40)
  expect(root?.props.justifyContent).toBe('space-between')
  expect(root?.children).toHaveLength(2)
  const legend = await ui.find({ key: 'legend' })
  // 数の行の記号、行の状態の記号、行の末尾の印を全部載せる。語は OSC 7501 の state と graph の欄の名前。
  for (const icon of ['■', '○', '▸', '↵', '✓', '◇', '?', '!', '⧗', '✗', '⊘', '·', '↓', '⟳']) {
    expect(legend?.text).toContain(icon)
  }
  expect(legend?.text).toMatch(/■ blocked/)
  expect(legend?.text).toMatch(/⧗ limit/)
  // 凡例は詳細と同じ丸い薄い枠で囲む。
  expect(legend?.props.borderStyle).toBe('round')
  expect(legend?.props.borderDimColor).toBe(true)
  // 数の行の記号、行だけに出る状態、行の末尾の印の 3 群を枠の内側いっぱいの罫線で分け、折り返しに任せず幅から
  // 列の数を決めた格子で組む。80 桁から枠と詰めの 4 桁を引いた 76 桁なら 5 列 (一番長い項目 13 桁 + 間 2 桁)。
  expect(legend?.props.flexWrap).toBeUndefined()
  expect((await ui.find({ key: 'legend:0' }))?.text).toBe('■ blocked      ○ pending      ▸ working      ↵ idle         ✓ completed')
  expect((await ui.find({ key: 'legend:1' }))?.text).toBe('◇ parked')
  expect((await ui.find({ key: 'legend:2' }))?.text).toBe('─'.repeat(76))
  expect((await ui.find({ key: 'legend:3' }))?.text).toBe('? question     ! permission   ⧗ limit        ✗ error        ⊘ exited')
  expect((await ui.find({ key: 'legend:4' }))?.text).toBe('· unknown')
  expect((await ui.find({ key: 'legend:5' }))?.text).toBe('─'.repeat(76))
  expect((await ui.find({ key: 'legend:6' }))?.text).toBe('↓n downstream  ⟳ deadlock')
  expect(await ui.find({ key: 'legend:7' })).toBeUndefined()
  // 送った結果の知らせは凡例より上 (中身の側) に出す。
  await ui.press({ key: 'row:task:W/1' })
  await ui.input({ key: 'instruction', text: '先にテストを足して' })
  const content = await ui.find({ key: 'content' })
  expect(content?.text).toMatch(/edge-cli へ送りました/)
  expect(legend?.text).not.toMatch(/送りました/)
})

test('選んだノードの詳細に参照とセッション、上流をたどった原因の鎖を出す', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:Q/2' })
  expect(await ui.find({ type: 'Text', text: /task:Q\/2 · design/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /blocked by: \? 論点を決める/ })).toBeDefined()
  await ui.press({ key: 'row:task:Q/1' })
  expect(await ui.find({ type: 'Text', text: /blocked by: none/ })).toBeDefined()
  // 完了を畳んでいる間に完了したノードを選んでいたら、その旨を出す。
  await ui.press({ key: 'fold' })
  await ui.press({ key: 'row:task:C/1' })
  await ui.press({ key: 'fold' })
  expect(await ui.find({ key: 'detail:task:C/1' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /選んだタスクは今の図に出ていません/ })).toBeDefined()
})

test('選んだタスクの詳細に、そのタスクが扱うメモ (description に id が書かれたもの) の title と status を出す (#213)', async ($, on) => {
  const memos = [
    { id: '20261025T010000-0000000a', title: '渡したメモ', status: 'handed' },
    { id: '20261025T020000-0000000b', title: 'まだ置いてあるメモ', status: 'open' },
  ]
  depsWorld(on, withNodes(node('task:M/1', 'working', 'メモを扱う', { session: 'memo', memos })))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:M/1' })
  const detail = await ui.find({ key: 'detail:task:M/1' })
  expect(detail?.text).toMatch(/◇ 渡したメモ \(handed\)/)
  expect(detail?.text).toMatch(/◇ まだ置いてあるメモ \(open\)/)
  // メモの無いタスクには出さない。
  await ui.press({ key: 'row:task:W/1' })
  expect((await ui.find({ key: 'detail:task:W/1' }))?.text).not.toMatch(/\((handed|open|discarded)\)/)
})

test('セッションのノードの詳細は key を出さず、名前と session ID の先頭 8 桁を出す (#196)', async ($, on) => {
  const session = { ...node('sid:S1234567890', 'working', 'design'), kind: 'session', task_id: undefined, session_id: 'S1234567890', session: 'design' }
  depsWorld(on, withNodes(session))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:sid:S1234567890' })
  expect(await ui.find({ type: 'Text', text: /^design · S1234567$/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /sid:S1234567890/ })).toBeUndefined()
})

test('止まっているノードは title も記号と同じ色で描く (#194)', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  // 行の Button の子は記号、右のレーン、title の 3 つの Text。
  const colors = async (key: string) => ((await ui.find({ key }))?.children ?? []).map(c => (c as { props?: { color?: string } }).props?.color)
  expect(await colors('row:task:Q/1')).toEqual(['warning', undefined, 'warning'])
  expect(await colors('row:task:W/1')).toEqual(['suggestion', undefined, undefined])
  // 待ちの輪にいるだけのノードは記号の色が無いので、title にも付けない。
  expect(await colors('row:task:X/1')).toEqual([undefined, undefined, undefined])
})

test('止まっているかの判定は JSON の halted と stuck に従い、mod は状態の語から決めない', async ($, on) => {
  // 状態の語だけなら止まっているノードに見えるが、バイナリが止まっていないと渡したもの。
  const calm = node('task:K/1', 'error', '止まっていない', { session: 'calm', downstream: 1 })
  // 進行中だが、バイナリが止まっていると渡したもの (待ちの輪に入った working)。
  const looped = node('task:K/2', 'working', '輪の中の作業', { session: 'calm', downstream: 1, deadlock: true, stuck: true })
  depsWorld(on, withNodes(calm, looped))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  const colors = async (key: string) => ((await ui.find({ key }))?.children ?? []).map(c => (c as { props?: { color?: string } }).props?.color)
  // halted でなければ title に色を付けない。stuck でなければ下流の数を出さない。
  expect(await colors('row:task:K/1')).toEqual(['error', undefined, undefined])
  expect(await ui.find({ key: 'row:task:K/1', text: /^✗ 止まっていない$/ })).toBeDefined()
  expect(await colors('row:task:K/2')).toEqual(['suggestion', undefined, undefined])
  expect(await ui.find({ key: 'row:task:K/2', text: /輪の中の作業 ↓1 ⟳$/ })).toBeDefined()
  // ■ は Q/1、X の輪、K/2 の 3 つ。
  expect(await ui.find({ type: 'Text', text: /^■ 3 / })).toBeDefined()
})

test('edge の辺は Unlink で edge rm に元の参照を渡す', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:R/1' })
  w.ran.length = 0
  await ui.press({ key: 'unlink:task:Q/2' })
  expect(w.ran[0]).toEqual([BIN, 'edge', 'rm', '--before', 'session:design', '--before-cwd', CWD, '--after', 'task:R/1', '--after-cwd', CWD])
})

test('同じセッションの blockedBy は外せないので、そのセッションへ解消を頼む', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:Q/2' })
  expect(await ui.find({ key: 'unlink:task:Q/1' })).toBeUndefined()
  await ui.press({ key: 'ask:task:Q/1' })
  expect(w.sent).toHaveLength(1)
  expect(w.sent[0]?.to).toBe('Q')
  expect(w.sent[0]?.text).toMatch(/タスク #2「段5」が #1「論点を決める」の完了待ち \(blockedBy\)/)
  expect(w.sent[0]?.text).toMatch(/#1 を completed か deleted にするか、#2 を blockedBy なしで作り直して/)
})

test('Add blocker は循環にならない候補だけを出し、edge add にノードの key を渡す', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:R/1' })
  await ui.press({ key: 'add-blocker' })
  // 自分、既にある上流、下流 (循環になる)、完了、終了したものは出さない。
  for (const key of ['task:R/1', 'task:Q/2', 'task:V/1', 'task:C/1', 'task:G/1']) {
    expect(await ui.find({ key: `blocker:${key}` })).toBeUndefined()
  }
  expect(await ui.find({ key: 'blocker:task:Q/1' })).toBeDefined()
  expect(await ui.find({ key: 'blocker:issue:o/r#9' })).toBeDefined()
  w.ran.length = 0
  await ui.press({ key: 'blocker:task:W/1' })
  expect(w.ran[0]).toEqual([BIN, 'edge', 'add', '--before', 'task:W/1', '--before-cwd', CWD, '--after', 'task:R/1', '--after-cwd', CWD])
})

test('タスクの無いセッションのノードも Add blocker の候補になり、sid: の key を端点に渡す', async ($, on) => {
  const session = { ...node('sid:U', 'working', 'dotfiles'), kind: 'session', task_id: undefined, session: 'u' }
  const w = depsWorld(on, withNodes(session))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:R/1' })
  await ui.press({ key: 'add-blocker' })
  w.ran.length = 0
  await ui.press({ key: 'blocker:sid:U' })
  expect(w.ran[0]).toEqual([BIN, 'edge', 'add', '--before', 'sid:U', '--before-cwd', CWD, '--after', 'task:R/1', '--after-cwd', CWD])
})

test('生きているほかのセッションのノードへ指示を送る。自分と終了したセッションには送らない', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)

  await ui.press({ key: 'row:task:SELF/1' })
  expect(await ui.find({ key: 'instruction' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /このセッションのタスクです/ })).toBeDefined()
  await ui.press({ key: 'row:task:G/1' })
  expect(await ui.find({ key: 'instruction' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /このセッションは終了しています/ })).toBeDefined()

  await ui.press({ key: 'row:task:W/1' })
  await ui.input({ key: 'instruction', text: '先にテストを足して' })
  expect(w.sent).toEqual([{ to: 'W', text: 'タスク「edge の CLI」について: 先にテストを足して' }])
  expect(await ui.find({ type: 'Text', text: /edge-cli へ送りました/ })).toBeDefined()
})

test('終了したセッションの未完了のタスクは To parking でメモへ移せる', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:W/1' })
  expect(await ui.find({ key: 'adopt' })).toBeUndefined()
  await ui.press({ key: 'row:task:G/1' })
  w.ran.length = 0
  await ui.press({ key: 'adopt' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'adopt', 'task:G/1', ...SELF_ARGS]])
})

test('daemon が status.json を書いていない依存図では、exited と描かれても To parking を出さない', async ($, on) => {
  const { updated_at: _, ...stale } = DEPS
  depsWorld(on, { ...stale, daemon_running: false })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:G/1' })
  expect(await ui.find({ key: 'adopt' })).toBeUndefined()
})

test('自分のセッションのタスクは、着手・完了・削除・パーキング・新しいセッションへ渡すことを自分の入力へ頼む (#296)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  // よく使う着手と完了だけを出し、残りは More… に畳む (#308)。
  expect((await ui.find({ key: 'actions:task:SELF/1' }))?.text).toMatch(/^Start\s*Complete\s*More…$/)
  await ui.press({ key: 'own-more' })
  expect((await ui.find({ key: 'actions:task:SELF/1' }))?.text).toMatch(/^Start\s*Complete\s*Hand off\s*Delete\s*To parking\s*\+ Add blocker\s*Less$/)
  // 渡す先は Select 1 つに畳む。repo を問わず生きているほかのセッションと、新しいセッション。自分は除く。
  expect(await options(ui, 'pick:own-hand')).toEqual([
    { value: 'W', label: 'edge-cli (edge の CLI)' },
    { value: 'P', label: 'peer (後続)' },
    { value: 'new', label: 'new session' },
  ])

  const said = /このセッションの入力へ渡しました/
  const cases: [() => Promise<unknown>, RegExp][] = [
    [() => ui.press({ key: 'own-start' }), /^タスク #1「このセッションの作業」に着手してください。TaskUpdate で in_progress にしてから進めてください。$/],
    [() => ui.press({ key: 'own-complete' }), /^タスク #1「このセッションの作業」を TaskUpdate で completed にしてください。$/],
    [() => ui.press({ key: 'own-delete' }), /^タスク #1「このセッションの作業」を TaskUpdate で deleted にしてください。$/],
    // 扱うメモの無いタスクは、park tool でメモに置いてから消すよう頼む。
    [() => ui.press({ key: 'own-park' }), /^タスク #1「このセッションの作業」をパーキングロットへ戻してください。park tool で.*TaskUpdate で deleted にしてください。$/],
    // 起こしたセッションの名前は自分が付けるので、SendMessage で渡すのも自分に任せる。
    [() => pickAndPress(ui, 'own-hand', 'new'), /^タスク #1「このセッションの作業」を新しいセッションへ渡してください。agents-daemon:peer-sessions skill の手順で.*TaskUpdate で deleted にしてください。$/],
  ]
  for (const [run, text] of cases) {
    w.submitted.length = 0
    w.ran.length = 0
    await run()
    expect(w.submitted).toHaveLength(1)
    expect(w.submitted[0]).toMatch(text)
    expect(w.sent).toEqual([])
    expect(operations(w.ran)).toEqual([])
    expect(await ui.find({ type: 'Text', text: said })).toBeDefined()
  }

  await ui.press({ key: 'own-less' })
  expect(await ui.find({ key: 'own-delete' })).toBeUndefined()
  expect(await ui.find({ key: 'pick:own-hand' })).toBeUndefined()
})

test('自分のタスクの More… と宛先の選択は、ノードを選び直すと畳み直す (#308)', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  await ui.press({ key: 'own-more' })
  await ui.select({ key: 'pick:own-hand', value: 'P' })
  expect((await ui.find({ key: 'pick:own-hand' }))?.props.value).toBe('P')
  await ui.press({ key: 'row:task:W/1' })
  await ui.press({ key: 'row:task:SELF/1' })
  expect(await ui.find({ key: 'own-delete' })).toBeUndefined()
  await ui.press({ key: 'own-more' })
  // 何も選んでいなければ先頭の宛先を出す (Select は値が無いと none と描く)。
  expect((await ui.find({ key: 'pick:own-hand' }))?.props.value).toBe('W')
})

test('選んでいた宛先が依存図から消えたら、先頭の宛先へ渡す (#308)', async ($, on) => {
  let data: unknown = DEPS
  const w = world(on, argv => (argv[1] === 'deps' ? output(JSON.stringify(data)) : output('{}')))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  await ui.press({ key: 'own-more' })
  await ui.select({ key: 'pick:own-hand', value: 'P' })
  // P のセッションが終わった依存図を読み直す。
  data = { ...DEPS, repos: [DEPS.repos[0], { ...DEPS.repos[1], sessions: [] }] }
  await ui.press({ key: 'own-complete' })
  expect((await ui.find({ key: 'pick:own-hand' }))?.props.value).toBe('W')
  await ui.press({ key: 'own-hand' })
  expect(w.sent[0]?.to).toBe('W')
})

test('Select の無い画面 (mobile) では、宛先ごとのボタンで渡す (#308)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($, 'mobile')
  await ui.press({ key: 'row:task:SELF/1' })
  await ui.press({ key: 'own-more' })
  expect(await ui.find({ key: 'pick:own-hand' })).toBeUndefined()
  expect(await ui.find({ key: 'note' })).toBeUndefined()
  expect((await ui.find({ key: 'own-hand:W' }))?.text).toBe('Hand off → edge-cli (edge の CLI)')
  expect((await ui.find({ key: 'own-hand:new' }))?.text).toBe('Hand off → new session')
  await ui.press({ key: 'own-hand:P' })
  expect(w.sent[0]?.to).toBe('P')

  await ui.press({ key: `park:${MEMO.id}` })
  expect((await ui.find({ key: 'task:W' }))?.text).toBe('To task → edge-cli (edge の CLI)')
  expect((await ui.find({ key: 'issue:P' }))?.text).toBe('Ask for issue → peer (後続)')
  // repo の絶対パスを書く欄が無いので、other repo… は出さない。
  expect(await ui.find({ key: 'task:path' })).toBeUndefined()
})

test('補足を書いてから押すと、着手・渡す・新しいセッションへ渡す文面の末尾に足し、押したら欄を空にする (#310)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  const NOTE = '利用者からの補足: 先にテストを足して'

  await ui.input({ key: 'note', text: '先にテストを足して', kind: 'change' })
  await ui.press({ key: 'own-start' })
  expect(w.submitted[0]).toMatch(new RegExp(`進めてください。\\n${NOTE}$`))
  expect((await ui.find({ key: 'note' }))?.props.value).toBe('')

  // 補足が空なら今と同じ文面。
  w.submitted.length = 0
  await ui.press({ key: 'own-start' })
  expect(w.submitted[0]).toMatch(/進めてください。$/)

  // 渡すときは、相手へ送る文面に足す。自分へ消すよう頼む文面には足さない。
  await ui.press({ key: 'own-more' })
  w.submitted.length = 0
  await ui.input({ key: 'note', text: '先にテストを足して', kind: 'change' })
  await pickAndPress(ui, 'own-hand', 'W')
  expect(w.sent[0]?.text).toMatch(new RegExp(`\\n詳細: 作業の詳細\\n${NOTE}$`))
  expect(w.submitted[0]).not.toMatch(/補足/)

  w.submitted.length = 0
  await ui.input({ key: 'note', text: '先にテストを足して', kind: 'change' })
  await pickAndPress(ui, 'own-hand', 'new')
  expect(w.submitted[0]).toMatch(new RegExp(`deleted にしてください。\\n${NOTE}$`))
})

test('補足は完了・削除・パーキングへ戻す文面には足さず、ノードを選び直すと消す (#310)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  await ui.press({ key: 'own-more' })
  for (const key of ['own-complete', 'own-delete', 'own-park']) {
    w.submitted.length = 0
    await ui.input({ key: 'note', text: '急ぎ', kind: 'change' })
    await ui.press({ key })
    expect(w.submitted[0]).not.toMatch(/急ぎ/)
  }

  await ui.press({ key: 'row:task:W/1' })
  await ui.press({ key: 'row:task:SELF/1' })
  expect((await ui.find({ key: 'note' }))?.props.value).toBe('')
})

// ListAgents の名前は deps の JSON のどの欄とも一致しないことがあるので、渡すのは model ではなく mod が session ID で送る。
test('自分のタスクを生きているほかのセッションへ渡す: mod が title と description を送り、届いたら自分にタスクを消すよう頼む (#296)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  await ui.press({ key: 'own-more' })
  await pickAndPress(ui, 'own-hand', 'W')
  expect(w.sent).toHaveLength(1)
  expect(w.sent[0]?.to).toBe('W')
  expect(w.sent[0]?.text).toBe('self (session ID SELF) からタスク「このセッションの作業」を渡します。TaskCreate で作って進めてください。\n詳細: 作業の詳細')
  expect(w.submitted).toEqual(['タスク #1「このセッションの作業」を edge-cli へ渡しました (title と description を送った)。TaskUpdate で deleted にしてください。'])
  expect(await ui.find({ type: 'Text', text: /edge-cli へ渡し、このセッションの入力へ渡しました/ })).toBeDefined()
})

test('ほかのセッションへ届かなかったら、自分にタスクを消すよう頼まない (#296)', async ($, on) => {
  const w = depsWorld(on, DEPS, undefined, undefined, 'セッションが終了した')
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/1' })
  await ui.press({ key: 'own-more' })
  await pickAndPress(ui, 'own-hand', 'W')
  expect(w.submitted).toEqual([])
  expect(await ui.find({ type: 'Text', text: /edge-cli へ送れませんでした: セッションが終了した$/ })).toBeDefined()
})

test('ほかのセッションのタスクと、自分の完了したタスクには自分へ頼む操作を出さない (#296)', async ($, on) => {
  depsWorld(on, withNodes(node('task:SELF/3', 'completed', '済んだ自分の作業', { session: 'self' })))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  for (const key of ['task:W/1', 'task:G/1', 'task:SELF/3']) {
    await ui.press({ key: `row:${key}` })
    for (const action of ['own-start', 'own-complete', 'own-more', 'own-hand', 'pick:own-hand', 'note']) {
      expect(await ui.find({ key: action })).toBeUndefined()
    }
  }
})

// OWN_MEMO は自分のセッションへ渡されてタスクになったメモ (handed)。
const OWN_MEMO = { id: '20261020T090000-cccc', title: 'タイトルを課題形にしない', status: 'handed' }
const OWN_TASK = node('task:SELF/2', 'pending', 'メモから作ったタスク', { session: 'self', memos: [OWN_MEMO] })
const REOPEN = [BIN, 'park', 'set', OWN_MEMO.id, '--status', 'open', '--from', 'handed', ...SELF_ARGS]
// 取り直すときは、元の宛先 (このセッション) を書き直す。
const RECLAIM = [BIN, 'park', 'set', OWN_MEMO.id, '--status', 'handed', '--from', 'open', '--to-session', 'SELF', ...SELF_ARGS]

test('メモから作った自分のタスクを To parking で戻すと、新しいメモを作らず元のメモを open に戻してからタスクを消すよう頼む (#296)', async ($, on) => {
  const w = depsWorld(on, withNodes(OWN_TASK))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/2' })
  await ui.press({ key: 'own-more' })
  w.ran.length = 0
  await ui.press({ key: 'own-park' })
  expect(operations(w.ran)).toEqual([REOPEN])
  expect(w.submitted).toEqual([`タスク #2「メモから作ったタスク」の元のメモ (${OWN_MEMO.id}) をパーキングロットの open に戻しました。TaskUpdate で deleted にしてください。`])
  expect(await ui.find({ type: 'Text', text: /メモ 20261020T090000-cccc を open に戻し、このセッションの入力へ渡しました/ })).toBeDefined()
})

test('扱うメモが既に open なら、park tool で 2 つ目のメモを作らずタスクを消すよう頼むだけにする (#296)', async ($, on) => {
  const open = { ...OWN_TASK, memos: [{ ...OWN_MEMO, status: 'open' }] }
  const w = depsWorld(on, withNodes(open))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/2' })
  await ui.press({ key: 'own-more' })
  w.ran.length = 0
  await ui.press({ key: 'own-park' })
  expect(operations(w.ran)).toEqual([])
  expect(w.submitted).toEqual([`タスク #2「メモから作ったタスク」の元のメモ (${OWN_MEMO.id}) をパーキングロットの open に戻しました。TaskUpdate で deleted にしてください。`])
})

test('自分の入力へ渡せなかった (drop) ときは、open に戻したメモを handed に取り直す (#296)', async ($, on) => {
  const w = depsWorld(on, withNodes(OWN_TASK), undefined, 'Stop が立っている')
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: 'row:task:SELF/2' })
  await ui.press({ key: 'own-more' })
  w.ran.length = 0
  await ui.press({ key: 'own-park' })
  expect(operations(w.ran)).toEqual([REOPEN, RECLAIM])
  expect(await ui.find({ type: 'Text', text: /このセッションの入力へ渡せませんでした: Stop が立っている。メモは handed に戻しました/ })).toBeDefined()

  w.ran.length = 0
  await ui.press({ key: 'own-start' })
  expect(operations(w.ran)).toEqual([])
  expect(await ui.find({ type: 'Text', text: /このセッションの入力へ渡せませんでした: Stop が立っている$/ })).toBeDefined()
})

test('メモを選ぶと詳細を出し、Discard / To task / Ask for issue で渡す', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  // 最後に置かれた時刻 (JST) を先頭に、置いたときに進めていたタスクを末尾に添える。
  // 種類は park tool の kind の語のまま出す。
  expect(await ui.find({ key: `park:${MEMO.id}`, text: /^10\/18 19:30 \[aside\] 保持期間の根拠が無い ×2 ← edge の CLI/ })).toBeDefined()
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ type: 'Text', text: /定数にコメントが無い/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /edge-cli 10\/18 19:30 別の定数も同じ/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /gone 10\/19 01:28/ })).toBeDefined()

  // 宛先は自セッション (#190) と、同じ repo の生きているほかのセッション。issue はすべての repo のセッション。
  // 続けて新しいセッションを起こす repo (#162) を並べる。宛先のセッション名には subject を添える (#191)。
  const spawns = [
    { value: CWD, label: 'new session: agents-daemon' },
    { value: '/src/home/dotfile', label: 'new session: dotfile' },
    { value: 'path', label: 'new session: other repo…' },
  ]
  expect(await options(ui, 'pick:task')).toEqual([{ value: 'SELF', label: 'このセッション' }, { value: 'W', label: 'edge-cli (edge の CLI)' }, ...spawns])
  expect(await options(ui, 'pick:issue')).toEqual([
    { value: 'SELF', label: 'このセッション' },
    { value: 'W', label: 'edge-cli (edge の CLI)' },
    { value: 'P', label: 'peer (後続)' },
    ...spawns,
  ])

  w.ran.length = 0
  await pickAndPress(ui, 'task', 'W')
  expect(w.sent[0]?.to).toBe('W')
  expect(w.sent[0]?.text).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」\(種類: aside\) をタスクにしてください。/)
  // 着手の時期は指示せず、受け手の計画に任せる。依存図はタスクの description に書かれた id でメモと照合する (#213)。
  expect(w.sent[0]?.text).not.toMatch(/今の作業の区切り/)
  expect(w.sent[0]?.text).toMatch(/着手の時期は今の計画 \(タスクの依存\) に従ってください。/)
  expect(w.sent[0]?.text).toMatch(/タスクの description にメモの id \(20261018T090000-aaaa\) を入れてください。/)
  expect(w.sent[0]?.text).toMatch(/既にこのメモを扱うタスクがあれば、新しく作らずにその description に id を書き足すだけで構いません。/)
  expect(w.sent[0]?.text).toMatch(/\(メモ 20261018T090000-aaaa\)$/)
  // 送る前に取る (open のときだけ handed にし、宛先のセッションを同じ park set で書く)。届いたら handed のまま。
  expect(operations(w.ran)).toEqual([CLAIM_W])

  // 選んだメモは操作の後も開いたまま (同じ行をもう一度押すと閉じる)。捨てるのも open のときだけ。
  w.ran.length = 0
  await ui.press({ key: 'discard' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'set', MEMO.id, '--status', 'discarded', '--from', 'open', ...SELF_ARGS]])
})

test('取れなかったメモ (ほかのセッションが先に渡した) は送らない', async ($, on) => {
  const w = world(on, argv =>
    argv[1] === 'deps' ? output(JSON.stringify(DEPS)) : argv[2] === 'set' ? output('', 1, `agents-daemon: メモ ${MEMO.id} の status は handed (期待 open) なので書き換えない`) : output('{}'),
  )
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  w.ran.length = 0
  await pickAndPress(ui, 'task', 'W')
  expect(operations(w.ran)).toEqual([CLAIM_W])
  expect(w.sent).toEqual([])
  expect(await ui.find({ type: 'Text', text: /メモを取れませんでした.*status は handed/ })).toBeDefined()
})

test('自セッションへは peer メッセージではなく自分の入力へ渡し、入ったらメモを handed にする (#190)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })

  w.ran.length = 0
  // 何も選ばなければ先頭の宛先 (このセッション) へ渡す。
  expect((await ui.find({ key: 'pick:issue' }))?.props.value).toBe('SELF')
  await ui.press({ key: 'issue' })
  expect(w.sent).toEqual([])
  expect(w.submitted).toHaveLength(1)
  expect(w.submitted[0]).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」を GitHub の issue にしてください。repo: agents-daemon。/)
  expect(operations(w.ran)).toEqual([CLAIM_SELF])
  expect(await ui.find({ type: 'Text', text: /このセッション へ送り、メモを渡しました/ })).toBeDefined()
})

test('自分の入力へ渡せなかった (drop) メモは open に戻す', async ($, on) => {
  const w = depsWorld(on, DEPS, undefined, 'Stop が立っている')
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })

  w.ran.length = 0
  await pickAndPress(ui, 'task', 'SELF')
  expect(operations(w.ran)).toEqual([CLAIM_SELF, REVERT])
  expect(await ui.find({ type: 'Text', text: /このセッション へ送れませんでした: Stop が立っている。メモは open に戻しました/ })).toBeDefined()
})

test('ほかの repo のメモは自セッションでタスクにできないが、issue は頼める', async ($, on) => {
  const elsewhere = { ...MEMO, id: '20261018T100000-bbbb', title: 'dotfile の件' }
  const repos = [DEPS.repos[0], { ...DEPS.repos[1], memos: [elsewhere] }]
  depsWorld(on, { ...DEPS, repos })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${elsewhere.id}` })
  const values = async (key: string) => ((await options(ui, key)) as { value: string }[]).map(o => o.value)
  expect(await values('pick:task')).toEqual(['P', '/src/home/dotfile', CWD, 'path'])
  expect((await values('pick:issue')).slice(0, 3)).toEqual(['SELF', 'P', 'W'])
})

test('終了したセッションから移したメモ (origin adopted) は種類に adopted を添えて出し、渡す文面にも添える', async ($, on) => {
  const adopted = { ...MEMO, id: '20261018T110000-cccc', kind: 'scope', origin: 'adopted', title: '残ったタスク', sources: [MEMO.sources[1]] }
  const repos = [{ ...DEPS.repos[0], memos: [adopted] }, DEPS.repos[1]]
  const w = depsWorld(on, { ...DEPS, repos })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ key: `park:${adopted.id}`, text: /\[scope・adopted\] 残ったタスク/ })).toBeDefined()
  await ui.press({ key: `park:${adopted.id}` })
  await pickAndPress(ui, 'task', 'W')
  expect(w.sent[0]?.text).toMatch(/^パーキングロットのメモ「残ったタスク」\(種類: scope・adopted\) をタスクにしてください。/)
})

test('最後に置かれてから stale_memo_days より経ったメモがあれば、パーキングの見出しからまとめて捨てる', async ($, on) => {
  // MEMO は 10/18 に置かれた。時計を 12/01 にすると 44 日経っている。日数は deps の stale_memo_days (バイナリが渡す)。
  const data = { ...DEPS, stale_memo_days: 40 }
  const w = world(
    on,
    argv => (argv[1] === 'deps' ? output(JSON.stringify(data)) : argv[2] === 'discard' ? output(JSON.stringify([{ id: MEMO.id }])) : output('{}')),
    Date.parse('2026-12-01T00:00:00Z'),
  )
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ key: 'discard-stale', text: /Discard 1 older than 40d/ })).toBeDefined()
  w.ran.length = 0
  await ui.press({ key: 'discard-stale' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'discard', '--days', '40', ...SELF_ARGS]])
  // 戻し方を添える (id は出来事のログに残る)。
  expect(await ui.find({ type: 'Text', text: /40 日より前のメモ 1 つを捨てました.*park set <id> --status open/ })).toBeDefined()
})

test('stale_memo_days より新しいメモだけなら、日が経っていてもまとめて捨てるボタンを出さない', async ($, on) => {
  world(on, argv => (argv[1] === 'deps' ? output(JSON.stringify({ ...DEPS, stale_memo_days: 50 })) : output('{}')), Date.parse('2026-12-01T00:00:00Z'))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ key: 'discard-stale' })).toBeUndefined()
})

test('30 日以内のメモだけなら、まとめて捨てるボタンを出さない', async ($, on) => {
  depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ key: 'discard-stale' })).toBeUndefined()
})

test('メモを別の repo の新しいセッションへ渡す: 候補はメモの repo、ほかの行の repo の順 (#162)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })

  w.ran.length = 0
  await pickAndPress(ui, 'task', '/src/home/dotfile')
  expect(operations(w.ran)).toEqual([CLAIM_DOTFILE, [BIN, 'spawn', '--repo', '/src/home/dotfile']])
  expect(w.fed).toHaveLength(1)
  expect(w.fed[0]).toMatch(/^このメモを渡すため、dotfile \(\/src\/home\/dotfile\) で新しいセッションとして起こしました。/)
  expect(w.fed[0]).toMatch(/パーキングロットのメモ「保持期間の根拠が無い」\(種類: aside\) をタスクにしてください。/)
  expect(w.fed[0]).not.toMatch(/今の作業の区切り/)
  expect(w.fed[0]).toMatch(/着手の時期は今の計画 \(タスクの依存\) に従ってください。/)
  expect(w.fed[0]).toMatch(/タスクの description にメモの id \(20261018T090000-aaaa\) を入れてください。/)
  expect(w.sent).toEqual([])
  expect(await ui.find({ type: 'Text', text: /dotfile で新しいセッションを起こし、メモを渡しました/ })).toBeDefined()
})

test('issue を頼む新しいセッションには、起こした repo を起票先にした文面を渡す', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await pickAndPress(ui, 'issue', '/src/home/dotfile')
  expect(w.fed[0]).toMatch(/パーキングロットのメモ「保持期間の根拠が無い」を GitHub の issue にしてください。repo: dotfile。/)
})

test('候補に無い repo は other repo… を選んで絶対パスで指定する。相対パスは起こさずに弾く', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ key: 'spawn-path:task' })).toBeUndefined()
  await ui.select({ key: 'pick:task', value: 'path' })
  // パスの欄が操作のボタンの代わりになる。
  expect(await ui.find({ key: 'task' })).toBeUndefined()

  w.ran.length = 0
  await ui.input({ key: 'spawn-path:task', text: 'src/home/other' })
  expect(operations(w.ran)).toEqual([])
  expect(await ui.find({ type: 'Text', text: /絶対パス/ })).toBeDefined()

  await ui.input({ key: 'spawn-path:task', text: '/src/home/other' })
  expect(operations(w.ran)[0]).toEqual(claim('--to-repo', '/src/home/other'))
  expect(operations(w.ran)[1]).toEqual([BIN, 'spawn', '--repo', '/src/home/other'])
  expect(w.fed[0]).toMatch(/^このメモを渡すため、other \(\/src\/home\/other\) で新しいセッションとして起こしました。/)

  // ほかの宛先を選び直すと、パスの欄を畳んでボタンに戻す。
  await ui.select({ key: 'pick:task', value: 'W' })
  expect(await ui.find({ key: 'spawn-path:task' })).toBeUndefined()
  expect(await ui.find({ key: 'task' })).toBeDefined()
})

test('メモの補足は渡す文面に足し、捨てるときは使わない (#310)', async ($, on) => {
  const w = depsWorld(on)
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await ui.input({ key: 'note', text: '今週中に', kind: 'change' })
  await pickAndPress(ui, 'task', 'W')
  expect(w.sent[0]?.text).toMatch(/\(メモ 20261018T090000-aaaa\)\n利用者からの補足: 今週中に$/)
  expect((await ui.find({ key: 'note' }))?.props.value).toBe('')

  await ui.input({ key: 'note', text: '今週中に', kind: 'change' })
  await pickAndPress(ui, 'issue', '/src/home/dotfile')
  expect(w.fed[0]).toMatch(/\n利用者からの補足: 今週中に$/)
})

test('新しいセッションを起こせなかった (window を作っていない) メモは open に戻す', async ($, on) => {
  const w = world(on, argv =>
    argv[1] === 'deps' ? output(JSON.stringify(DEPS)) : argv[1] === 'spawn' ? output('', 1, 'agents-daemon: repo が見つからない: /src/home/dotfile') : output('{}'),
  )
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  w.ran.length = 0
  await pickAndPress(ui, 'task', '/src/home/dotfile')
  expect(operations(w.ran)).toEqual([CLAIM_DOTFILE, [BIN, 'spawn', '--repo', '/src/home/dotfile'], REVERT])
  expect(await ui.find({ type: 'Text', text: /dotfile で新しいセッションを起こせませんでした: .*repo が見つからない.*メモは open に戻しました/ })).toBeDefined()
})

test('claude が前景にならなかった (pane は残る) メモは戻さず、pane と戻し方を知らせる', async ($, on) => {
  const w = world(on, argv =>
    argv[1] === 'deps'
      ? output(JSON.stringify(DEPS))
      : argv[1] === 'spawn'
        ? output('{"session_id":"NEW","pane_id":"block:b1","foreground":false,"detail":"15s の間に pane block:b1 で claude が前景にならなかった"}')
        : output('{}'),
  )
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  w.ran.length = 0
  await pickAndPress(ui, 'task', '/src/home/dotfile')
  expect(operations(w.ran)).toEqual([CLAIM_DOTFILE, [BIN, 'spawn', '--repo', '/src/home/dotfile']])
  expect(await ui.find({ type: 'Text', text: /dotfile で window を作りましたが.*前景にならなかった.*handed のまま.*park set .* --status open/ })).toBeDefined()
})

test('ノードの無い依存図と、daemon が動いていないことを描く', async ($, on) => {
  depsWorld(on, { daemon_running: false, here: CWD, stale_memo_days: 30, repos: [], graph:{ nodes: [], links: [], longest_chain: [], rows: [], rows_with_completed: [] } })
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /agents-daemon が動いていないため、セッションの状態は出ません/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /タスクはまだありません/ })).toBeDefined()
})

test('状態を分類できないノードは · で出し、待っているものに数えて working には数えない', async ($, on) => {
  const session = (key: string, state: string, title: string) => ({ ...node(key, state, title), kind: 'session', task_id: undefined })
  depsWorld(on, withNodes(session('sid:U', 'unknown', 'dotfiles')))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /▸ 2 {2}↵ 2 {2}✓ 1/ })).toBeDefined()
  expect(await ui.find({ key: 'row:sid:U', text: /^· dotfiles$/ })).toBeDefined()
})

test('deps が失敗したら理由を描く', async ($, on) => {
  world(on, () => output('', 1, 'status snapshot のパースに失敗'))
  await $.command.run(OPEN_DEPS)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /status snapshot のパースに失敗/ })).toBeDefined()
})
