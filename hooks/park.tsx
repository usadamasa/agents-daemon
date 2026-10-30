import type { EngineInterface, Register } from 'claude-code'

import { runDaemon } from './daemon-bin'
import { logEvent } from './log'
import { definitions } from './protocol'
import type { Memo, ParkAdded } from '../types'

// パーキングロット (#89) の置く側 (#121 の段 5)。model が呼べる park の tool を登録し、呼ばれたら
// `agents-daemon park add` に JSON を stdin で渡す。tool の入力の schema は protocol の Memo。repo はバイナリが cwd から決める。
// cc-premise: 2.1.302 mods tool.register / tool.call の API で書いた

const TOOL = 'park'

const DESCRIPTION = `作業の途中で見つけた脇のことを、ユーザーに聞かずにメモとしてパーキングロットへ置き、今の作業を続けるための tool。
置いたメモはセッションが終わっても残り、後で依存図の pane から取り出される。

置けるのは次の 3 種類だけ (kind):
- aside: 脇で見つけたもの。例「関係ないが、ここにバグがある」
- scope: 範囲の相談。例「ついでに X も直すか」。既定は「やらない」で進める
- preference: 好みの確認。例「命名は A と B のどちらか」。仮に決めて進め、確認だけを置く

次のものは置かずに、ユーザーに聞く:
- 進めるのに要る判断。例「この API の仕様がわからない」。答えが無いと今の作業が正しく終わらないもの
- 危ない操作の確認。例「削除してよいか」「force push してよいか」

default_taken には、置いたメモについて今どうしたか (「やらない」、仮に決めた内容) を書く。`

// model が書ける欄 (schema の Memo の欄)。session_id は model に書かせず、このセッションの ID を入れる。
const FIELDS = Object.keys(definitions.Memo.properties) as (keyof Memo)[]

// startPark は session.start で park の tool を登録する (session.start の hook は hooks/register.ts が持つ)。
// 登録の失敗は engine が画面に一度出すだけなので、理由をログに残してから投げ直す (hook を飛ばす扱いは engine に任せる)。
export async function startPark($: EngineInterface) {
  try {
    await $.tool.register({ name: TOOL, description: DESCRIPTION, inputSchema: definitions.Memo, isDeferred: false })
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
// 置いたメモは、開いている pane の次の読み直しで出る。
async function place($: EngineInterface, args: Record<string, unknown>): Promise<{ result: string } | { deny: string }> {
  const memo: Record<string, unknown> = {}
  for (const field of FIELDS) {
    if (args[field] !== undefined) memo[field] = args[field]
  }
  // 欄の形はバイナリが schema で確かめる。
  const req = { session_id: await $.session.id(), cwd: await $.session.cwd(), memo: memo as Memo }

  const { exitCode, stdout, stderr } = await runDaemon($, 'park add', req)
  if (exitCode !== 0) return { deny: `メモを置けませんでした: ${stderr.trim() || `exit ${exitCode}`}` }

  const added = JSON.parse(stdout) as ParkAdded
  const proceed = req.memo.default_taken ? `既定 (${req.memo.default_taken}) で進めてください。` : '既定で進めてください。'
  if (added.created) return { result: `メモを置きました (${added.item.id})。${proceed}` }
  return { result: `既に置かれていたメモ (${added.item.id}) にまとめました。${proceed}` }
}
