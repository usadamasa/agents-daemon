import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import { runDaemon } from './daemon-bin'
import { logEvent } from './log'
import type { Work } from '../types'

// companion (#368): セッションの作業の記録 (worklog) に推定の題を足す。hook が書く題は最初の prompt の 1 行目で、
// 渡された依頼文の冒頭や「続けて」のような短い prompt だと読みにくいので、ターンを重ねたセッションの会話全体から
// 1 行の題を推定し、`work set` の inferred に書く (subject は上書きしない。人が直した題があればアプリはそちらを見せる)。
//
// - 推定は `$.model.fork` (セッションの会話をそのまま prompt cache から読む tool 無しの問い) で、別の process から transcript を
//   読み直すより安く、セッション自身の権限の文脈で動く。effort は指定せず、prompt で手早く答えるよう頼む (effort を変えると
//   per-message effort の無い model では会話全体の cache を書き直す)
// - 起こすのは main のターンが answer で終わった後、記録の turns が INFER_AFTER 以上で推定の題がまだ無いときに 1 回。
//   失敗 (API error、nothing-to-fork、空の返事) はやり直せるが、セッションの中で MAX_ATTEMPTS 回まで (InferBounded)
// - turn.complete の hook の中で待つと応答の表示が止まるので、hook は印 (due) を置くだけにし、session.start で始めた timer が
//   拾って推定する (dispatch をまたぐ仕事は session.start の timer で、という reference の作法)
// cc-premise: 2.1.319 mods turn.complete (reason、agentId) と $.model.fork (isAnswered、text) と $.clock.every の API で書いた

// INFER_AFTER は推定を起こすのに要る記録の turns。2 ターン以下は中継や返事だけのセッションが多く、prompt の題で足りる。
const INFER_AFTER = 3
// MAX_ATTEMPTS はセッションの中で推定を起こす回数の上限 (失敗のやり直しを含む)。
const MAX_ATTEMPTS = 2
// POLL_MS は印を見に行く間隔。読むのは $.state だけなので安い。
const POLL_MS = 20_000
// TITLE_LIMIT は推定の題の長さ (文字)。超えたら切る。
const TITLE_LIMIT = 80

const PROMPT = [
  'この会話でこのセッションが進めている作業を、依存図に出す題として日本語 1 行 (40 字以内、句点なし) で答えてください。',
  '題だけを返し、前置きや説明や引用符は付けないでください。考え込まず手早く答えてください。',
].join('\n')

// due は main のターンが終わってから timer がまだ見ていない印。attempts はこのセッションで推定を起こした回数。
// atom は読み書きする module ごとに書く (state の scan が import を辿らない)。
const due = atom({ plugin: 'agents-daemon', key: 'inferDue' } as const, false)
const attempts = atom({ plugin: 'agents-daemon', key: 'inferAttempts' } as const, 0)

// inflight は推定の途中か (同じ process の timer が重ねて起こさないため。reload で始め直す)。
let inflight = false

// startWorkInfer は session.start で timer を始める (session.start の hook は hooks/register.ts が持つ)。
export function startWorkInfer($: EngineInterface) {
  $.clock.every(POLL_MS, () => {
    void tick($)
  })
}

export const registerWorkInfer: Register = on => {
  on('turn.complete', async ($, e, next) => {
    if (e.agentId === undefined && e.reason === 'answer') await update($, due, () => true)
    return next(e)
  })
}

// tick は印があれば記録を読み、条件がそろえば推定して書く。失敗はログに残すだけでセッションを止めない。
async function tick($: EngineInterface) {
  if (inflight || !(await read($, due))) return
  inflight = true
  try {
    await update($, due, () => false)
    if ((await read($, attempts)) >= MAX_ATTEMPTS) return
    const sessionID = await $.session.id()
    const got = await runDaemon($, 'work get', { session_id: sessionID })
    if (got.exitCode !== 0) return
    const rec = JSON.parse(got.stdout) as Work
    if (rec.inferred !== undefined || rec.turns < INFER_AFTER) return
    await update($, attempts, n => n + 1)
    const reply = await $.model.fork({ prompt: PROMPT })
    if (!reply.isAnswered) {
      await logEvent($, 'mod.work.infer.failed', { reason: reply.reason, turns: rec.turns })
      return
    }
    const title = firstLine(reply.text)
    if (title === '') {
      await logEvent($, 'mod.work.infer.failed', { reason: 'empty-title', turns: rec.turns })
      return
    }
    const set = await runDaemon($, 'work set', { session_id: sessionID, inferred: title })
    if (set.exitCode !== 0) {
      await logEvent($, 'mod.work.infer.failed', { reason: set.stderr.trim() || `exit ${set.exitCode}`, turns: rec.turns })
      return
    }
    await logEvent($, 'mod.work.inferred', { turns: rec.turns })
  } catch (err) {
    await logEvent($, 'mod.work.infer.failed', { reason: String(err) })
  } finally {
    inflight = false
  }
}

// firstLine は返事の最初の空でない行を、引用符を外し空白を詰めて TITLE_LIMIT で切ったもの。無ければ空。
function firstLine(text: string): string {
  for (const raw of text.split('\n')) {
    const line = raw
      .replace(/^[\s"'「『]+|[\s"'」』]+$/g, '')
      .split(/\s+/)
      .join(' ')
    if (line === '') continue
    return [...line].slice(0, TITLE_LIMIT).join('')
  }
  return ''
}
