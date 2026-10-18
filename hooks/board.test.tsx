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
      cards: [
        card('PEER/2', 'in_progress', 'ingest-task を書く', 'PEER', { mark: 'input', mark_detail: 'permission' }),
        card('PEER/3', 'blocked', 'PR を出す', 'PEER', { blockers: 1 }),
        card('SELF/1', 'in_progress', 'ボードを書く', 'SELF'),
        card('GONE/1', 'pending', '残った作業', 'GONE', { live: false }),
      ],
    },
    { repo: '/src/home/dotfile', title: 'dotfile', cards: [card('C/1', 'pending', 'alias を整理', 'C')] },
  ],
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

test('board が失敗したら理由を描く', async ($, on) => {
  world(on, () => output('', 1, 'status snapshot のパースに失敗'))

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  expect(await ui.find({ type: 'Text', text: /status snapshot のパースに失敗/ })).toBeDefined()
})
