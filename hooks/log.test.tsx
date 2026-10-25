import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

// mod の出来事を `agents-daemon board-log` でボードの経路のログに書く (hooks/log.ts、internal/boardlog)。

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const LOG = [BIN, 'board-log', '--session', 'SELF', '--']

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// world は engine の代わりを立てる。board-log の呼び出しは logged に event 以降の引数で残し、ほかは run が答える。
// clock は band の読み直し (10 秒ごと) を進めるのに使う。
function world(on: On, run: (argv: readonly string[]) => ProcessRunResult, register?: () => never) {
  const logged: string[][] = []
  mock.env(on, { HOME: '/home/u' })
  const clock = mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    if (e.argv[1] === 'board-log') {
      expect([...e.argv.slice(0, LOG.length)]).toEqual(LOG)
      logged.push([...e.argv.slice(LOG.length)])
      return { value: output('') }
    }
    return { value: run(e.argv) }
  })
  on('ui.open', async () => ({ value: { isPlaced: true } }))
  on('ui.render', { component: 'AbovePrompt' }, async () => ({ type: 'engine', ref: 1 }))
  on('tool.register', async (_$, e) => {
    if (register) register()
    return { value: { tool: `mcp__agents-daemon__${e.name}` } }
  })
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  return { logged, clock }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const
const OPEN_BOARD = { command: 'board', args: '', origin: { kind: 'composer' }, presentation: { isFullscreen: true, columns: 160 } } as const
const PANE_PROPS = { title: 'Tasks', isFocused: true, bodyColumns: 80, placement: 'dock', scroll: { offset: 0, bodyRows: 40 }, view: {} } as const

const mount = ($: Engine) =>
  $.ui.mount({ plugin: 'agents-daemon', surface: 'terminal', component: 'Pane', props: PANE_PROPS, requestId: 'agents-daemon-board' })

// engine は tool.register の失敗を session.start の呼び出し元へ返さず、plugin の session.start の hook を飛ばして
// 画面に一度出すだけにする。理由をログに残し、hook を飛ばす扱いは変えない (登録の後の band は出ない)。
// cc-premise: 2.1.302 mods tool.register が失敗すると engine が session.start の hook を skipped にする (claude plugin test で確認)
test('session.start で park の tool を登録できなかったら、理由をログに残す', async ($, on) => {
  const { logged } = world(
    on,
    () => output('[]'),
    () => {
      throw new Error('Failed to start server. Is port 0 in use?')
    },
  )

  await $.session.start(START)
  expect(logged).toHaveLength(1)
  expect(logged[0]?.[0]).toBe('mod.tool-register.failed')
  expect(logged[0]?.[1]).toMatch(/^reason=.*tool\.register/)
})

const COLUMNS = [{ id: 'pending', title: '未着手' }]
const MEMO = { id: 'm1', kind: 'aside', title: '秘密の題', detail: '秘密の詳細', sources: [{ session_id: 'PEER', session: 'peer', live: true, at: '2026-10-18T10:00:00Z' }] }
const CARD = { key: 'PEER/1', kind: 'task', column: 'pending', subject: '秘密のタスク', session_id: 'PEER', session: 'peer', live: true, updated_at: '2026-10-18T10:00:00Z' }

const NODE = {
  key: 'PEER/1',
  kind: 'task',
  state: 'working',
  title: '秘密のタスク',
  ref: 'task:PEER/1',
  cwd: CWD,
  session_id: 'PEER',
  session: 'peer',
  live: true,
  depth: 0,
  downstream: 0,
  parked: 0,
  memos: [],
  blocked_by: [],
  deadlock: false,
}

function board(memos: unknown[]) {
  return {
    daemon_running: true,
    columns: COLUMNS,
    rows: [{ repo: CWD, title: 'agents-daemon', cards: [CARD], parking: memos }],
    graph: { nodes: [NODE], links: [], longest_chain: [], rows: [{ lanes: '*', node: NODE.key }], rows_with_completed: [{ lanes: '*', node: NODE.key }] },
  }
}

test('ボードを開いたときの数と、変わったとき、status --board の失敗をログに残す。同じ失敗は繰り返さない', async ($, on) => {
  let answer = output('', 1, 'status snapshot のパースに失敗')
  const { logged } = world(on, argv => (argv[1] === 'status' ? answer : output('{}')))
  on('session.send', async () => ({ isDelivered: true as const }))

  await $.command.run(OPEN_BOARD)
  await $.command.run(OPEN_BOARD)
  expect(logged).toEqual([['mod.board.failed', 'reason=status snapshot のパースに失敗']])

  answer = output(JSON.stringify(board([MEMO])))
  await $.command.run(OPEN_BOARD)
  expect(logged.at(-1)).toEqual(['mod.board.open', 'rows=1', 'cards=1', 'memos=1', 'daemon_running=true'])

  // メモを捨てると読み直す。数が変わったときだけ書く。
  answer = output(JSON.stringify(board([])))
  const ui = await mount($)
  await ui.press({ key: 'park:m1' })
  await ui.press({ key: 'discard' })
  expect(logged.at(-1)).toEqual(['mod.board.changed', 'rows=1', 'cards=1', 'memos=0', 'daemon_running=true'])
  const count = logged.length
  await ui.press({ key: 'row:PEER/1' })
  await ui.input({ key: 'instruction', text: '秘密の指示' })
  await ui.unmount()

  // 指示の送信は宛先と届いたかだけ書き、文面と subject は書かない。
  expect(logged.slice(count)).toEqual([['mod.send', 'via=instruction', 'to=PEER', 'delivered=true']])
  expect(JSON.stringify(logged)).not.toMatch(/秘密/)
})

test('メモを渡せなかった理由をログに残す', async ($, on) => {
  const { logged } = world(on, argv => (argv[1] === 'status' ? output(JSON.stringify(board([MEMO]))) : output('{}')))
  on('session.send', async () => ({ isDelivered: false as const, reason: 'not_found' as const }))

  await $.command.run(OPEN_BOARD)
  const ui = await mount($)
  await ui.press({ key: 'park:m1' })
  await ui.press({ key: 'task:PEER' })
  await ui.unmount()
  expect(logged.at(-1)).toEqual(['mod.send', 'via=memo', 'memo=m1', 'to=PEER', 'delivered=false', 'reason=not_found'])
})

test('band のボードとメモを読めなかったら理由をログに残す。JSON の中身は理由に載せない', async ($, on) => {
  let status = output('', 1, 'status snapshot のパースに失敗')
  let list = output('[]')
  const { logged, clock } = world(on, argv => (argv[1] === 'status' ? status : list))
  on('tool.call', { tool: 'TaskUpdate' }, async () => ({ result: { success: true, taskId: '3', updatedFields: ['status'], statusChange: { from: 'in_progress', to: 'completed' } } }) as never)

  await $.session.start(START)
  // 同じ理由が続く間は書き直さない (10 秒ごとの読み直しと、park add の後の読み直し)。
  await clock.advance(10000)
  await $.tool.call({ tool: 'mcp__agents-daemon__park', kind: 'aside', title: '秘密の題' } as never)
  expect(logged).toEqual([['mod.band.failed', 'reason=status snapshot のパースに失敗']])

  status = output(JSON.stringify(board([])))
  list = output('[{"title": "秘密の題"')
  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', status: 'completed' } as never)
  expect(logged.slice(1)).toEqual([['mod.band.failed', 'reason=park list の JSON を読めない']])
})
