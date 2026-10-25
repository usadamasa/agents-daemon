import type { On, ProcessRunResult } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const STATUS = [BIN, 'status', '--board', '--session', 'SELF', '--cwd', CWD]

// card はボードの parking のメモ 1 つ。by はメモを置いたセッションの ID。
function card(id: string, title: string, files: string[], by: string[] = ['OTHER'], extra: Record<string, unknown> = {}) {
  return {
    id,
    kind: 'aside',
    title,
    files,
    sources: by.map(sid => ({ session_id: sid, session: sid.toLowerCase(), live: true, at: '2026-10-25T10:00:00+09:00' })),
    ...extra,
  }
}

function board(parking: unknown[]) {
  return {
    domain: 'home',
    daemon_running: true,
    columns: [],
    here: CWD,
    rows: [{ repo: CWD, title: 'agents-daemon', cards: [], parking }],
    graph: { nodes: [], links: [], longest_chain: [], rows: [], rows_with_completed: [] },
  }
}

function output(stdout: string): ProcessRunResult {
  return { exitCode: 0, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下の engine の代わり。Read / Edit / Write は file_path を返すだけにし、failing に入れたパスは
// エラーにする。
function world(on: On, answers: Record<string, unknown>, failing: string[] = []) {
  mock.env(on, { HOME: '/home/u' })
  const clock = mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => ({ value: output(JSON.stringify(answers[e.argv.join(' ')] ?? [])) }))
  on('ui.render', { component: 'AbovePrompt' }, async () => ({ type: 'engine', ref: 1 }))
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  const answer = (path: string) =>
    (failing.includes(path) ? { isError: true, result: 'File does not exist.', text: 'File does not exist.' } : { result: { filePath: path } }) as never
  on('tool.call', { tool: 'Read' }, async (_$, e) => answer(e.file_path))
  on('tool.call', { tool: 'Edit' }, async (_$, e) => answer(e.file_path))
  on('tool.call', { tool: 'Write' }, async (_$, e) => answer(e.file_path))
  return { clock }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

type T = Engine

const touch = ($: T, tool: 'Read' | 'Edit' | 'Write', path: string, extra: Record<string, unknown> = {}) =>
  $.tool.call({ tool, file_path: path, ...extra } as never)

// context は結果に添えられた model 向けの文。無ければ空。
const context = (res: { context?: readonly string[] }) => (res.context ?? []).join('\n')

test('別のセッションが置いたメモの files に当たるファイルを読むと、そのメモを結果の context で model に伝える', async ($, on) => {
  world(on, {
    [STATUS.join(' ')]: board([
      card('m1', '保持期間の根拠が無い', ['internal/daemon/housekeeping.go'], ['OTHER'], { detail: '90 日の根拠をコメントに書く', default_taken: 'やらない' }),
      card('m2', '関係ないメモ', ['README.md']),
    ]),
  })
  await $.session.start(START)

  const res = await touch($, 'Read', `${CWD}/internal/daemon/housekeeping.go`)
  const text = context(res)
  expect(text).toMatch(/パーキングのメモ/)
  expect(text).toMatch(/m1/)
  expect(text).toMatch(/保持期間の根拠が無い/)
  expect(text).toMatch(/90 日の根拠をコメントに書く/)
  expect(text).toMatch(/やらない/)
  expect(text).not.toMatch(/関係ないメモ/)
  // tool の結果そのものは変えない。
  expect(res.result).toEqual({ filePath: `${CWD}/internal/daemon/housekeeping.go` })
})

test('伝えたメモはセッションの中で 2 度は伝えない。Edit と Write でも同じ', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board([card('m1', 'メモ', ['a.go']), card('m2', '二つ目', ['b.go'])]) })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
  expect(context(await touch($, 'Edit', `${CWD}/a.go`))).toBe('')
  expect(context(await touch($, 'Write', `${CWD}/b.go`))).toMatch(/m2/)
  expect(context(await touch($, 'Read', `${CWD}/b.go`))).toBe('')
})

test('並列の tool が同じファイルを触っても、メモを伝えるのは 1 回だけ (models/handoff の AtMostOnce)', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board([card('m1', 'メモ', ['a.go'])]) })
  await $.session.start(START)

  const results = await Promise.all([touch($, 'Read', `${CWD}/a.go`), touch($, 'Edit', `${CWD}/a.go`), touch($, 'Read', `${CWD}/a.go`)])
  expect(results.filter(r => /m1/.test(context(r)))).toHaveLength(1)
})

test('このセッションだけが置いたメモは伝えない。ほかのセッションも置いたメモは伝える', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board([card('m1', '自分のメモ', ['a.go'], ['SELF']), card('m2', '一緒に置いたメモ', ['a.go'], ['SELF', 'OTHER'])]) })
  await $.session.start(START)

  const text = context(await touch($, 'Read', `${CWD}/a.go`))
  expect(text).not.toMatch(/m1/)
  expect(text).toMatch(/m2/)
})

test('1 つのファイルに当たるメモは 1 つの context にまとめる', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board([card('m1', '一つ目', ['a.go']), card('m2', '二つ目', ['a.go', 'b.go'])]) })
  await $.session.start(START)

  const res = await touch($, 'Read', `${CWD}/a.go`)
  expect(res.context).toHaveLength(1)
  expect(context(res)).toMatch(/m1[\s\S]*m2/)
})

test('メモの files は repo からの相対パスでも絶対パスでもよく、repo の下の worktree で触っても当たる', async ($, on) => {
  world(on, {
    [STATUS.join(' ')]: board([
      card('m1', '相対', ['hooks/band.tsx']),
      card('m2', '絶対', [`${CWD}/hooks/park.tsx`]),
      card('m3', 'worktree の絶対', [`${CWD}/.claude/worktrees/other/hooks/log.ts`]),
      card('m4', './ 付き', ['./hooks/graph.ts']),
    ]),
  })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/.claude/worktrees/mine/hooks/band.tsx`))).toMatch(/m1/)
  expect(context(await touch($, 'Read', `${CWD}/hooks/park.tsx`))).toMatch(/m2/)
  expect(context(await touch($, 'Read', `${CWD}/hooks/log.ts`))).toMatch(/m3/)
  expect(context(await touch($, 'Read', `${CWD}/hooks/graph.ts`))).toMatch(/m4/)
  // 名前の末尾が同じだけの別のファイルには当たらない。
  expect(context(await touch($, 'Read', `${CWD}/other/hooks/band.tsx`))).toBe('')
})

test('subagent の tool では伝えず、伝えたことにもしない。main の model が触ったときに伝える', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board([card('m1', 'メモ', ['a.go'])]) })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`, { agentId: 'agent-1' }))).toBe('')
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
})

test('tool がエラーになったら伝えず、伝えたことにもしない', async ($, on) => {
  world(on, { [STATUS.join(' ')]: board([card('m1', 'メモ', ['a.go'])]) }, [`${CWD}/a.go`])
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
})

test('後から置かれたメモは、読み直した後に触ったときに伝える (models/handoff の NoneMissed)', async ($, on) => {
  const answers: Record<string, unknown> = { [STATUS.join(' ')]: board([]) }
  const w = world(on, answers)
  await $.session.start(START)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')

  answers[STATUS.join(' ')] = board([card('m1', '後から', ['a.go'])])
  await w.clock.advance(10000)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
})
