import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

// world は plugin の下にいる engine の代わりを立てる。board の呼び出しは run が答える。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult) {
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('ui.open', async () => ({ value: { isPlaced: true } }))
  on('session.id', async () => ({ value: 'SELF' }))
  on('process.run', async (_$, e) => ({ value: run(e.argv) }))
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// board の JSON は internal/board の形。カードは 3 枚: 送れるもの、自分自身、セッション不明。
const BOARD = {
  daemon_running: true,
  updated_at: '2026-10-18T09:00:00Z',
  lanes: [
    {
      id: 'blocked',
      title: '入力待ち',
      cards: [
        { pane_id: 'p1', session_id: 's-peer', cwd: '/src/peer', project: 'peer', agent_status: 'blocked', agent_kind: 'permission' },
      ],
    },
    { id: 'waiting', title: '上限待ち', cards: [] },
    {
      id: 'working',
      title: '作業中',
      cards: [
        { pane_id: 'p2', session_id: 'SELF', cwd: '/src/self', project: 'self', agent_status: 'working' },
        { pane_id: 'p3', cwd: '/src/orphan', project: 'orphan', agent_status: 'working' },
      ],
    },
    { id: 'idle', title: '待機', cards: [] },
    { id: 'other', title: 'その他', cards: [] },
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
  title: 'Sessions',
  isFocused: true,
  bodyColumns: 80,
  placement: 'dock',
  scroll: { offset: 0, bodyRows: 40 },
  view: {},
} as const

test('ボードを描き、選んだセッションへ指示を送る', async ($, on) => {
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
  // バイナリは hooks/lib/daemon-bin.sh と同じ置き場 (XDG_CACHE_HOME が無ければ ~/.cache) を呼ぶ。
  expect(ran[0]).toEqual(['/home/u/.cache/agents-daemon/bin/agents-daemon', 'status', '--board'])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ plugin: 'agents-daemon', surface, component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })
    expect(await ui.find({ type: 'Text', text: /入力待ち \(1\)/ })).toBeDefined()
    expect(await ui.find({ key: 'p2', text: /このセッション/ })).toBeDefined()
    expect(await ui.find({ key: 'p3', text: /セッション不明/ })).toBeDefined()

    // 自分自身を選んでも入力欄は出ない (自分へ送る意味が無い)。
    await ui.press({ key: 'p2' })
    expect(await ui.find({ key: 'instruction' })).toBeUndefined()

    await ui.press({ key: 'p1' })
    await ui.input({ key: 'instruction', text: 'テストを足して' })
    expect(await ui.find({ type: 'Text', text: /peer へ送りました/ })).toBeDefined()
    await ui.unmount()
  }

  // { sessionId } で送った宛先は、engine が session.send の hook へ渡すときに ID の文字列へ直す。
  expect(sent).toEqual([
    { to: 's-peer', text: 'テストを足して' },
    { to: 's-peer', text: 'テストを足して' },
  ])
})

test('daemon が動いていなければそう描く', async ($, on) => {
  world(on, () => output(JSON.stringify({ daemon_running: false, lanes: [] })))

  await $.command.run(OPEN_BOARD)
  const ui = await $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })
  expect(await ui.find({ type: 'Text', text: /agents-daemon が動いていません/ })).toBeDefined()
})

test('board が失敗したら理由を描く', async ($, on) => {
  world(on, () => output('', 1, 'status snapshot のパースに失敗'))

  await $.command.run(OPEN_BOARD)
  const ui = await $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })
  expect(await ui.find({ type: 'Text', text: /status snapshot のパースに失敗/ })).toBeDefined()
})
