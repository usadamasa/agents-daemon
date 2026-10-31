import type { EngineInterface, Register, ToolCallResult } from 'claude-code'

import { loadDeps } from './deps-data'
import { daemonBin } from './daemon-bin'
import { Graph, unlinked } from './graph'
import { logChange, logEvent, reason, settle } from './log'
import type { EdgeAddRequest } from '../types'

// model が SendMessage で渡した宛先に依存の辺が無ければ、その tool の結果の context で辺の書き方を model に伝える (#172)。
// 渡す側が辺を書くのは agents-daemon:peer-sessions skill の手順で、書き忘れに気付けるのは送った model だけなので、
// 人ではなく model に伝える。
// - 宛先は依存図の生きている済んでいないセッションで、`edge add` の辺がどの端点にも無いもの (hooks/graph.ts の unlinked)
// - 送信がエラーになったもの、文面の無いもの (notify_when_idle だけ、shutdown などの要求)、subagent の送信は見ない
// - mod 自身の送信 ($.session.send) は tool.call を通らないので、ここには来ない
// cc-premise: 2.1.310 mods SendMessage の tool.call は e.to (ListAgents の名前、同名なら末尾に ` [ref]`) と e.message を持ち、結果の context は model だけが読む
// cc-premise: 2.1.310 mods 届かなかった SendMessage は isError の結果になるとみなす (型は result を unknown とし、確かめていない)

export const registerHandoffNotice: Register = on => {
  on('tool.call', { tool: 'SendMessage' }, async ($, e, next) => {
    const sent = await next(e)
    if (sent.deny !== undefined || sent.isError || e.agentId !== undefined) return sent
    if (typeof e.to !== 'string' || typeof e.message !== 'string' || e.message === '') return sent

    return nudge($, recipient(e.to), sent)
  })
}

// recipient は SendMessage の宛先からセッション名を取る。同名が複数あるときに付く末尾の ` [ref]` を落とす。
function recipient(to: string): string {
  return to.replace(/\s+\[[^\]]*\]$/, '').trim()
}

// nudge は name に辺が無ければ、辺の書き方を sent の context に添える。伝えるのは補助なので、依存図を読めなくても
// 送信の結果はそのまま返す。
async function nudge<R extends ToolCallResult>($: EngineInterface, name: string, sent: R): Promise<R> {
  try {
    const loaded = await loadDeps($)
    if ('failure' in loaded) {
      await logChange($, 'handoff', 'mod.handoff.failed', { reason: loaded.failure })
      return sent
    }
    await settle($, 'handoff')
    const g = new Graph(loaded.data.graph)
    if (unlinked(g, [name]).length === 0) return sent

    const peer = g.nodes.find(n => n.session_name === name && n.live && n.state !== 'completed')
    await logEvent($, 'mod.handoff.unlinked', { session: peer?.session_id ?? '' })
    return { ...sent, context: [...(sent.context ?? []), describe(name, peer?.cwd, await daemonBin($))] }
  } catch (err) {
    await logChange($, 'handoff', 'mod.handoff.failed', { reason: reason(err, 'handoff') })
    return sent
  }
}

// describe は辺の書き方を両方の向きで示す。peer の端点は session:<名前> で、cwd は依存図のノードから埋める。自分の
// タスクの端点は task 欄の id だけで、参照と cwd はバイナリが埋める (mod は自分のどのタスクかを知らない)。
function describe(name: string, cwd: string | undefined, bin: string): string {
  const peer = { ref: `session:${name}`, cwd: cwd ?? '<その cwd>' }
  const peerWaits: EdgeAddRequest = { before: { ref: '<待つ相手の参照>', cwd: '<その cwd>' }, after: peer, note: '<何の後か>' }
  const selfWaits: EdgeAddRequest = { before: peer, after: { task: '<自分のタスクの id>' }, note: '<何の後か>' }
  const line = (req: EdgeAddRequest) => `  printf '%s' '${JSON.stringify(req)}' | ${bin} edge add`
  return [
    `${name} へ送りましたが、${name} を端点に持つ依存の辺がありません。`,
    'agents-daemon:peer-sessions skill の「依存の辺を書く」に従って、当たる向きの辺を書いてください。',
    `作業を渡していて、${name} がほかの作業 (別の peer の作業、マージ待ちの PR) の後でないと始められないなら:`,
    line(peerWaits),
    `自分のタスクが ${name} の完了を待つなら (自分のタスクは id だけで指し、session_id と cwd はバイナリが埋める):`,
    line(selfWaits),
    '返事や報告のように依存の無い送信なら、辺は要りません。',
  ].join('\n')
}
