import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'

// world は plugin の下にいる engine の代わりを立てる。process.run は run が答え、呼ばれた argv (バイナリの後) を
// ran に残す。ボードの経路のログ (hooks/log.ts) は log.test.tsx が見るので残さない。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult = () => output('{}')) {
  const ran: string[][] = []
  const opened: string[] = []
  const closed: string[] = []
  const sent: { to: unknown; text: string }[] = []
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
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
    return { value: run(e.argv) }
  })
  on('session.send', async (_$, e) => {
    sent.push({ to: e.to, text: e.text })
    return { isDelivered: true as const }
  })
  return { ran, opened, closed, sent }
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// operations は ran のうち、操作の後の読み直し (status --board) を除いたもの。
function operations(ran: string[][]): string[][] {
  return ran.filter(argv => argv[1] !== 'status')
}

// boardWorld は status --board に data を返し、それ以外 (park、edge) は成功を返す。
function boardWorld(on: On, data: unknown = BOARD) {
  return world(on, argv => (argv[1] === 'status' ? output(JSON.stringify(data)) : output('{"created":true}')))
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
  graph: { nodes: NODES, links: LINKS, longest_chain: ['Q/1', 'Q/2', 'R/1', 'V/1'] },
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
  expect(await ui.find({ key: 'row:Q/1:Q/1' })).toBeUndefined()
  await ui.unmount()
  // 閉じるまでに inline の描画がもう一度走っても、知らせは 1 回だけ。
  const again = await mount($, 'terminal', { ...PANE_PROPS, placement: 'inline' })
  await again.unmount()
  expect(toasts).toHaveLength(1)
})

test('⊘ の節は、先の根の木に出た exited のノードを根にしない', async ($, on) => {
  const gone2 = node('G/2', 'exited', '残った作業の続き', { session: 'gone', blocked_by: ['G/1'] })
  const nodes = NODES.map(n => (n.key === 'G/1' ? { ...n, downstream: 1 } : n))
  boardWorld(on, { ...BOARD, graph: { ...BOARD.graph, nodes: [...nodes, gone2], links: [...LINKS, { before: 'G/1', after: 'G/2', kind: 'task' }] } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /⊘ exited 1/ })).toBeDefined()
  expect(await ui.find({ key: 'row:G/1:G/2' })).toBeDefined()
  expect(await ui.find({ key: 'row:G/2:G/2' })).toBeUndefined()
})

test('長い title は pane の幅に合わせて 1 行に切り詰める', async ($, on) => {
  const long = node('L/1', 'working', 'とても長いタイトル'.repeat(10), { session: 'long' })
  boardWorld(on, { ...BOARD, graph: { ...BOARD.graph, nodes: [...NODES, long] } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($, 'terminal', { ...PANE_PROPS, bodyColumns: 40 })
  const row = await ui.find({ key: 'row:L/1:L/1' })
  expect(row?.text).toMatch(/^▸ (とても長いタイトル)+.*…$/)
  // 行頭の印の 2 桁を除いた幅に収める (全角は 2 桁)。行は枠を描かないので、その分も title に使う。
  const cells = [...(row?.text ?? '')].reduce((n, ch) => n + ((ch.codePointAt(0) ?? 0) >= 0x3000 ? 2 : 1), 0)
  expect(cells).toBeLessThanOrEqual(38)
  expect(cells).toBeGreaterThan(36)
})

test('行は枠なしで描き、選んだ行は行頭の › と太字で示す', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  for (const key of ['row:G/1:G/1', 'more:Q/1:R/1', `park:${MEMO.id}`]) {
    expect((await ui.find({ key }))?.props.plain).toBe(true)
  }
  expect((await ui.find({ key: 'box:row:G/1:G/1' }))?.text).toMatch(/^ {2}⊘/)
  await ui.press({ key: 'row:G/1:G/1' })
  expect((await ui.find({ key: 'box:row:G/1:G/1' }))?.text).toMatch(/^› ⊘/)
  // Text は key を持てないので、行の Button の子に太字の Text があるかで見る。
  const hasBold = async (key: string) =>
    ((await ui.find({ key }))?.children ?? []).some(c => typeof c === 'object' && c !== null && (c as { props?: { bold?: boolean } }).props?.bold === true)
  expect(await hasBold('row:G/1:G/1')).toBe(true)
  expect(await hasBold('row:W/1:W/1')).toBe(false)
})

test('選んだノードの詳細は枠で囲み、操作ボタンは枠の最後の行に枠付きで並べる', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:G/1:G/1' })
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
  boardWorld(on, { ...BOARD, graph: { ...BOARD.graph, nodes: [...NODES, todo] } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:T/todo-0:T/todo-0' })
  expect(await ui.find({ key: 'add-blocker' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /辺の端点にできないタスク/ })).toBeDefined()
})

test('止まっているノードを根にした木を節ごとに描き、深い段は畳んで押すと開く', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /私用のボード/ })).toBeDefined()
    // 数: 止まっている根、その下流、進行中、完了、メモ。
    expect(await ui.find({ type: 'Text', text: /■ 2 {2}○ 4 {2}▸ 2 {2}↵ 1 {2}✓ 1 {2}◇ 1/ })).toBeDefined()
    // 節は ■ → ▸ → ↵ → ○ → ⊘ → ◇ の順で、見出しに根の数。ターンを終えて待っているノードは ↵ idle に分ける。
    for (const head of [/■ blocked 2/, /▸ working 2/, /↵ idle 1/, /○ pending 1/, /⊘ exited 1/, /◇ parking 1/]) {
      expect(await ui.find({ type: 'Text', text: head })).toBeDefined()
    }
    // 根には下流の数、途中で置いたメモの数、待ちの輪の印。
    expect(await ui.find({ key: 'row:Q/1:Q/1', text: /\? 論点を決める ↓3/ })).toBeDefined()
    expect(await ui.find({ key: 'row:Q/1:Q/2', text: /○ 段5/ })).toBeDefined()
    expect(await ui.find({ key: 'row:X/1:X/1', text: /○ x を待つ.*⟳/ })).toBeDefined()
    expect(await ui.find({ key: 'row:X/1:X/2', text: /⟳/ })).toBeDefined()
    // 輪の 2 つ目は根にしない。
    expect(await ui.find({ key: 'row:X/2:X/2' })).toBeUndefined()
    expect(await ui.find({ key: 'row:W/1:W/1', text: /▸ edge の CLI ↓1 ◇1/ })).toBeDefined()
    expect(await ui.find({ key: 'row:D/1:D/1', text: /↵ 要約を書いた/ })).toBeDefined()
    expect(await ui.find({ key: 'row:ref:issue:o/r#9:ref:issue:o/r#9', text: /○ issue:o\/r#9 ↓1/ })).toBeDefined()
    expect(await ui.find({ key: 'row:ref:issue:o/r#9:P/1', text: /○ 後続/ })).toBeDefined()
    expect(await ui.find({ key: 'row:G/1:G/1', text: /⊘ 残った作業/ })).toBeDefined()
    // 上流のある pending と completed は根にしない。
    expect(await ui.find({ key: 'row:Q/2:Q/2' })).toBeUndefined()
    expect(await ui.find({ key: 'row:C/1:C/1' })).toBeUndefined()
    await ui.unmount()
  }

  // 2 段目より深い段は畳む。押すと開く。
  const ui = await mount($)
  expect(await ui.find({ key: 'row:Q/1:R/1' })).toBeUndefined()
  expect(await ui.find({ key: 'more:Q/1:R/1', text: /\+2 more/ })).toBeDefined()
  await ui.press({ key: 'more:Q/1:R/1' })
  expect(await ui.find({ key: 'row:Q/1:R/1', text: /○ リリース/ })).toBeDefined()
  expect(await ui.find({ key: 'row:Q/1:V/1', text: /○ 確認/ })).toBeDefined()
})

test('選んだノードの詳細に参照とセッション、上流をたどった原因の鎖を出す', async ($, on) => {
  boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:Q/1:Q/2' })
  expect(await ui.find({ type: 'Text', text: /task:Q\/2 · design/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /blocked by: \? 論点を決める/ })).toBeDefined()
  // 選んだノードを含む枝は畳まない。
  await ui.press({ key: 'row:Q/1:Q/1' })
  expect(await ui.find({ type: 'Text', text: /blocked by: none/ })).toBeDefined()
})

test('edge の辺は Unlink で edge rm に元の参照を渡す', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'more:Q/1:R/1' })
  await ui.press({ key: 'row:Q/1:R/1' })
  w.ran.length = 0
  await ui.press({ key: 'unlink:Q/2' })
  expect(w.ran[0]).toEqual([BIN, 'edge', 'rm', '--before', 'session:design', '--before-cwd', CWD, '--after', 'task:R/1', '--after-cwd', CWD])
})

test('同じセッションの blockedBy は外せないので、そのセッションへ解消を頼む', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:Q/1:Q/2' })
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
  await ui.press({ key: 'more:Q/1:R/1' })
  await ui.press({ key: 'row:Q/1:R/1' })
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

  await ui.press({ key: 'row:SELF/1:SELF/1' })
  expect(await ui.find({ key: 'instruction' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /このセッションのタスクです/ })).toBeDefined()
  await ui.press({ key: 'row:G/1:G/1' })
  expect(await ui.find({ key: 'instruction' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /このセッションは終了しています/ })).toBeDefined()

  await ui.press({ key: 'row:W/1:W/1' })
  await ui.input({ key: 'instruction', text: '先にテストを足して' })
  expect(w.sent).toEqual([{ to: 'W', text: 'タスク「edge の CLI」について: 先にテストを足して' }])
  expect(await ui.find({ type: 'Text', text: /edge-cli へ送りました/ })).toBeDefined()
})

test('終了したセッションの未完了のタスクは To parking でメモへ移せる', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'row:W/1:W/1' })
  expect(await ui.find({ key: 'adopt' })).toBeUndefined()
  await ui.press({ key: 'row:G/1:G/1' })
  w.ran.length = 0
  await ui.press({ key: 'adopt' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'adopt', 'G', '1', ...SELF_ARGS]])
})

test('メモを選ぶと詳細を出し、Discard / To task / Ask for issue で渡す', async ($, on) => {
  const w = boardWorld(on)
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  // 置いたときに進めていたタスクを添える。
  // 種類は park tool の kind の語のまま出す。
  expect(await ui.find({ key: `park:${MEMO.id}`, text: /\[aside\] 保持期間の根拠が無い ×2 ← edge の CLI/ })).toBeDefined()
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ type: 'Text', text: /定数にコメントが無い/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /edge-cli 10\/18 19:30 別の定数も同じ/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /gone 10\/19 01:28/ })).toBeDefined()

  // 宛先は同じ repo の生きているほかのセッション。issue は私用のボードでボード全体のセッション。
  expect(await ui.find({ key: 'task:SELF' })).toBeUndefined()
  expect(await ui.find({ key: 'task:G' })).toBeUndefined()
  expect(await ui.find({ key: 'task:P' })).toBeUndefined()
  expect(await ui.find({ key: 'task:W', text: /To task → edge-cli/ })).toBeDefined()
  expect(await ui.find({ key: 'issue:P', text: /Ask for issue → peer/ })).toBeDefined()

  w.ran.length = 0
  await ui.press({ key: 'task:W' })
  expect(w.sent[0].to).toBe('W')
  expect(w.sent[0].text).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」\(種類: aside\) をタスクにしてください。/)
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'set', MEMO.id, '--status', 'handed', ...SELF_ARGS]])

  // 選んだメモは操作の後も開いたまま (同じ行をもう一度押すと閉じる)。
  w.ran.length = 0
  await ui.press({ key: 'discard' })
  expect(operations(w.ran)).toEqual([[BIN, 'park', 'set', MEMO.id, '--status', 'discarded', ...SELF_ARGS]])
})

test('業務のボードには Ask for issue を出さない', async ($, on) => {
  boardWorld(on, { ...BOARD, domain: 'work' })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ key: 'task:W' })).toBeDefined()
  expect(await ui.find({ key: 'issue:P' })).toBeUndefined()
})

test('ノードの無いボードと、daemon が動いていないことを描く', async ($, on) => {
  boardWorld(on, { domain: 'work', daemon_running: false, columns: [], rows: [], graph: { nodes: [], links: [], longest_chain: [] } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /業務のボード \(agents-daemon が動いていない/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /タスクはまだありません/ })).toBeDefined()
})

test('状態を分類できないノードは · で idle の節に出し、working には数えない', async ($, on) => {
  const session = (key: string, state: string, title: string) => ({ ...node(key, state, title), kind: 'session', ref: undefined })
  const nodes = [...NODES, session('U/session', 'unknown', 'dotfiles')]
  boardWorld(on, { ...BOARD, graph: { ...BOARD.graph, nodes } })
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /▸ 2 {2}↵ 2 {2}✓ 1/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /↵ idle 2/ })).toBeDefined()
  expect(await ui.find({ key: 'row:U/session:U/session', text: /^· dotfiles$/ })).toBeDefined()
})

test('board が失敗したら理由を描く', async ($, on) => {
  world(on, () => output('', 1, 'status snapshot のパースに失敗'))
  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /status snapshot のパースに失敗/ })).toBeDefined()
})
