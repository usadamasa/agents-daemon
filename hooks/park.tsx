import type { EngineInterface, Register } from 'claude-code'

import { reloadForBand } from './board-data'
import { daemonBin } from './daemon-bin'
import { logEvent } from './log'

// パーキングロット (#89) の置く側 (#121 の段 5)。model が呼べる park の tool を登録し、呼ばれたら
// `agents-daemon park add` に JSON を stdin で渡す。domain と repo はバイナリが --cwd と session_id から決める。
// cc-premise: 2.1.302 mods tool.register / tool.call の API で書いた

const TOOL = 'park'

const DESCRIPTION = `作業の途中で見つけた脇のことを、ユーザーに聞かずにメモとしてパーキングロットへ置き、今の作業を続けるための tool。
置いたメモはセッションが終わっても残り、後でボードから取り出される。

置けるのは次の 3 種類だけ (kind):
- aside: 脇で見つけたもの。例「関係ないが、ここにバグがある」
- scope: 範囲の相談。例「ついでに X も直すか」。既定は「やらない」で進める
- preference: 好みの確認。例「命名は A と B のどちらか」。仮に決めて進め、確認だけを置く

次のものは置かずに、ユーザーに聞く:
- 進めるのに要る判断。例「この API の仕様がわからない」。答えが無いと今の作業が正しく終わらないもの
- 危ない操作の確認。例「削除してよいか」「force push してよいか」

default_taken には、置いたメモについて今どうしたか (「やらない」、仮に決めた内容) を書く。`

const INPUT_SCHEMA = {
  type: 'object',
  properties: {
    kind: { type: 'string', enum: ['aside', 'scope', 'preference'], description: 'メモの種類' },
    title: { type: 'string', description: 'メモを 1 行で。同じ repo に同じ title の open のメモがあれば 1 つにまとまる' },
    detail: { type: 'string', description: '後で取り出した人が文脈を復元できる説明' },
    files: { type: 'array', items: { type: 'string' }, description: '関係するファイルのパス' },
    default_taken: { type: 'string', description: '今どうしたか (「やらない」、仮に決めた内容)' },
  },
  required: ['kind', 'title'],
}

// model が書ける欄。session_id は model に書かせず、このセッションの ID を入れる。
const FIELDS = ['kind', 'title', 'detail', 'files', 'default_taken'] as const

type Added = { created: boolean; item: { id: string } }

// startPark は session.start で park の tool を登録する (session.start の hook は hooks/register.ts が持つ)。メモの一覧は
// 続けて走る hooks/band.tsx の startBand が読む。
// 登録の失敗は engine が画面に一度出すだけなので、理由をログに残してから投げ直す (hook を飛ばす扱いは engine に任せる)。
export async function startPark($: EngineInterface) {
  try {
    await $.tool.register({ name: TOOL, description: DESCRIPTION, inputSchema: INPUT_SCHEMA, isDeferred: false })
  } catch (err) {
    await logEvent($, 'mod.tool-register.failed', { reason: String(err) })
    throw err
  }
}

export const registerPark: Register = on => {
  // matcher は validate が読めるよう文字列のまま書く (TOOL と plugin 名から決まる名前)。
  on('tool.call', { tool: 'mcp__agents-daemon__park' }, ($, e) =>
    place($, e as unknown as Record<string, unknown>).catch(err => ({ deny: `メモを置けませんでした: ${String(err)}` })),
  )
}

// place は model が書いた欄にこのセッションの ID を足して park add に渡し、置いた結果を model への文面にする。
// 置いた後はボードを読み直し、band のメモをすぐ変える。読めなかったときは前の一覧が残る (band の読み直しと同じ)。
async function place($: EngineInterface, args: Record<string, unknown>): Promise<{ result: string } | { deny: string }> {
  const req: Record<string, unknown> = {}
  for (const field of FIELDS) {
    if (args[field] !== undefined) req[field] = args[field]
  }
  req.session_id = await $.session.id()

  const argv = [await daemonBin($), 'park', 'add', '--cwd', await $.session.cwd()]
  const { exitCode, stdout, stderr } = await $.process.run(argv, { stdin: JSON.stringify(req), timeoutMs: 10000 })
  await reloadForBand($)
  if (exitCode !== 0) return { deny: `メモを置けませんでした: ${stderr.trim() || `exit ${exitCode}`}` }

  const added = JSON.parse(stdout) as Added
  const proceed = typeof req.default_taken === 'string' && req.default_taken !== '' ? `既定 (${req.default_taken}) で進めてください。` : '既定で進めてください。'
  if (added.created) return { result: `メモを置きました (${added.item.id})。${proceed}` }
  return { result: `既に置かれていたメモ (${added.item.id}) にまとめました。${proceed}` }
}
