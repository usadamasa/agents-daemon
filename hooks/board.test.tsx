import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'

// world は plugin の下にいる engine の代わりを立てる。process.run は run が答え、呼ばれた argv (バイナリの後) を
// ran に残す。ボードの経路のログ (hooks/log.ts) は log.test.tsx が見るので残さない。now は mod の時計の始点
// (古いメモの判定に使う)。submitted は自セッションの入力へ渡した文面 (prompt.submit)。drop を渡すと入らなかった
// ことにする。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult = () => output('{}'), now = Date.parse('2026-10-19T00:00:00Z'), drop = '') {
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
    if (e.argv[1] !== 'board-log') ran.push([...e.argv])
    if (e.argv[1] === 'spawn') fed.push(e.init?.stdin ?? '')
    return { value: run(e.argv) }
  })
  on('session.send', async (_$, e) => {
    sent.push({ to: e.to, text: e.text })
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

// operations は ran のうち、操作の後の読み直し (status --board) を除いたもの。
function operations(ran: string[][]): string[][] {
  return ran.filter(argv => argv[1] !== 'status')
}

// boardWorld は status --board に data を返し、spawn は起こしたセッションを、それ以外 (park、edge) は成功を返す。
function boardWorld(on: On, data: unknown = BOARD, now?: number, drop?: string) {
  return world(on, argv => (argv[1] === 'status' ? output(JSON.stringify(data)) : argv[1] === 'spawn' ? output(SPAWNED) : output('{"created":true}')), now, drop)
}

function node(key: string, state: string, title: string, extra: Record<string, unknown> = {}) {
  const [sid, tid] = key.split('/')
  return {
    key,
    kind: 'task',
    state,
    title,
    ref: `task:${sid}/${tid}`,
    cwd: CWD,
    session_id: sid,
    session: sid.toLowerCase(),
    live: state !== 'exited',
    depth: 0,
    downstream: 0,
    parked: 0,
    blocked_by: [] as string[],
    deadlock: false,
    ...extra,
  }
}

const ep = (ref: string) => ({ ref, cwd: CWD, repo: CWD })

// 依存の DAG (internal/board の graph)。Q/1 が質問で止まり、その下流に 3 つ。X/1 と X/2 は待ちの輪。
// W/1 は進行中で、途中で置いたメモが 1 つ。D/1 はターンを終えた。issue:o/r#9 は置き場のノードで、その下流に P/1。
const NODES = [
  node('Q/1', 'question', '論点を決める', { session: 'design', downstream: 3 }),
  node('X/1', 'pending', 'x を待つ', { session: 'loop', downstream: 1, blocked_by: ['X/2'], deadlock: true }),
  node('X/2', 'pending', 'y を待つ', { session: 'loop', downstream: 1, blocked_by: ['X/1'], deadlock: true }),
  node('Q/2', 'pending', '段5', { session: 'design', downstream: 2, blocked_by: ['Q/1'] }),
  node('W/1', 'working', 'edge の CLI', { session: 'edge-cli', downstream: 1, parked: 1 }),
  {
    ...node('I/0', 'pending', 'issue:o/r#9', { downstream: 1 }),
    key: 'ref:issue:o/r#9',
    kind: 'ref',
    ref: 'issue:o/r#9',
    session_id: undefined,
    session: undefined,
    live: false,
  },
  node('R/1', 'pending', 'リリース', { session: 'release', downstream: 1, blocked_by: ['Q/2', 'Q/1'] }),
  node('D/1', 'done', '要約を書いた', { session: 'doner' }),
  node('W/2', 'pending', 'skill から辺を書く', { session: 'edge-cli', blocked_by: ['W/1'] }),
  node('P/1', 'pending', '後続', { session: 'peer', blocked_by: ['ref:issue:o/r#9'] }),
  node('V/1', 'pending', '確認', { session: 'verify', blocked_by: ['R/1', 'Q/2', 'Q/1'] }),
  node('G/1', 'exited', '残った作業', { session: 'gone' }),
  node('C/1', 'completed', '済んだ'),
  node('SELF/1', 'working', 'このセッションの作業', { session: 'self' }),
]

const LINKS = [
  { before: 'Q/1', after: 'Q/2', kind: 'task' },
  { before: 'Q/2', after: 'R/1', kind: 'edge', edge: { id: 'e1', before: ep('session:design'), after: ep('task:R/1') } },
  { before: 'R/1', after: 'V/1', kind: 'edge', edge: { id: 'e2', before: ep('task:R/1'), after: ep('task:V/1') } },
  { before: 'W/1', after: 'W/2', kind: 'task' },
  { before: 'X/1', after: 'X/2', kind: 'task' },
  { before: 'X/2', after: 'X/1', kind: 'task' },
  { before: 'ref:issue:o/r#9', after: 'P/1', kind: 'edge', edge: { id: 'e3', before: ep('issue:o/r#9'), after: ep('task:P/1') } },
]

// レーンの行 (internal/dag の Rows を Nodes の順で組んだもの)。鎖は Q → R → V と W、ref → P。輪の X は最後。
const ROWS = ['Q/1', 'Q/2', 'R/1', 'V/1', 'W/1', 'W/2', 'ref:issue:o/r#9', 'P/1', 'D/1', 'G/1', 'SELF/1', 'X/1', 'X/2'].map(node => ({ lanes: '*', node }))
const ROWS_WITH_COMPLETED = [...ROWS.slice(0, 10), { lanes: '*', node: 'C/1' }, ...ROWS.slice(10)]

// メモは W/1 の途中と、終了した G が置いた。
const MEMO = {
  id: '20261018T090000-aaaa',
  kind: 'aside',
  title: '保持期間の根拠が無い',
  detail: '定数にコメントが無い',
  files: ['internal/parking/parking.go'],
  default_taken: 'やらない',
  sources: [
    { session_id: 'W', task_id: '1', session: 'edge-cli', live: true, detail: '別の定数も同じ', at: '2026-10-18T19:30:00+09:00' },
    { session_id: 'G', session: 'gone', live: false, at: '2026-10-18T16:28:44Z' },
  ],
}

function card(key: string, column: string, subject: string, session_id: string, extra: Record<string, unknown> = {}) {
  return { key, kind: 'task', column, subject, session_id, session: session_id.toLowerCase(), live: true, updated_at: '2026-10-18T09:00:00Z', ...extra }
}

// rows はメモの送り先 (同じ repo の生きているセッション) を決めるのに使う。
const BOARD = {
  domain: 'home',
  daemon_running: true,
  updated_at: '2026-10-18T09:00:00Z',
  columns: [],
  here: CWD,
  rows: [
    {
      repo: CWD,
      title: 'agents-daemon',
      cards: [
        card('W/1', 'in_progress', 'edge の CLI', 'W', { session: 'edge-cli', task_id: '1' }),
        card('SELF/1', 'in_progress', 'このセッションの作業', 'SELF', { task_id: '1' }),
        card('G/1', 'pending', '残った作業', 'G', { session: 'gone', live: false, task_id: '1' }),
      ],
      parking: [MEMO],
    },
    { repo: '/src/home/dotfile', title: 'dotfile', cards: [card('P/1', 'pending', '後続', 'P', { session: 'peer', task_id: '1' })], parking: [] },
  ],
  graph: { nodes: NODES, links: LINKS, longest_chain: ['Q/1', 'Q/2', 'R/1', 'V/1'], rows: ROWS, rows_with_completed: ROWS_WITH_COMPLETED },
}

// withNodes はボードにノードを足し、レーンの行の末尾に 1 本ずつ並べる。
function withNodes(...extra: Record<string, unknown>[]) {
  const rows = extra.map(n => ({ lanes: '*', node: n.key as string }))
  return {
    ...BOARD,
    graph: { ...BOARD.graph, nodes: [...NODES, ...extra], rows: [...ROWS, ...rows], rows_with_completed: [...ROWS_WITH_COMPLETED, ...rows] },
  }
}

// 利用者が composer で /board を打ったときの command.run。
const OPEN_BOARD = {
  command: 'board',
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

type T = Parameters<Parameters<typeof test>[1]>[0]

const mount = ($: T, surface: 'terminal' | 'desktop' = 'terminal', props: object = PANE_PROPS) =>
  $.ui.mount({ plugin: 'agents-daemon', surface, component: 'Pane', props: props as typeof PANE_PROPS, requestId: 'agents-daemon-board' })

const SELF_ARGS = ['--session', 'SELF', '--cwd', CWD]
// CLAIM は送る前にメモを取る park set、REVERT は届かなかったメモを戻す park set。
const CLAIM = [BIN, 'park', 'set', MEMO.id, '--status', 'handed', '--from', 'open', ...SELF_ARGS]
const REVERT = [BIN, 'park', 'set', MEMO.id, '--status', 'open', '--from', 'handed', ...SELF_ARGS]

test('fullscreen でなければ /board は開かず、その旨を返す', async ($, on) => {
  const w = boardWorld(on)
  const result = await $.command.run({ ...OPEN_BOARD, presentation: { isFullscreen: false, columns: 160 } })
  expect(result.text).toMatch(/fullscreen/)
  expect(w.opened).toEqual([])
  expect(w.ran).toEqual([])
})

test('/board は開いたセッションの ID と cwd で status --board を読み、dock に開く', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  expect(w.ran[0]).toEqual([BIN, 'status', '--board', ...SELF_ARGS])
  expect(w.opened).toEqual(['agents-daemon-board'])
})

test('入力欄の上 (inline) に描かれたら pane を閉じ、閉じたことを toast で知らせる', async ($, on) => {
  const w = boardWorld(on)
  const toasts: string[] = []
  on('ui.toast', async (_$, e) => {
    toasts.push(e.text)
    return { value: undefined }
  })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($, 'terminal', { ...PANE_PROPS, placement: 'inline' })
  expect(w.closed).toEqual(['agents-daemon-board'])
  expect(toasts[0]).toMatch(/110 桁/)
  expect(await ui.find({ key: 'row:Q/1' })).toBeUndefined()
  await ui.unmount()
  // 閉じるまでに inline の描画がもう一度走っても、知らせは 1 回だけ。
  const again = await mount($, 'terminal', { ...PANE_PROPS, placement: 'inline' })
  await again.unmount()
  expect(toasts).toHaveLength(1)
})

test('レーンの行を上から描く: 罫線は薄く、ノードは記号と title、つなぐだけの行は罫線だけ', async ($, on) => {
  // W/1 → R/1 の辺 (セッションをまたぐ、破線) が Q/1 → R/1 に合流し、R/1 の右を別のレーンが通る見本。
  const rows = [
    { lanes: '*', node: 'Q/1' },
    { lanes: '│ *', node: 'W/1' },
    { lanes: '├┄┘' },
    { lanes: '* │', node: 'R/1' },
  ]
  boardWorld(on, { ...BOARD, graph: { ...BOARD.graph, rows, rows_with_completed: rows } })
  await $.command.run(OPEN_BOARD)
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect((await ui.find({ key: 'box:row:Q/1' }))?.text).toMatch(/^ {2}\? 論点を決める ↓3$/)
    expect((await ui.find({ key: 'box:row:W/1' }))?.text).toMatch(/^ {2}│ ▸ edge の CLI ◇1$/)
    expect((await ui.find({ key: 'box:lane:2' }))?.text).toMatch(/^ {2}├┄┘$/)
    expect((await ui.find({ key: 'box:row:R/1' }))?.text).toMatch(/^ {2}○ │ リリース$/)
    // 行に無いノードは描かない。節の見出しも無い。
    expect(await ui.find({ key: 'row:V/1' })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /blocked \d/ })).toBeUndefined()
    await ui.unmount()
  }
})

test('長い title は pane の幅からレーンの分を除いて 1 行に切り詰める', async ($, on) => {
  const long = node('L/1', 'working', 'とても長いタイトル'.repeat(10), { session: 'long' })
  const rows = [...ROWS, { lanes: '│ *', node: 'L/1' }]
  boardWorld(on, { ...withNodes(long), graph: { ...withNodes(long).graph, rows, rows_with_completed: rows } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($, 'terminal', { ...PANE_PROPS, bodyColumns: 40 })
  const row = await ui.find({ key: 'box:row:L/1' })
  expect(row?.text).toMatch(/^ {2}│ ▸ (とても長いタイトル)+.*…$/)
  // 行頭の印とレーンを含めて pane の幅に収める (全角は 2 桁)。行は枠を描かないので、その分も title に使う。
  const cells = [...(row?.text ?? '')].reduce((n, ch) => n + ((ch.codePointAt(0) ?? 0) >= 0x3000 ? 2 : 1), 0)
  expect(cells).toBeLessThanOrEqual(40)
  expect(cells).toBeGreaterThan(38)
})

test('行は枠なしで描き、選んだ行は行頭の › と太字で示す', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  for (const key of ['row:G/1', `park:${MEMO.id}`]) {
    expect((await ui.find({ key }))?.props.plain).toBe(true)
  }
  expect((await ui.find({ key: 'box:row:G/1' }))?.text).toMatch(/^ {2}⊘/)
  await ui.press({ key: 'row:G/1' })
  expect((await ui.find({ key: 'box:row:G/1' }))?.text).toMatch(/^› ⊘/)
  // Text は key を持てないので、行の Button の子に太字の Text があるかで見る。
  const hasBold = async (key: string) =>
    ((await ui.find({ key }))?.children ?? []).some(c => typeof c === 'object' && c !== null && (c as { props?: { bold?: boolean } }).props?.bold === true)
  expect(await hasBold('row:G/1')).toBe(true)
  expect(await hasBold('row:W/1')).toBe(false)
})

test('選んだノードの詳細は枠で囲み、操作ボタンは枠の最後の行に枠付きで並べる', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:G/1' })
  expect((await ui.find({ key: 'detail:G/1' }))?.props.borderStyle).toBe('round')
  const actions = await ui.find({ key: 'actions:G/1' })
  expect(actions?.props.flexDirection).toBe('row')
  expect(actions?.text).toMatch(/\+ Add blocker.*To parking/)
  for (const key of ['add-blocker', 'adopt']) {
    expect((await ui.find({ key }))?.props.plain).toBeUndefined()
  }
})

test('選んだメモの詳細も枠で囲み、操作ボタンを枠の最後の行に並べる', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect((await ui.find({ key: 'memo' }))?.props.borderStyle).toBe('round')
  const actions = await ui.find({ key: 'actions:memo' })
  expect(actions?.props.flexDirection).toBe('row')
  expect(actions?.text).toMatch(/Discard.*To task → edge-cli.*Ask for issue → peer/)
})

test('辺の端点にできないノード (参照の無いもの) には Add blocker の代わりに理由を出す', async ($, on) => {
  const todo = { ...node('T/todo-0', 'working', 'todo の項目', { session: 'todo' }), kind: 'todo', ref: undefined }
  boardWorld(on, withNodes(todo))
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:T/todo-0' })
  expect(await ui.find({ key: 'add-blocker' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /辺の端点にできないタスク/ })).toBeDefined()
})

test('数の行の下にレーンの行を全部描き、完了は Show completed で出す', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /私用のボード/ })).toBeDefined()
    // 数: 止まっているノード、その下流、進行中、待っている、完了、メモ。
    expect(await ui.find({ type: 'Text', text: /■ 2 {2}○ 4 {2}▸ 2 {2}↵ 1 {2}✓ 1 {2}◇ 1/ })).toBeDefined()
    // 止まっているノードには下流の数、途中で置いたメモの数、待ちの輪の印。
    expect(await ui.find({ key: 'row:Q/1', text: /\? 論点を決める ↓3/ })).toBeDefined()
    expect(await ui.find({ key: 'row:Q/2', text: /^○ 段5$/ })).toBeDefined()
    expect(await ui.find({ key: 'row:X/1', text: /○ x を待つ ↓1 ⟳/ })).toBeDefined()
    expect(await ui.find({ key: 'row:X/2', text: /⟳/ })).toBeDefined()
    expect(await ui.find({ key: 'row:W/1', text: /^▸ edge の CLI ◇1$/ })).toBeDefined()
    expect(await ui.find({ key: 'row:D/1', text: /↵ 要約を書いた/ })).toBeDefined()
    expect(await ui.find({ key: 'row:ref:issue:o/r#9', text: /^○ issue:o\/r#9$/ })).toBeDefined()
    expect(await ui.find({ key: 'row:P/1', text: /○ 後続/ })).toBeDefined()
    expect(await ui.find({ key: 'row:G/1', text: /⊘ 残った作業/ })).toBeDefined()
    expect(await ui.find({ key: 'row:V/1', text: /○ 確認/ })).toBeDefined()
    // 完了は既定で出さない。
    expect(await ui.find({ key: 'row:C/1' })).toBeUndefined()
    await ui.unmount()
  }

  const ui = await mount($)
  await ui.press({ key: 'fold', text: /Show completed/ })
  expect(await ui.find({ key: 'row:C/1', text: /✓ 済んだ/ })).toBeDefined()
  await ui.press({ key: 'fold', text: /Hide completed/ })
  expect(await ui.find({ key: 'row:C/1' })).toBeUndefined()
})

test('記号の凡例を pane の一番下に薄く置き、ボードが短くても底に寄せる', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
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
  await ui.press({ key: 'row:W/1' })
  await ui.input({ key: 'instruction', text: '先にテストを足して' })
  const content = await ui.find({ key: 'content' })
  expect(content?.text).toMatch(/edge-cli へ送りました/)
  expect(legend?.text).not.toMatch(/送りました/)
})

test('選んだノードの詳細に参照とセッション、上流をたどった原因の鎖を出す', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:Q/2' })
  expect(await ui.find({ type: 'Text', text: /task:Q\/2 · design/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /blocked by: \? 論点を決める/ })).toBeDefined()
  await ui.press({ key: 'row:Q/1' })
  expect(await ui.find({ type: 'Text', text: /blocked by: none/ })).toBeDefined()
  // 完了を畳んでいる間に完了したノードを選んでいたら、その旨を出す。
  await ui.press({ key: 'fold' })
  await ui.press({ key: 'row:C/1' })
  await ui.press({ key: 'fold' })
  expect(await ui.find({ key: 'detail:C/1' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /選んだタスクは今の図に出ていません/ })).toBeDefined()
})

test('セッションのノードの詳細は ref を出さず、名前と session ID の先頭 8 桁を出す (#196)', async ($, on) => {
  const session = { ...node('S1234567890/session', 'working', 'design'), kind: 'session', ref: 'session:design', session_id: 'S1234567890', session: 'design' }
  boardWorld(on, withNodes(session))
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:S1234567890/session' })
  expect(await ui.find({ type: 'Text', text: /^design · S1234567$/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /session:design/ })).toBeUndefined()
})

test('止まっているノードは title も記号と同じ色で描く (#194)', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  // 行の Button の子は記号、右のレーン、title の 3 つの Text。
  const colors = async (key: string) => ((await ui.find({ key }))?.children ?? []).map(c => (c as { props?: { color?: string } }).props?.color)
  expect(await colors('row:Q/1')).toEqual(['warning', undefined, 'warning'])
  expect(await colors('row:W/1')).toEqual(['suggestion', undefined, undefined])
  // 待ちの輪にいるだけのノードは記号の色が無いので、title にも付けない。
  expect(await colors('row:X/1')).toEqual([undefined, undefined, undefined])
})

test('edge の辺は Unlink で edge rm に元の参照を渡す', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:R/1' })
  w.ran.length = 0
  await ui.press({ key: 'unlink:Q/2' })
  expect(w.ran[0]).toEqual([BIN, 'edge', 'rm', '--before', 'session:design', '--before-cwd', CWD, '--after', 'task:R/1', '--after-cwd', CWD])
})

test('同じセッションの blockedBy は外せないので、そのセッションへ解消を頼む', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:Q/2' })
  expect(await ui.find({ key: 'unlink:Q/1' })).toBeUndefined()
  await ui.press({ key: 'ask:Q/1' })
  expect(w.sent).toHaveLength(1)
  expect(w.sent[0].to).toBe('Q')
  expect(w.sent[0].text).toMatch(/タスク #2「段5」が #1「論点を決める」の完了待ち \(blockedBy\)/)
  expect(w.sent[0].text).toMatch(/#1 を completed か deleted にするか、#2 を blockedBy なしで作り直して/)
})

test('Add blocker は循環にならない候補だけを出し、edge add に参照を渡す', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:R/1' })
  await ui.press({ key: 'add-blocker' })
  // 自分、既にある上流、下流 (循環になる)、完了、終了したものは出さない。
  for (const key of ['R/1', 'Q/2', 'V/1', 'C/1', 'G/1']) {
    expect(await ui.find({ key: `blocker:${key}` })).toBeUndefined()
  }
  expect(await ui.find({ key: 'blocker:Q/1' })).toBeDefined()
  expect(await ui.find({ key: 'blocker:ref:issue:o/r#9' })).toBeDefined()
  w.ran.length = 0
  await ui.press({ key: 'blocker:W/1' })
  expect(w.ran[0]).toEqual([BIN, 'edge', 'add', '--before', 'task:W/1', '--before-cwd', CWD, '--after', 'task:R/1', '--after-cwd', CWD])
})

test('生きているほかのセッションのノードへ指示を送る。自分と終了したセッションには送らない', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)

  await ui.press({ key: 'row:SELF/1' })
  expect(await ui.find({ key: 'instruction' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /このセッションのタスクです/ })).toBeDefined()
  await ui.press({ key: 'row:G/1' })
  expect(await ui.find({ key: 'instruction' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /このセッションは終了しています/ })).toBeDefined()

  await ui.press({ key: 'row:W/1' })
  await ui.input({ key: 'instruction', text: '先にテストを足して' })
  expect(w.sent).toEqual([{ to: 'W', text: 'タスク「edge の CLI」について: 先にテストを足して' }])
  expect(await ui.find({ type: 'Text', text: /edge-cli へ送りました/ })).toBeDefined()
})

test('終了したセッションの未完了のタスクは To parking でメモへ移せる', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:W/1' })
  expect(await ui.find({ key: 'adopt' })).toBeUndefined()
  await ui.press({ key: 'row:G/1' })
  w.ran.length = 0
  await ui.press({ key: 'adopt' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'adopt', 'G', '1', ...SELF_ARGS]])
})

test('daemon が status.json を書いていないボードでは、exited と描かれても To parking を出さない', async ($, on) => {
  const { updated_at: _, ...stale } = BOARD
  boardWorld(on, { ...stale, daemon_running: false })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:G/1' })
  expect(await ui.find({ key: 'adopt' })).toBeUndefined()
})

test('メモを選ぶと詳細を出し、Discard / To task / Ask for issue で渡す', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  // 最後に置かれた時刻 (JST) を先頭に、置いたときに進めていたタスクを末尾に添える。
  // 種類は park tool の kind の語のまま出す。
  expect(await ui.find({ key: `park:${MEMO.id}`, text: /^10\/18 19:30 \[aside\] 保持期間の根拠が無い ×2 ← edge の CLI/ })).toBeDefined()
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ type: 'Text', text: /定数にコメントが無い/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /edge-cli 10\/18 19:30 別の定数も同じ/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /gone 10\/19 01:28/ })).toBeDefined()

  // 宛先は自セッション (#190) と、同じ repo の生きているほかのセッション。issue は私用のボードでボード全体のセッション。
  expect(await ui.find({ key: 'task:SELF', text: /To task → このセッション/ })).toBeDefined()
  expect(await ui.find({ key: 'task:G' })).toBeUndefined()
  expect(await ui.find({ key: 'task:P' })).toBeUndefined()
  expect(await ui.find({ key: 'task:W', text: /To task → edge-cli/ })).toBeDefined()
  expect(await ui.find({ key: 'issue:SELF', text: /Ask for issue → このセッション/ })).toBeDefined()
  expect(await ui.find({ key: 'issue:P', text: /Ask for issue → peer/ })).toBeDefined()
  // 宛先のセッション名だけでは何をしているセッションか分からないので、そのセッションのカードの subject を添える (#191)。
  expect((await ui.find({ key: 'task:W' }))?.text).toBe('To task → edge-cli (edge の CLI)')
  expect((await ui.find({ key: 'issue:P' }))?.text).toBe('Ask for issue → peer (後続)')

  w.ran.length = 0
  await ui.press({ key: 'task:W' })
  expect(w.sent[0].to).toBe('W')
  expect(w.sent[0].text).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」\(種類: aside\) をタスクにしてください。/)
  // 送る前に取る (open のときだけ handed にする)。届いたら handed のまま。
  expect(operations(w.ran)).toEqual([CLAIM])

  // 選んだメモは操作の後も開いたまま (同じ行をもう一度押すと閉じる)。捨てるのも open のときだけ。
  w.ran.length = 0
  await ui.press({ key: 'discard' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'set', MEMO.id, '--status', 'discarded', '--from', 'open', ...SELF_ARGS]])
})

test('取れなかったメモ (ほかのセッションが先に渡した) は送らない', async ($, on) => {
  const w = world(on, argv =>
    argv[1] === 'status' ? output(JSON.stringify(BOARD)) : argv[2] === 'set' ? output('', 1, `agents-daemon: メモ ${MEMO.id} の status は handed (期待 open) なので書き換えない`) : output('{}'),
  )
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  w.ran.length = 0
  await ui.press({ key: 'task:W' })
  expect(operations(w.ran)).toEqual([CLAIM])
  expect(w.sent).toEqual([])
  expect(await ui.find({ type: 'Text', text: /メモを取れませんでした.*status は handed/ })).toBeDefined()
})

test('自セッションへは peer メッセージではなく自分の入力へ渡し、入ったらメモを handed にする (#190)', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })

  w.ran.length = 0
  await ui.press({ key: 'issue:SELF' })
  expect(w.sent).toEqual([])
  expect(w.submitted).toHaveLength(1)
  expect(w.submitted[0]).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」を GitHub の issue にしてください。repo: agents-daemon。/)
  expect(operations(w.ran)).toEqual([CLAIM])
  expect(await ui.find({ type: 'Text', text: /このセッション へ送り、メモを渡しました/ })).toBeDefined()
})

test('自分の入力へ渡せなかった (drop) メモは open に戻す', async ($, on) => {
  const w = boardWorld(on, BOARD, undefined, 'Stop が立っている')
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })

  w.ran.length = 0
  await ui.press({ key: 'task:SELF' })
  expect(operations(w.ran)).toEqual([CLAIM, REVERT])
  expect(await ui.find({ type: 'Text', text: /このセッション へ送れませんでした: Stop が立っている。メモは open に戻しました/ })).toBeDefined()
})

test('ほかの repo のメモは自セッションでタスクにできないが、issue は頼める', async ($, on) => {
  const elsewhere = { ...MEMO, id: '20261018T100000-bbbb', title: 'dotfile の件' }
  const rows = [BOARD.rows[0], { ...BOARD.rows[1], parking: [elsewhere] }]
  boardWorld(on, { ...BOARD, rows })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${elsewhere.id}` })
  expect(await ui.find({ key: 'task:SELF' })).toBeUndefined()
  expect(await ui.find({ key: 'task:P', text: /To task → peer/ })).toBeDefined()
  expect(await ui.find({ key: 'issue:SELF', text: /Ask for issue → このセッション/ })).toBeDefined()
})

test('最後に置かれてから 30 日より経ったメモがあれば、パーキングの見出しからまとめて捨てる', async ($, on) => {
  // MEMO は 10/18 に置かれた。時計を 12/01 にすると 30 日より古い。
  const w = world(
    on,
    argv => (argv[1] === 'status' ? output(JSON.stringify(BOARD)) : argv[2] === 'discard' ? output(JSON.stringify([{ id: MEMO.id }])) : output('{}')),
    Date.parse('2026-12-01T00:00:00Z'),
  )
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ key: 'discard-stale', text: /Discard 1 older than 30d/ })).toBeDefined()
  w.ran.length = 0
  await ui.press({ key: 'discard-stale' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'discard', '--days', '30', ...SELF_ARGS]])
  // 戻し方を添える (id はボードの経路のログに残る)。
  expect(await ui.find({ type: 'Text', text: /30 日より前のメモ 1 つを捨てました.*park set <id> --status open/ })).toBeDefined()
})

test('30 日以内のメモだけなら、まとめて捨てるボタンを出さない', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ key: 'discard-stale' })).toBeUndefined()
})

test('業務のボードには Ask for issue を出さない', async ($, on) => {
  boardWorld(on, { ...BOARD, domain: 'work' })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ key: 'task:W' })).toBeDefined()
  expect(await ui.find({ key: 'task:SELF' })).toBeDefined()
  expect(await ui.find({ key: 'spawn-task' })).toBeDefined()
  expect(await ui.find({ key: 'issue:P' })).toBeUndefined()
  expect(await ui.find({ key: 'issue:SELF' })).toBeUndefined()
  expect(await ui.find({ key: 'spawn-issue' })).toBeUndefined()
})

test('メモを別の repo の新しいセッションへ渡す: 候補はメモの repo、ほかの行の repo の順 (#162)', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ key: 'spawn:/src/home/dotfile' })).toBeUndefined()

  await ui.press({ key: 'spawn-task', text: /To task → new session…/ })
  expect(await ui.find({ key: 'spawn:/src/home/agents-daemon', text: /agents-daemon/ })).toBeDefined()
  expect(await ui.find({ key: 'spawn:/src/home/dotfile', text: /dotfile/ })).toBeDefined()

  w.ran.length = 0
  await ui.press({ key: 'spawn:/src/home/dotfile' })
  expect(operations(w.ran)).toEqual([CLAIM, [BIN, 'spawn', '--repo', '/src/home/dotfile', ...SELF_ARGS]])
  expect(w.fed).toHaveLength(1)
  expect(w.fed[0]).toMatch(/^このメモを渡すため、dotfile \(\/src\/home\/dotfile\) で新しいセッションとして起こしました。/)
  expect(w.fed[0]).toMatch(/パーキングロットのメモ「保持期間の根拠が無い」\(種類: aside\) をタスクにしてください。/)
  expect(w.sent).toEqual([])
  expect(await ui.find({ type: 'Text', text: /dotfile で新しいセッションを起こし、メモを渡しました/ })).toBeDefined()
  // 候補は渡した後に畳む。
  expect(await ui.find({ key: 'spawn:/src/home/dotfile' })).toBeUndefined()
})

test('issue を頼む新しいセッションには、起こした repo を起票先にした文面を渡す', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await ui.press({ key: 'spawn-issue', text: /Ask for issue → new session…/ })
  await ui.press({ key: 'spawn:/src/home/dotfile' })
  expect(w.fed[0]).toMatch(/パーキングロットのメモ「保持期間の根拠が無い」を GitHub の issue にしてください。repo: dotfile。/)
})

test('候補に無い repo は絶対パスで指定する。相対パスは起こさずに弾き、Cancel で候補を畳む', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await ui.press({ key: 'spawn-task' })

  w.ran.length = 0
  await ui.input({ key: 'spawn-path', text: 'src/home/other' })
  expect(operations(w.ran)).toEqual([])
  expect(await ui.find({ type: 'Text', text: /絶対パス/ })).toBeDefined()

  await ui.input({ key: 'spawn-path', text: '/src/home/other' })
  expect(operations(w.ran)[1]).toEqual([BIN, 'spawn', '--repo', '/src/home/other', ...SELF_ARGS])
  expect(w.fed[0]).toMatch(/^このメモを渡すため、other \(\/src\/home\/other\) で新しいセッションとして起こしました。/)

  await ui.press({ key: 'spawn-task' })
  expect(await ui.find({ key: 'spawn-path' })).toBeDefined()
  await ui.press({ key: 'spawn-cancel' })
  expect(await ui.find({ key: 'spawn-path' })).toBeUndefined()
})

test('新しいセッションを起こせなかった (window を作っていない) メモは open に戻す', async ($, on) => {
  const w = world(on, argv =>
    argv[1] === 'status' ? output(JSON.stringify(BOARD)) : argv[1] === 'spawn' ? output('', 1, 'agents-daemon: 開いたセッションの domain (home) と repo の domain (work) が違うので起こさない') : output('{}'),
  )
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await ui.press({ key: 'spawn-task' })
  w.ran.length = 0
  await ui.press({ key: 'spawn:/src/home/dotfile' })
  expect(operations(w.ran)).toEqual([CLAIM, [BIN, 'spawn', '--repo', '/src/home/dotfile', ...SELF_ARGS], REVERT])
  expect(await ui.find({ type: 'Text', text: /dotfile で新しいセッションを起こせませんでした: .*domain \(work\) が違う.*メモは open に戻しました/ })).toBeDefined()
})

test('claude が前景にならなかった (pane は残る) メモは戻さず、pane と戻し方を知らせる', async ($, on) => {
  const w = world(on, argv =>
    argv[1] === 'status'
      ? output(JSON.stringify(BOARD))
      : argv[1] === 'spawn'
        ? output('{"session_id":"NEW","pane_id":"block:b1","foreground":false,"detail":"15s の間に pane block:b1 で claude が前景にならなかった"}')
        : output('{}'),
  )
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await ui.press({ key: 'spawn-task' })
  w.ran.length = 0
  await ui.press({ key: 'spawn:/src/home/dotfile' })
  expect(operations(w.ran)).toEqual([CLAIM, [BIN, 'spawn', '--repo', '/src/home/dotfile', ...SELF_ARGS]])
  expect(await ui.find({ type: 'Text', text: /dotfile で window を作りましたが.*前景にならなかった.*handed のまま.*park set .* --status open/ })).toBeDefined()
})

test('ノードの無いボードと、daemon が動いていないことを描く', async ($, on) => {
  boardWorld(on, { domain: 'work', daemon_running: false, columns: [], rows: [], graph: { nodes: [], links: [], longest_chain: [], rows: [], rows_with_completed: [] } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /業務のボード \(agents-daemon が動いていない/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /タスクはまだありません/ })).toBeDefined()
})

test('状態を分類できないノードは · で出し、待っているものに数えて working には数えない', async ($, on) => {
  const session = (key: string, state: string, title: string) => ({ ...node(key, state, title), kind: 'session', ref: undefined })
  boardWorld(on, withNodes(session('U/session', 'unknown', 'dotfiles')))
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /▸ 2 {2}↵ 2 {2}✓ 1/ })).toBeDefined()
  expect(await ui.find({ key: 'row:U/session', text: /^· dotfiles$/ })).toBeDefined()
})

test('board が失敗したら理由を描く', async ($, on) => {
  world(on, () => output('', 1, 'status snapshot のパースに失敗'))
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /status snapshot のパースに失敗/ })).toBeDefined()
})
