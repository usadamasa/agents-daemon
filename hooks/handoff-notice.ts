import type { EngineInterface, Register, ToolCallResult } from 'claude-code'

import { loadDeps } from './deps-data'
import { daemonBin } from './daemon-bin'
import { Graph, unlinked } from './graph'
import { logChange, logEvent, reason, settle } from './log'

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

function describe(name: string, cwd: string | undefined, bin: string): string {
  return [
    `${name} へ送りましたが、${name} を端点に持つ依存の辺がありません。`,
    `作業を渡していて、${name} がほかの作業 (別の peer の作業、マージ待ちの PR) の後でないと始められないなら、`,
    'agents-daemon:peer-sessions skill の「依存の辺を書く」に従って辺を書いてください。',
    `  ${bin} edge add --before <待つ相手の参照> --before-cwd <その cwd> --after session:${name} --after-cwd ${cwd ?? '<その cwd>'} --note "<何の後か>"`,
    '返事や報告のように依存の無い送信なら、辺は要りません。',
  ].join('\n')
}
