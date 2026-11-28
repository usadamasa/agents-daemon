import { atom, read, update } from 'claude-code'
import type { EngineInterface } from 'claude-code'

import { runDaemon } from './daemon-bin'
import { logEvent } from './log'
import type { TabLabelResult } from '../types'

// #722: 共有の rex session にタブとして起きたセッション (spawn のタブ、restarting-session の fork) で、タブ (window) の名前を
// 作業の内容に追わせる。session の名前には触れない (ほかのタブと共有する)。
// - 対象はタブの包み (wrap) が AGENTS_DAEMON_SHARED_REX_SESSION を渡したセッションだけ。rex の場所は mod が持たず、
//   tab-label が Claude Code から継いだ自分の環境から読む (rexplace)
// - main のターンが answer で終わったら数え、最初の 1 ターンの後に 1 回、以後 CHECK_EVERY ターンごとに問う
// - 問うたびに今の名前を tab-label で読み直し、それを基準に $.model.fork で「合っているか、合わないなら新しい名前」を問う。
//   人が付け直した名前も基準にし、合わなくなれば付け直す (利用者の選択)
// - rex を叩くのはバイナリ (tab-label)。mod と hook から多重化ツールを叩かない
// - fork の SessionStart では plugin の hook の出力が context に入らないが、mod は fork の後も新しいバージョンで動く
// - 待つと応答の表示が止まるので、turn.complete は数えるだけにし、session.start で始めた timer が拾う (work-infer と同じ)
// cc-premise: 2.1.330 mods $.model.fork (isAnswered、text) と $.env.get の API で書いた (turn.complete は hooks/register.ts)

// CHECK_EVERY は 2 回目以降に問う間隔 (main の answer の数)。作業の切り替わりに数ターンで追い、fork を毎ターンは起こさない。
const CHECK_EVERY = 4
// LABEL_LIMIT は付ける名前の長さ (文字)。モデルは頼んだ字数を守らない (英単語の混ざる題で 33 文字を返した) ので、
// 断らずにここで切る。バイナリ (tablabel) の上限と同じ。
const LABEL_LIMIT = 64
// POLL_MS は数を見に行く間隔。読むのは $.state だけなので安い。
const POLL_MS = 20_000

// pending は前に問うてから数えた main の answer。checked はこのセッションで 1 回でも問うたか。
// atom は読み書きする module ごとに書く (state の scan が import を辿らない)。
const pending = atom({ plugin: 'agents-daemon', key: 'tabPending' } as const, 0)
const checked = atom({ plugin: 'agents-daemon', key: 'tabChecked' } as const, false)

// inflight は問うている途中か (同じ process の timer が重ねて起こさないため)。
let inflight = false

// startTabLabel は session.start で timer を始める (session.start の hook は hooks/register.ts が持つ)。
export function startTabLabel($: EngineInterface) {
  $.clock.every(POLL_MS, () => {
    void tick($)
  })
}

// countTabLabel は main のターンが answer で終わったときに数える (turn.complete の hook は hooks/register.ts が持つ)。
export async function countTabLabel($: EngineInterface) {
  await update($, pending, n => n + 1)
}

// sharedTab は共有のタブで起きたセッションか。印はタブの包み (wrap の sharedSessionEnv) が渡す。
async function sharedTab($: EngineInterface): Promise<boolean> {
  return (await $.env.get('AGENTS_DAEMON_SHARED_REX_SESSION')) === '1'
}

// tick は数が足りていれば、今の名前を読み、fork で問い、合わなければ付け直す。失敗はログに残すだけでセッションを止めない。
async function tick($: EngineInterface) {
  if (inflight) return
  const need = (await read($, checked)) ? CHECK_EVERY : 1
  if ((await read($, pending)) < need) return
  if (!(await sharedTab($))) return
  inflight = true
  try {
    await update($, pending, () => 0)
    await update($, checked, () => true)
    const current = await tabLabel($, {})
    const reply = await $.model.fork({ prompt: prompt(current.label) })
    if (!reply.isAnswered) {
      await logEvent($, 'mod.tab.failed', { reason: reply.reason })
      return
    }
    const label = firstLine(reply.text)
    if (label === '' || label.toUpperCase() === 'SAME') return
    const set = await tabLabel($, { label })
    if (set.status === 'renamed') await logEvent($, 'mod.tab.renamed', {})
  } catch (err) {
    await logEvent($, 'mod.tab.failed', { reason: String(err) })
  } finally {
    inflight = false
  }
}

// tabLabel は tab-label を呼ぶ。失敗は投げる。
async function tabLabel($: EngineInterface, req: { label?: string }): Promise<TabLabelResult> {
  const { exitCode, stdout, stderr } = await runDaemon($, 'tab-label', req)
  if (exitCode !== 0) throw new Error(stderr.trim() || `tab-label: exit ${exitCode}`)
  return JSON.parse(stdout) as TabLabelResult
}

function prompt(current: string): string {
  return [
    `この端末のタブの今の名前は「${current}」です。`,
    'この会話でこのセッションが今進めている作業をその名前が表しているなら、SAME とだけ答えてください。',
    '表していなければ、今の作業を表す新しい名前を、日本語 1 行 (英単語と空白も数えて 15 字前後、句点なし。例: ログインのリダイレクト修正) で答えてください。',
    '名前か SAME だけを返し、前置きや説明は付けないでください。考え込まず手早く答えてください。',
  ].join('\n')
}

// firstLine は返事の最初の空でない行から、引用符とバッククォートを外し、空白を詰めて LABEL_LIMIT で切ったものを返す。
// 無ければ空。
function firstLine(text: string): string {
  for (const raw of text.split('\n')) {
    const line = raw.replace(/[`"'「」『』]/g, '').trim().split(/\s+/).join(' ')
    if (line !== '') return [...line].slice(0, LABEL_LIMIT).join('').trim()
  }
  return ''
}
