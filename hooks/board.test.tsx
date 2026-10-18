import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

// world は plugin の下にいる engine の代わりを立てる。board の呼び出しは run が答える。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult) {
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('ui.open', async () => ({ value: { isPlaced: true } }))
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: '/src/home/agents-daemon' }))
  on('process.run', async (_$, e) => ({ value: run(e.argv) }))
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

const COLUMNS = [
  { id: 'pending', title: '未着手' },
  { id: 'in_progress', title: '進行中' },
  { id: 'blocked', title: 'ブロック' },
  { id: 'completed', title: '完了 (直近)' },
]

function card(key: string, column: string, subject: string, session_id: string, extra: Record<string, unknown> = {}) {
  return { key, kind: 'task', column, subject, session_id, session: session_id.toLowerCase(), live: true, updated_at: '2026-10-18T09:00:00Z', ...extra }
}

// board の JSON は internal/board の形。agents-daemon の行に 4 枚: 送れるもの (入力待ちの印付き)、ブロック、
// 自分自身、終了したセッション。dotfile の行に 1 枚。
const BOARD = {
  domain: 'home',
  daemon_running: true,
  updated_at: '2026-10-18T09:00:00Z',
  columns: COLUMNS,
  rows: [
    {
      repo: '/src/home/agents-daemon',
      title: 'agents-daemon',
      parking: [] as unknown[],
      cards: [
        card('PEER/2', 'in_progress', 'ingest-task を書く', 'PEER', { mark: 'input', mark_detail: 'permission' }),
        card('PEER/3', 'blocked', 'PR を出す', 'PEER', { blockers: 1 }),
        card('SELF/1', 'in_progress', 'ボードを書く', 'SELF'),
        card('GONE/1', 'pending', '残った作業', 'GONE', { live: false }),
      ],
    },
    { repo: '/src/home/dotfile', title: 'dotfile', cards: [card('C/1', 'pending', 'alias を整理', 'C')], parking: [] },
  ],
}

// メモ 1 つ (PEER と GONE が置いた) を agents-daemon の行に足したボード。GONE/1 は adopt できるタスク。
const MEMO = {
  id: '20261018T090000-aaaa',
  kind: 'aside',
  title: '保持期間の根拠が無い',
  detail: '定数にコメントが無い',
  files: ['internal/parking/parking.go'],
  default_taken: 'やらない',
  sources: [
    { session_id: 'PEER', session: 'peer', live: true, detail: '別の定数も同じ', at: '2026-10-18T19:30:00+09:00' },
    // UTC で書かれた時刻も JST で出す。
    { session_id: 'GONE', session: 'gone', live: false, at: '2026-10-18T16:28:44Z' },
  ],
}

function withParking(domain: 'home' | 'work') {
  return {
    ...BOARD,
    domain,
    rows: [
      { ...BOARD.rows[0], cards: BOARD.rows[0].cards.map(c => (c.key === 'GONE/1' ? { ...c, task_id: '1' } : c)), parking: [MEMO] },
      BOARD.rows[1],
    ],
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

const mount = ($: Parameters<Parameters<typeof test>[1]>[0], surface: 'terminal' | 'desktop' = 'terminal') =>
  $.ui.mount({ plugin: 'agents-daemon', surface, component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })

test('repo の行と状態の列でタスクを描き、選んだタスクのセッションへ subject を添えて送る', async ($, on) => {
  const ran: string[][] = []
  world(on, argv => {
    ran.push([...argv])
    return output(JSON.stringify(BOARD))
  })
  const sent: { to: unknown; text: string }[] = []
  on('session.send', async (_$, e) => {
    sent.push({ to: e.to, text: e.text })
    return { isDelivered: true as const }
  })

  await $.command.run(OPEN_BOARD)
  // バイナリは hooks/lib/daemon-bin.sh と同じ置き場 (XDG_CACHE_HOME が無ければ ~/.cache) を呼び、
  // 開いたセッションの ID と cwd を渡して、その domain だけを描かせる。
  expect(ran[0]).toEqual([
    '/home/u/.cache/agents-daemon/bin/agents-daemon',
    'status',
    '--board',
    '--session',
    'SELF',
    '--cwd',
    '/src/home/agents-daemon',
  ])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /私用のボード/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /^agents-daemon$/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /進行中 \(2\)/ })).toBeDefined()
    expect(await ui.find({ key: 'PEER/2', text: /⚠入力待ち \(permission\)/ })).toBeDefined()
    expect(await ui.find({ key: 'PEER/3', text: /←1/ })).toBeDefined()
    expect(await ui.find({ key: 'SELF/1', text: /このセッション/ })).toBeDefined()
    expect(await ui.find({ key: 'GONE/1', text: /\(終了\)/ })).toBeDefined()

    // 自分自身と終了したセッションを選んでも入力欄は出ない。
    await ui.press({ key: 'SELF/1' })
    expect(await ui.find({ key: 'instruction' })).toBeUndefined()
    await ui.press({ key: 'GONE/1' })
    expect(await ui.find({ key: 'instruction' })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /終了しています/ })).toBeDefined()

    await ui.press({ key: 'PEER/2' })
    await ui.input({ key: 'instruction', text: '先にテストを足して' })
    expect(await ui.find({ type: 'Text', text: /peer へ送りました/ })).toBeDefined()
    await ui.unmount()
  }

  // { sessionId } で送った宛先は、engine が session.send の hook へ渡すときに ID の文字列へ直す。
  const text = 'タスク「ingest-task を書く」について: 先にテストを足して'
  expect(sent).toEqual([
    { to: 'PEER', text },
    { to: 'PEER', text },
  ])
})

test('タスクの無いボードと、daemon が動いていないことを描く', async ($, on) => {
  world(on, () => output(JSON.stringify({ domain: 'work', daemon_running: false, columns: COLUMNS, rows: [] })))

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /業務のボード \(agents-daemon が動いていない/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /タスクはまだありません/ })).toBeDefined()
})

// parkWorld は status --board に data を返し、park の呼び出しを ran に残して成功を返す。
function parkWorld(on: On, data: unknown, ran: string[][], delivered = true) {
  world(on, argv => {
    if (argv[1] === 'status') return output(JSON.stringify(data))
    ran.push([...argv.slice(1)])
    return output(JSON.stringify({ created: true, item: { id: 'x' } }))
  })
  const sent: { to: unknown; text: string }[] = []
  on('session.send', async (_$, e) => {
    sent.push({ to: e.to, text: e.text })
    return delivered ? { isDelivered: true as const } : { isDelivered: false as const, reason: 'not_found' as const }
  })
  return sent
}

const SELF_ARGS = ['--session', 'SELF', '--cwd', '/src/home/agents-daemon']

test('パーキングの列にメモを描き、選ぶと詳細と置いたセッションを出し、捨てられる', async ($, on) => {
  const ran: string[][] = []
  parkWorld(on, withParking('home'), ran)

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /パーキング \(1\)/ })).toBeDefined()
  expect(await ui.find({ key: `park:${MEMO.id}`, text: /\[脇\] 保持期間の根拠が無い ×2/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /定数にコメントが無い/ })).toBeUndefined()

  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ type: 'Text', text: /定数にコメントが無い/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /既定: やらない/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /internal\/parking\/parking\.go/ })).toBeDefined()
  // sources は新しい順に、セッションの名前、時刻、後から置いた側の detail。
  expect(await ui.find({ type: 'Text', text: /peer 10\/18 19:30 別の定数も同じ/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /gone 10\/19 01:28/ })).toBeDefined()

  await ui.press({ key: 'discard' })
  expect(ran).toEqual([['park', 'set', MEMO.id, '--status', 'discarded', ...SELF_ARGS]])
  expect(await ui.find({ type: 'Text', text: /捨てました/ })).toBeDefined()
})

test('タスクにするは同じ行の生きているセッションへ送り、届いたときだけ handed にする', async ($, on) => {
  const ran: string[][] = []
  const sent = parkWorld(on, withParking('home'), ran)

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  // 宛先は同じ行の生きているセッション (自分と終了したセッションは出さない)。
  expect(await ui.find({ key: 'task:SELF' })).toBeUndefined()
  expect(await ui.find({ key: 'task:GONE' })).toBeUndefined()
  expect(await ui.find({ key: 'task:C' })).toBeUndefined()
  await ui.press({ key: 'task:PEER' })

  expect(sent).toHaveLength(1)
  expect(sent[0].to).toBe('PEER')
  expect(sent[0].text).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」\(種類: 脇\) をタスクにしてください。TaskCreate で作り/)
  expect(sent[0].text).toMatch(/詳細: 定数にコメントが無い/)
  expect(sent[0].text).toMatch(/関係するファイル: internal\/parking\/parking\.go/)
  expect(sent[0].text).toMatch(/置いたときの既定: やらない/)
  expect(sent[0].text).toMatch(/\(メモ 20261018T090000-aaaa\)/)
  expect(ran).toEqual([['park', 'set', MEMO.id, '--status', 'handed', ...SELF_ARGS]])
})

test('届かなければ handed にしない', async ($, on) => {
  const ran: string[][] = []
  parkWorld(on, withParking('home'), ran, false)

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  await ui.press({ key: 'task:PEER' })
  expect(ran).toEqual([])
  expect(await ui.find({ type: 'Text', text: /送れませんでした/ })).toBeDefined()
})

test('issue にするよう頼むのは私用のボードだけで、届いたら handed にする', async ($, on) => {
  const ran: string[][] = []
  const sent = parkWorld(on, withParking('home'), ran)

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  // 同じ domain のボードに載っている生きているセッションなら、別の行のセッションにも頼める。
  expect(await ui.find({ key: 'issue:C' })).toBeDefined()
  await ui.press({ key: 'issue:PEER' })
  expect(sent[0].text).toMatch(/^パーキングロットのメモ「保持期間の根拠が無い」を GitHub の issue にしてください。repo: agents-daemon。/)
  expect(sent[0].text).toMatch(/起票したら URL を返してください。/)
  expect(ran).toEqual([['park', 'set', MEMO.id, '--status', 'handed', ...SELF_ARGS]])
})

test('同じ行に送り先が無ければ、そう出してメモは open のまま', async ($, on) => {
  const ran: string[][] = []
  const data = withParking('work')
  // dotfile の行の C は終了したセッション。メモをそこへ移す。
  const lone = { ...BOARD.rows[1], cards: [card('C/1', 'pending', 'alias を整理', 'C', { live: false })], parking: [MEMO] }
  parkWorld(on, { ...data, rows: [BOARD.rows[0], lone] }, ran)

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ type: 'Text', text: /送り先のセッションがありません/ })).toBeDefined()
  // 別の行の生きているセッション (PEER) へはタスクにさせない。
  expect(await ui.find({ key: 'task:PEER' })).toBeUndefined()
  expect(await ui.find({ key: 'task:C' })).toBeUndefined()
  expect(ran).toEqual([])
})

test('業務のボードには issue の操作を出さない', async ($, on) => {
  parkWorld(on, withParking('work'), [])

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: `park:${MEMO.id}` })
  expect(await ui.find({ key: 'task:PEER' })).toBeDefined()
  expect(await ui.find({ key: 'issue:PEER' })).toBeUndefined()
  expect(await ui.find({ key: 'issue:C' })).toBeUndefined()
})

test('終了したセッションの未完了のタスクはパーキングへ移せる', async ($, on) => {
  const ran: string[][] = []
  parkWorld(on, withParking('home'), ran)

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  // 生きているセッションのタスクには出さない。
  await ui.press({ key: 'PEER/3' })
  expect(await ui.find({ key: 'adopt' })).toBeUndefined()
  await ui.press({ key: 'GONE/1' })
  await ui.press({ key: 'adopt' })
  expect(ran).toEqual([['park', 'adopt', 'GONE', '1', ...SELF_ARGS]])
  expect(await ui.find({ type: 'Text', text: /パーキングへ移しました/ })).toBeDefined()
})

test('board が失敗したら理由を描く', async ($, on) => {
  world(on, () => output('', 1, 'status snapshot のパースに失敗'))

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /status snapshot のパースに失敗/ })).toBeDefined()
})
