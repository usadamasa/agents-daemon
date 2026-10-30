import type { On, ProcessRunResult, SessionMessage } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

const BIN = '/home/u/.cache/agents-daemon/bin/agents-daemon'
const CWD = '/src/home/agents-daemon'
const LIST = [BIN, 'park', 'list', '--status', 'open', '--here', '--session', 'SELF', '--cwd', CWD]

// memo は `park list` が出すメモ 1 つ (parking の Item)。by はメモを置いたセッションの ID。
function memo(id: string, title: string, files: string[], by: string[] = ['OTHER'], extra: Record<string, unknown> = {}) {
  return {
    id,
    repo: CWD,
    kind: 'aside',
    origin: 'placed',
    title,
    files,
    sources: by.map(sid => ({ session_id: sid, cwd: CWD, at: '2026-10-25T10:00:00+09:00' })),
    status: 'open',
    ...extra,
  }
}

function output(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

// world は plugin の下の engine の代わり。park list は answers[LIST] を返し、呼ばれた回数を数える。Read / Edit / Write は
// file_path を返すだけにし、failing に入れたパスはエラーにする。compact は空の会話を返し、ran.skip が立っていれば skip する。
function world(on: On, answers: Record<string, unknown>, failing: string[] = []) {
  const ran = { list: 0, skip: false }
  mock.env(on, { HOME: '/home/u' })
  mock.clock(on)
  on('session.id', async () => ({ value: 'SELF' }))
  on('session.cwd', async () => ({ value: CWD }))
  on('process.run', async (_$, e) => {
    const key = e.argv.join(' ')
    if (key === LIST.join(' ')) ran.list++
    const answer = answers[key]
    if (answer instanceof Error) return { value: output('', 1, answer.message) }
    return { value: output(JSON.stringify(answer ?? [])) }
  })
  on('tool.register', async (_$, e) => ({ value: { tool: `mcp__agents-daemon__${e.name}` } }))
  on('command.register', async (_$, e) => ({ value: { command: e.name } }))
  on('session.start', async (_$, e) => ({ cwd: e.cwd }))
  on('session.compact', async () => (ran.skip ? { skip: 'off' } : { messages: SUMMARY }))
  const answer = (path: string) =>
    (failing.includes(path) ? { isError: true, result: 'File does not exist.', text: 'File does not exist.' } : { result: { filePath: path } }) as never
  on('tool.call', { tool: 'Read' }, async (_$, e) => answer(e.file_path))
  on('tool.call', { tool: 'Edit' }, async (_$, e) => answer(e.file_path))
  on('tool.call', { tool: 'Write' }, async (_$, e) => answer(e.file_path))
  return { ran }
}

const START = { cwd: CWD, surface: 'terminal', isInteractive: true } as const

type T = Engine

const touch = ($: T, tool: 'Read' | 'Edit' | 'Write', path: string, extra: Record<string, unknown> = {}) =>
  $.tool.call({ tool, file_path: path, ...extra } as never)

// SUMMARY は compact の後の会話 (要約 1 つ)。compact は少なくとも 1 つの message を残す。
const SUMMARY: SessionMessage[] = [{ role: 'user', text: '要約', toolUses: [] }]

const compact = ($: T, extra: Record<string, unknown> = {}) => $.session.compact({ trigger: 'manual', messages: SUMMARY, ...extra } as never)

// context は結果に添えられた model 向けの文。無ければ空。
const context = (res: { context?: readonly string[] }) => (res.context ?? []).join('\n')

test('別のセッションが置いたメモの files に当たるファイルを読むと、そのメモを結果の context で model に伝える', async ($, on) => {
  world(on, {
    [LIST.join(' ')]: [
      memo('m1', '保持期間の根拠が無い', ['internal/runtime/daemon/housekeeping.go'], ['OTHER'], { detail: '90 日の根拠をコメントに書く', default_taken: 'やらない' }),
      memo('m2', '関係ないメモ', ['README.md']),
    ],
  })
  await $.session.start(START)

  const res = await touch($, 'Read', `${CWD}/internal/runtime/daemon/housekeeping.go`)
  const text = context(res)
  expect(text).toMatch(/パーキングのメモ/)
  expect(text).toMatch(/m1/)
  expect(text).toMatch(/保持期間の根拠が無い/)
  expect(text).toMatch(/90 日の根拠をコメントに書く/)
  expect(text).toMatch(/やらない/)
  expect(text).not.toMatch(/関係ないメモ/)
  // tool の結果そのものは変えない。
  expect(res.result).toEqual({ filePath: `${CWD}/internal/runtime/daemon/housekeeping.go` })
})

test('終了したセッションから移したメモ (origin adopted) は種類に adopted を添えて伝える', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', '残ったタスク', ['a.go'], ['OTHER'], { kind: 'scope', origin: 'adopted' })] })
  await $.session.start(START)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/- \[scope・adopted\] 残ったタスク \(m1\)/)
})

test('メモは tool の結果のたびに park list で読み、依存図の読み直しを待たない', async ($, on) => {
  const w = world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go'])] })
  await $.session.start(START)
  expect(w.ran.list).toBe(0)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
  expect(w.ran.list).toBe(1)
})

test('伝えたメモはセッションの中で 2 度は伝えない。Edit と Write でも同じ', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go']), memo('m2', '二つ目', ['b.go'])] })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
  expect(context(await touch($, 'Edit', `${CWD}/a.go`))).toBe('')
  expect(context(await touch($, 'Write', `${CWD}/b.go`))).toMatch(/m2/)
  expect(context(await touch($, 'Read', `${CWD}/b.go`))).toBe('')
})

test('並列の tool が同じファイルを触っても、メモを伝えるのは 1 回だけ (models/handoff の AtMostOnce)', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go'])] })
  await $.session.start(START)

  const results = await Promise.all([touch($, 'Read', `${CWD}/a.go`), touch($, 'Edit', `${CWD}/a.go`), touch($, 'Read', `${CWD}/a.go`)])
  expect(results.filter(r => /m1/.test(context(r)))).toHaveLength(1)
})

test('このセッションだけが置いたメモは伝えない。ほかのセッションも置いたメモは伝える', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', '自分のメモ', ['a.go'], ['SELF']), memo('m2', '一緒に置いたメモ', ['a.go'], ['SELF', 'OTHER'])] })
  await $.session.start(START)

  const text = context(await touch($, 'Read', `${CWD}/a.go`))
  expect(text).not.toMatch(/m1/)
  expect(text).toMatch(/m2/)
})

test('1 つのファイルに当たるメモは 1 つの context にまとめる', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', '一つ目', ['a.go']), memo('m2', '二つ目', ['a.go', 'b.go'])] })
  await $.session.start(START)

  const res = await touch($, 'Read', `${CWD}/a.go`)
  expect(res.context).toHaveLength(1)
  expect(context(res)).toMatch(/m1[\s\S]*m2/)
})

test('メモの files は repo からの相対パスでも絶対パスでもよく、repo の下の worktree で触っても当たる', async ($, on) => {
  world(on, {
    [LIST.join(' ')]: [
      memo('m1', '相対', ['hooks/notice.ts']),
      memo('m2', '絶対', [`${CWD}/hooks/park.tsx`]),
      memo('m3', 'worktree の絶対', [`${CWD}/.claude/worktrees/other/hooks/log.ts`]),
      memo('m4', './ 付き', ['./hooks/graph.ts']),
    ],
  })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/.claude/worktrees/mine/hooks/notice.ts`))).toMatch(/m1/)
  expect(context(await touch($, 'Read', `${CWD}/hooks/park.tsx`))).toMatch(/m2/)
  expect(context(await touch($, 'Read', `${CWD}/hooks/log.ts`))).toMatch(/m3/)
  expect(context(await touch($, 'Read', `${CWD}/hooks/graph.ts`))).toMatch(/m4/)
  // 名前の末尾が同じだけの別のファイルには当たらない。
  expect(context(await touch($, 'Read', `${CWD}/other/hooks/notice.ts`))).toBe('')
})

test('subagent の tool では伝えず、伝えたことにもしない。main の model が触ったときに伝える', async ($, on) => {
  const w = world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go'])] })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`, { agentId: 'agent-1' }))).toBe('')
  // subagent の tool では park list も呼ばない。
  expect(w.ran.list).toBe(0)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
})

test('tool がエラーになったら伝えず、伝えたことにもしない', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go'])] }, [`${CWD}/a.go`])
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
})

test('park list が失敗したら tool の結果をそのまま返す', async ($, on) => {
  world(on, { [LIST.join(' ')]: new Error('state を読めない') })
  await $.session.start(START)

  const res = await touch($, 'Read', `${CWD}/a.go`)
  expect(context(res)).toBe('')
  expect(res.result).toEqual({ filePath: `${CWD}/a.go` })
})

test('後から置かれたメモは、次に触ったときに伝える (models/handoff の NoneMissed)', async ($, on) => {
  const answers: Record<string, unknown> = { [LIST.join(' ')]: [] }
  world(on, answers)
  await $.session.start(START)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')

  answers[LIST.join(' ')] = [memo('m1', '後から', ['a.go'])]
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
})

test('compact の後に触ったファイルのメモは伝え直す (#273、models/handoff の Informed)', async ($, on) => {
  world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go'])] })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
  await compact($)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
})

test('main の会話が置き換わらない compact (precompute、skip、subagent の compact) では伝え直さない', async ($, on) => {
  const w = world(on, { [LIST.join(' ')]: [memo('m1', 'メモ', ['a.go'])] })
  await $.session.start(START)

  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toMatch(/m1/)
  await compact($, { trigger: 'precompute' })
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
  await compact($, { agentId: 'agent-1' })
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
  w.ran.skip = true
  await compact($)
  expect(context(await touch($, 'Read', `${CWD}/a.go`))).toBe('')
})
