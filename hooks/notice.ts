import { atom, update } from 'claude-code'
import type { EngineInterface, Register, ToolCallResult } from 'claude-code'

import { daemonBin } from './daemon-bin'
import { logChange, logEvent, reason, settle } from './log'

// model がファイルを読む・書いたら、そのファイルを files に持つパーキングのメモを tool の結果の context で model に
// 伝える (#89)。人には出さない (メモの一覧は B の pane が持つ)。
// - メモは tool の結果のたびに `park list --status open --here` (この repo の open のメモ) を読んで選ぶ
// - このセッションだけが置いたメモは model が自分で置いたものなので伝えない
// - 伝えるのは 1 つの context の中で 1 回。選ぶのと told に足すのを 1 回の update で行い、並列の tool の hook が
//   同じメモを 2 回添えないようにする (models/handoff/FileNotice.tla の AtMostOnce)
// - main の会話が compact されたら told を空にし、compact の後に触ったファイルのメモを伝え直す (#273、同じモデルの
//   Informed)。添えたメモが要約に残るかは決まらないので、残らない側に倒す
// - subagent の tool は main の model に届かないので、伝えず told にも足さない
// cc-premise: 2.1.310 mods tool.call の結果の context は model だけが読み、e.agentId は subagent の呼び出しにだけ付く
// cc-premise: 2.1.310 mods session.compact の next(e) は置き換えた会話 (messages) か skip を返す。precompute は何も置き換えず、e.agentId は subagent の compact にだけ付く

const told = atom({ plugin: 'agents-daemon', key: 'told' } as const, [])

// Memo は `park list` が出すメモ (internal/parking の Item) のうち、ここで読む欄。
type Memo = {
  id: string
  repo: string
  kind: string
  title: string
  detail?: string
  files?: string[]
  default_taken?: string
  sources: { session_id: string }[]
}

// repo の下に置く worktree (Claude Code の EnterWorktree の置き場)。
const WORKTREES = '/.claude/worktrees/'

export const registerNotice: Register = on => {
  on('tool.call', { tool: 'Read' }, async ($, e, next) => notice($, e.tool, e.file_path, e.agentId, await next(e)))
  on('tool.call', { tool: 'Edit' }, async ($, e, next) => notice($, e.tool, e.file_path, e.agentId, await next(e)))
  on('tool.call', { tool: 'Write' }, async ($, e, next) => notice($, e.tool, e.file_path, e.agentId, await next(e)))

  on('session.compact', async ($, e, next) => {
    const compacted = await next(e)
    if (compacted.skip === undefined && e.agentId === undefined && e.trigger !== 'precompute') {
      await update($, told, () => [])
    }

    return compacted
  })
}

// notice は tool の結果に path のメモを添える。通知は補助なので、失敗しても tool の結果はそのまま返す。
async function notice<R extends ToolCallResult>($: EngineInterface, tool: string, path: string, agentId: string | undefined, ran: R): Promise<R> {
  if (ran.deny !== undefined || ran.isError || agentId !== undefined) return ran
  try {
    const memos = await claim($, path)
    await settle($, 'notice')
    if (memos.length === 0) return ran
    await logEvent($, 'mod.notice', { tool, memos: memos.map(m => m.id).join(',') })
    return { ...ran, context: [...(ran.context ?? []), describe(path, memos)] }
  } catch (err) {
    await logChange($, 'notice', 'mod.notice.failed', { reason: reason(err, 'park list') })
    return ran
  }
}

// claim は path に関係するメモのうち、まだ伝えていないものを told に足して返す。一覧は update の前に読み、選ぶのと
// 足すのは update の中で行う。
async function claim($: EngineInterface, path: string): Promise<Memo[]> {
  const self = await $.session.id()
  const relevant = (await openMemos($, self)).filter(
    memo => memo.sources.some(s => s.session_id !== self) && (memo.files ?? []).some(f => inRepo(f, memo.repo) === inRepo(path, memo.repo)),
  )
  let fresh: Memo[] = []
  await update($, told, prev => {
    fresh = relevant.filter(memo => !prev.includes(memo.id))
    return fresh.length === 0 ? prev : [...prev, ...fresh.map(memo => memo.id)]
  })
  return fresh
}

// openMemos はこのセッションの repo の open のメモを読む。読めなければ投げる。
async function openMemos($: EngineInterface, self: string): Promise<Memo[]> {
  const argv = [await daemonBin($), 'park', 'list', '--status', 'open', '--here', '--session', self, '--cwd', await $.session.cwd()]
  const { exitCode, stdout, stderr } = await $.process.run(argv, { timeoutMs: 10000 })
  if (exitCode !== 0) throw new Error(stderr.trim() || `park list: exit ${exitCode}`)
  return JSON.parse(stdout) as Memo[]
}

// inRepo はパスを repo からの相対パスにする。repo の下の worktree の中なら worktree からの相対にする。
// repo の外の絶対パスはそのまま返す。
function inRepo(path: string, repo: string): string {
  if (path.startsWith(repo + WORKTREES)) {
    const rest = path.slice(repo.length + WORKTREES.length)
    const slash = rest.indexOf('/')
    return slash < 0 ? '' : rest.slice(slash + 1)
  }
  if (path.startsWith(repo + '/')) return path.slice(repo.length + 1)
  return path.replace(/^(\.\/)+/, '')
}

function describe(path: string, memos: Memo[]): string {
  const lines = [`${path} に関係するパーキングのメモ (ほかのセッションが置いた脇のこと)。今の作業の範囲は変えず、関わるときだけ考慮する。`]
  for (const memo of memos) {
    lines.push(`- [${memo.kind}] ${memo.title} (${memo.id})`)
    if (memo.detail) lines.push(`  ${memo.detail}`)
    if (memo.default_taken) lines.push(`  置いた側の既定: ${memo.default_taken}`)
  }
  return lines.join('\n')
}
