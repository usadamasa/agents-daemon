import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'

const HERE = [BIN, 'park', 'list', '--here', '--status', 'open', '--session', 'SELF', '--cwd', CWD]
const DURING = (task: string) => [BIN, 'park', 'list', '--status', 'open', '--task', task, '--session', 'SELF', '--cwd', CWD]

// memo は park list が出すメモ 1 つ。at は sources の最後 (いちばん新しく置かれたとき)。
function memo(id: string, title: string, ...at: string[]) {
  return {
    id,
    domain: 'home',
    repo: CWD,
    kind: 'aside',
    title,
    sources: at.map(a => ({ session_id: 'SELF', cwd: CWD, at: a })),
    status: 'open',
  }
}

function output(stdout: string, exitCode = 0): ProcessRunResult {
  return { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下にいる engine の代わりを立てる。park list の argv を key にした答えを返し、
// TaskUpdate は status を completed にした結果を返す。
function world(on: On, answers: Record<string, unknown[]>) {
  const ran: string[][] = []
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    ran.push([...e.argv])
    return { value: output(JSON.stringify(answers[e.argv.join(' ')] ?? [])) }
  })
  // band を出さないときに engine が描くもの。
  on('ui.render', { component: 'AbovePrompt' }, async () => ({ type: 'engine', ref: 1 }))
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  on('tool.call', { tool: 'TaskUpdate' }, async (_$, e) => {
    const input = e as unknown as { taskId: string; status?: string }
    const statusChange = input.status === undefined ? undefined : { from: 'in_progress', to: input.status }
    return { result: { success: true, taskId: input.taskId, updatedFields: input.status ? ['status'] : ['subject'], statusChange } } as never
  })
  return { ran }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

const BAND_PROPS = {
  hasSurvey: false,
  isWorking: false,
  maxRows: 10,
  bodyColumns: 80,
  scroll: { offset: 0, bodyRows: 9 },
  view: {},
} as const

const mount = ($: Parameters<Parameters<typeof test>[1]>[0], surface: 'terminal' | 'desktop', props: object = BAND_PROPS) =>
  $.ui.mount({ plugin: 'agents-daemon', surface, component: 'AbovePrompt', props: props as typeof BAND_PROPS })

test('session.start で、この repo の open のメモの数と新しいものを band に JST の時刻で出す', async ($, on) => {
  const memos = [
    memo('20261017T000000-aaaa', '古いメモ', '2026-10-17T09:00:00+09:00'),
    memo('20261018T000000-bbbb', '二つ目', '2026-10-18T10:00:00+09:00'),
    memo('20261018T010000-cccc', '三つ目', '2026-10-18T11:00:00+09:00'),
    // 置き直されたメモは最後に置かれた時刻を出す。UTC で書かれていても JST にする。
    memo('20261018T020000-dddd', '置き直したメモ', '2026-10-18T12:00:00+09:00', '2026-10-18T16:28:44Z'),
  ]
  const w = world(on, { [HERE.join(' ')]: memos })
  await $.session.start(START)
  // hint 行の数と band で同じ一覧を使い、session.start で list を 2 度呼ばない。
  expect(w.ran).toEqual([HERE])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await mount($, surface)
    expect(await ui.find({ type: 'Text', text: /この repo のパーキングのメモ 4 件/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /\[aside\] 置き直したメモ 10\/19 01:28/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /三つ目 10\/18 11:00/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /二つ目/ })).toBeDefined()
    // 主なものは新しい 3 件まで。残りは数だけ。
    expect(await ui.find({ type: 'Text', text: /古いメモ/ })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /ほか 1 件/ })).toBeDefined()
    await ui.unmount()
  }
})

test('open のメモが無ければ band を出さない', async ($, on) => {
  world(on, {})
  await $.session.start(START)

  const ui = await mount($, 'terminal')
  expect(await ui.find({ type: 'Text', text: /パーキングのメモ/ })).toBeUndefined()
  await ui.unmount()
})

test('閉じると band を出さない。survey があるときは譲る', async ($, on) => {
  world(on, { [HERE.join(' ')]: [memo('20261018T000000-bbbb', 'メモ', '2026-10-18T10:00:00+09:00')] })
  await $.session.start(START)

  const survey = await mount($, 'terminal', { ...BAND_PROPS, hasSurvey: true })
  expect(await survey.find({ type: 'Text', text: /パーキングのメモ/ })).toBeUndefined()
  await survey.unmount()

  const ui = await mount($, 'terminal')
  await ui.press({ key: 'park-band-close' })
  expect(await ui.find({ type: 'Text', text: /パーキングのメモ/ })).toBeUndefined()
  await ui.unmount()
})

test('TaskUpdate で completed になったタスクの途中で置いたメモを band に出す', async ($, on) => {
  const w = world(on, {
    [DURING('3').join(' ')]: [memo('20261018T000000-bbbb', 'タスク 3 の脇', '2026-10-18T10:00:00+09:00')],
  })
  await $.session.start(START)
  w.ran.length = 0

  // completed 以外の更新では引かない。
  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', subject: 'x' } as never)
  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', status: 'in_progress' } as never)
  expect(w.ran).toEqual([])

  await $.tool.call({ tool: 'TaskUpdate', taskId: '3', status: 'completed' } as never)
  expect(w.ran).toEqual([DURING('3')])

  const ui = await mount($, 'terminal')
  expect(await ui.find({ type: 'Text', text: /タスク #3 の途中で置いたメモ 1 件/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /タスク 3 の脇 10\/18 10:00/ })).toBeDefined()
  await ui.unmount()
})

test('完了したタスクの途中で置いたメモが無ければ、今の band を残す', async ($, on) => {
  world(on, { [HERE.join(' ')]: [memo('20261018T000000-bbbb', 'メモ', '2026-10-18T10:00:00+09:00')] })
  await $.session.start(START)

  await $.tool.call({ tool: 'TaskUpdate', taskId: '4', status: 'completed' } as never)
  const ui = await mount($, 'terminal')
  expect(await ui.find({ type: 'Text', text: /この repo のパーキングのメモ 1 件/ })).toBeDefined()
  await ui.unmount()
})
