import type { EngineInterface } from 'claude-code'

import type { DepsData, DepsRepo, DepsSession, GraphNode, ParkingCard, ParkingSource } from '../types'
import { daemonBin } from './daemon-bin'
import { fit } from './graph'
import { logEvent, reason } from './log'

// パーキングのメモの見せ方と、依存図の pane からの操作。DAG の描き方には依存させない。描くのは hooks/deps.tsx。

// lastPlacedAt はメモが最後に置かれた時刻 (sources は新しい順)。
export function lastPlacedAt(memo: ParkingCard): string {
  return memo.sources[0]?.at ?? ''
}

const DAY_MS = 24 * 60 * 60 * 1000

// isStale はメモが最後に置かれてから days 日より経ったか (now はミリ秒)。`park discard --days` と同じ境界。
export function isStale(memo: ParkingCard, now: number, days: number): boolean {
  const ms = Date.parse(lastPlacedAt(memo))
  return !Number.isNaN(ms) && now - ms > days * DAY_MS
}

// Destination はメモを渡す先。peer は生きているほかのセッション (`$.session.send`)、self は開いたセッション自身
// (peer メッセージではなく自分の入力へ渡す。#190)、spawn は repo で新しく起こすセッション (`agents-daemon spawn`。#162)。
// peer の subject はそのセッションの subject (進行中のタスクか今の作業)。自動で付いたセッション名だけでは
// 何をしているセッションか分からないので、宛先の名前に添える (#191)。
export type Destination =
  | { route: 'peer'; session_id: string; session: string; subject: string }
  | { route: 'self'; session_id: string; session: string }
  | { route: 'spawn'; repo: string; title: string }

// destinationKey は宛先の描画の key。セッションは ID、起こす先は repo のパス。
export function destinationKey(to: Destination): string {
  return to.route === 'spawn' ? to.repo : to.session_id
}

// SUBJECT_CELLS は宛先の名前に添える subject の桁数の上限。ボタンが 1 行に収まる長さにする。
const SUBJECT_CELLS = 24

// destinationLabel は宛先の名前。
export function destinationLabel(to: Destination): string {
  if (to.route === 'spawn') return to.title
  if (to.route === 'peer' && to.subject !== '') return `${to.session} (${fit(to.subject, SUBJECT_CELLS)})`
  return to.session
}

// SELF_LABEL は自セッションの宛先の名前。セッション名は sessions に頼らず固定にする (自分が生きているのは自明で、
// sessions に無くても宛先に出す)。
const SELF_LABEL = 'このセッション'

// Intent はメモを渡した先に頼むこと。
export type Intent = 'task' | 'issue'

// memoLabel はメモの 1 行: 種類 (park tool の kind の語のまま)、title、置かれた回数 (2 回以上のとき)。
export function memoLabel(memo: ParkingCard): string {
  const count = memo.sources.length > 1 ? ` ×${memo.sources.length}` : ''
  return `[${memo.kind}] ${memo.title}${count}`
}

// memoLines は選んだメモの詳細の行。sources は受け取った順 (新しい順) に出す。
export function memoLines(memo: ParkingCard): string[] {
  const lines: string[] = []
  if (memo.detail) lines.push(memo.detail)
  if (memo.default_taken) lines.push(`既定: ${memo.default_taken}`)
  if (memo.files?.length) lines.push(`ファイル: ${memo.files.join(', ')}`)
  for (const source of memo.sources) lines.push(sourceLine(source))
  return lines
}

function sourceLine(source: ParkingSource): string {
  return [source.session, stamp(source.at), source.detail].filter(Boolean).join(' ')
}

const JST_OFFSET_MS = 9 * 60 * 60 * 1000

// stamp は RFC 3339 の時刻を JST の「MM/DD HH:MM」にする。記録の offset にも mod の時刻帯にも頼らない
// (parking.md「時刻の書き方」)。
export function stamp(at: string): string {
  const ms = Date.parse(at)
  if (Number.isNaN(ms)) return at
  const jst = new Date(ms + JST_OFFSET_MS)
  const two = (n: number) => String(n).padStart(2, '0')
  return `${two(jst.getUTCMonth() + 1)}/${two(jst.getUTCDate())} ${two(jst.getUTCHours())}:${two(jst.getUTCMinutes())}`
}

// taskDestinations は「タスクにする」の宛先: メモの repo が開いたセッションの repo (here) なら自セッションを先に、
// 続けて同じ repo の生きているほかのセッション。
export function taskDestinations(data: DepsData, repo: DepsRepo, self: string): Destination[] {
  const own = repo.repo === data.here ? [selfDestination(self)] : []
  return [...own, ...uniqueLive(repo.sessions, self)]
}

// issueDestinations は「issue にするよう頼む」の宛先。自セッションを先に、同じ repo のセッション、
// ほかの repo の生きているセッションの順に並べる。
export function issueDestinations(data: DepsData, sessions: DepsSession[], self: string): Destination[] {
  return [selfDestination(self), ...uniqueLive([...sessions, ...data.repos.flatMap(repo => repo.sessions)], self)]
}

function selfDestination(self: string): Destination {
  return { route: 'self', session_id: self, session: SELF_LABEL }
}

// uniqueLive は sessions を受け取った順に宛先にする。自分と、同じセッションの 2 回目は除く (subject は最初のもの)。
function uniqueLive(sessions: DepsSession[], self: string): Destination[] {
  const seen = new Set<string>()
  const out: Destination[] = []
  for (const s of sessions) {
    if (!s.live || s.session_id === '' || s.session_id === self || seen.has(s.session_id)) continue
    seen.add(s.session_id)
    out.push({ route: 'peer', session_id: s.session_id, session: s.session, subject: s.subject })
  }
  return out
}

// spawnDestinations は「新しいセッションを起こして渡す」の repo の候補: メモの repo、開いたセッションの repo (here)、
// ほかの repo の順 (重複は除く)。候補に無い repo は pathDestination で指定する。
export function spawnDestinations(data: DepsData, repo: DepsRepo): Destination[] {
  const seen = new Set<string>()
  const out: Destination[] = []
  const add = (repo: string, title: string) => {
    if (repo === '' || seen.has(repo)) return
    seen.add(repo)
    out.push({ route: 'spawn', repo, title })
  }
  add(repo.repo, repo.title)
  add(data.here, data.repos.find(r => r.repo === data.here)?.title ?? basename(data.here))
  for (const r of data.repos) add(r.repo, r.title)
  return out
}

// pathDestination は入力されたパスを起こす先にする。絶対パスでなければ理由を返す (バイナリも同じ検査をするが、
// 起こす前に手元で弾いて知らせる)。
export function pathDestination(path: string): Destination | { failure: string } {
  const repo = path.trim()
  if (!repo.startsWith('/')) return { failure: `repo は絶対パスで指定してください: ${repo}` }
  return { route: 'spawn', repo, title: basename(repo) }
}

function basename(path: string): string {
  return path.replace(/\/+$/, '').split('/').pop() ?? path
}

// canAdopt はタスクのノードをパーキングへ移せるか。終了したセッションの未完了のタスクだけ。daemon が status.json を
// 書いていない (daemon_running でも updated_at が無い) 依存図は、生きているセッションも exited と描くので出さない。
// 押した後の判定はバイナリが status.json で改めて行う (`park adopt`)。
export function canAdopt(node: GraphNode, self: string, data: DepsData): boolean {
  return data.updated_at !== undefined && node.kind === 'task' && node.state === 'exited' && node.session_id !== undefined && node.session_id !== self
}

// taskText は「タスクにする」で送る文面。着手の時期は受け手の計画に任せる。依存図はタスクの description に
// 書かれた id でメモと照合する (internal/deps/graph)。
export function taskText(memo: ParkingCard): string {
  return [
    `パーキングロットのメモ「${memo.title}」(種類: ${memo.kind}) をタスクにしてください。` +
      `TaskCreate で作り、タスクの description にメモの id (${memo.id}) を入れてください。` +
      `既にこのメモを扱うタスクがあれば、新しく作らずにその description に id を書き足すだけで構いません。` +
      `着手の時期は今の計画 (タスクの依存) に従ってください。`,
    ...context(memo),
  ].join('\n')
}

// issueText は「issue にするよう頼む」で送る文面。
export function issueText(memo: ParkingCard, repo: string): string {
  return [
    `パーキングロットのメモ「${memo.title}」を GitHub の issue にしてください。repo: ${repo}。起票したら URL を返してください。`,
    ...context(memo),
  ].join('\n')
}

// spawnText は新しく起こすセッションの最初のターンの文面。起こした経緯を 1 行置き、続けて peer へ送るものと同じ文面。
// issue はメモの repo ではなく起こした repo を起票先にする (生きているセッションの無い repo へ渡すための経路なので)。
export function spawnText(memo: ParkingCard, to: { repo: string; title: string }, intent: Intent): string {
  const body = intent === 'task' ? taskText(memo) : issueText(memo, to.title)
  return `このメモを渡すため、${to.title} (${to.repo}) で新しいセッションとして起こしました。\n${body}`
}

function context(memo: ParkingCard): string[] {
  const lines: string[] = []
  if (memo.detail) lines.push(`詳細: ${memo.detail}`)
  for (const source of memo.sources) {
    if (source.detail) lines.push(`補足 (${source.session}): ${source.detail}`)
  }
  if (memo.files?.length) lines.push(`関係するファイル: ${memo.files.join(', ')}`)
  if (memo.default_taken) lines.push(`置いたときの既定: ${memo.default_taken}`)
  lines.push(`(メモ ${memo.id})`)
  return lines
}

// discard は open のメモを捨て、結果の文面を返す。ほかのセッションが先に取ったメモは捨てない (`--from open`)。
export async function discard($: EngineInterface, memo: ParkingCard): Promise<string> {
  const failure = await park($, ['set', memo.id, '--status', 'discarded', '--from', 'open'])
  return failure === '' ? `メモ「${memo.title}」を捨てました` : `メモを捨てられませんでした: ${failure}`
}

// discardStale は最後に置かれてから days 日より経った open のメモをまとめて捨て、結果の文面を返す。捨てたメモは
// 7 日残り (internal/parking の ClosedRetention)、id (`logs --events` の park.discard に残る) で戻せる。
export async function discardStale($: EngineInterface, days: number): Promise<string> {
  const ran = await run($, ['discard', '--days', String(days)])
  if ('failure' in ran) return `古いメモを捨てられませんでした: ${ran.failure}`
  let count: number
  try {
    count = (JSON.parse(ran.stdout) as unknown[]).length
  } catch (err) {
    return `古いメモを捨てましたが、結果を読めませんでした: ${reason(err, 'park discard')}`
  }
  return `${days} 日より前のメモ ${count} つを捨てました (戻すには park set <id> --status open、id は logs --events に残る)`
}

// handOff は text を to へ渡す。送る前にメモを取り (open のときだけ handed にする。取れるのは 1 つのセッションだけ)、
// 届かなければ open に戻す。取れなかった (ほかのセッションが先に渡した、捨てた) メモは送らない。
// 手順の交錯で破れないことは models/handoff の Handoff.tla (Claim → Send / Spawn → Revert) が持つ。
export async function handOff($: EngineInterface, memo: ParkingCard, to: Destination, text: string): Promise<string> {
  const sent = { via: 'memo', memo: memo.id, ...(to.route === 'spawn' ? { repo: to.repo } : {}) }
  const claim = await park($, ['set', memo.id, '--status', 'handed', '--from', 'open'])
  if (claim !== '') {
    await logEvent($, 'mod.send', { ...sent, to: '', delivered: false, reason: `claim: ${claim}` })
    return `メモを取れませんでした (ほかのセッションが先に渡したか、捨てたかもしれません): ${claim}`
  }
  const delivered = await deliver($, to, text)
  if ('unknown' in delivered) {
    // 起きたか分からないメモは戻さない。戻すと押し直しで 2 つ目のセッションが起きる。
    await logEvent($, 'mod.send', { ...sent, to: delivered.to, delivered: false, reason: delivered.unknown })
    return `${destinationLabel(to)} で window を作りましたが、claude が前景になりませんでした: ${delivered.unknown}。メモは handed のまま残してあります (起きていなければ park set ${memo.id} --status open で戻す)`
  }
  if ('failure' in delivered) {
    await logEvent($, 'mod.send', { ...sent, to: delivered.to, delivered: false, reason: delivered.failure })
    return `${said(to, false)}: ${delivered.failure}。${await revert($, memo)}`
  }
  await logEvent($, 'mod.send', { ...sent, to: delivered.to, delivered: true })
  return `${said(to, true)}、メモを渡しました`
}

// revert は届かなかったメモを open に戻し、結果の文面を返す。戻せなければ handed のまま残るので、手で戻す形を添える。
async function revert($: EngineInterface, memo: ParkingCard): Promise<string> {
  const failure = await park($, ['set', memo.id, '--status', 'open', '--from', 'handed'])
  return failure === '' ? 'メモは open に戻しました' : `メモを open に戻せませんでした (park set ${memo.id} --status open で戻す): ${failure}`
}

// said は結果の文面の前半。起こす先は「起こし (た)」、セッションは「送り (送った)」。
function said(to: Destination, ok: boolean): string {
  const name = destinationLabel(to)
  if (to.route === 'spawn') return ok ? `${name} で新しいセッションを起こし` : `${name} で新しいセッションを起こせませんでした`
  return ok ? `${name} へ送り` : `${name} へ送れませんでした`
}

// Delivered は届いた先のセッション ID か、届かなかった理由 (failure)、届いたか分からない理由 (unknown。spawn が window を
// 作ったが claude が前景にならなかった。pane は残る)。
type Delivered = { to: string } | { to: string; failure: string } | { to: string; unknown: string }

// deliver は text を to へ届ける。
// - self: 自分へは peer メッセージを送れないので、自分の入力に渡す (`$.prompt.submit`)。入力はセッションが idle に
//   なってから 1 ターンとして入り、drop されたら入っていない (`isDelivered` の false と同じ扱い)
// - spawn: `agents-daemon spawn` に本文を stdin で渡し、repo を cwd にした新しいセッションの最初のターンにする
// cc-premise: 2.1.306 mods $.prompt.submit は idle になってから自分のターンとして入り、drop で入らなかったことが分かる
async function deliver($: EngineInterface, to: Destination, text: string): Promise<Delivered> {
  try {
    switch (to.route) {
      case 'self': {
        const result = await $.prompt.submit({ text })
        return result.drop === undefined ? { to: to.session_id } : { to: to.session_id, failure: result.drop }
      }
      case 'peer': {
        const result = await $.session.send({ to: { sessionId: to.session_id }, text })
        return result.isDelivered ? { to: to.session_id } : { to: to.session_id, failure: result.reason }
      }
      case 'spawn':
        return spawnSession($, to.repo, text)
    }
  } catch (err) {
    return { to: '', failure: String(err) }
  }
}

// spawnSession は repo で新しいセッションを起こし、本文を最初のターンにする。claude が前景になるまでバイナリが待つので、
// park より長い timeout にする。exit 0 で foreground が false なら window はできていて claude が前景にならなかった (unknown)。
async function spawnSession($: EngineInterface, repo: string, text: string): Promise<Delivered> {
  const argv = [await daemonBin($), 'spawn', '--repo', repo]
  const { exitCode, stdout, stderr } = await $.process.run(argv, { stdin: text, timeoutMs: 30000 })
  if (exitCode !== 0) return { to: '', failure: stderr.trim() || `exit ${exitCode}` }
  try {
    const res = JSON.parse(stdout) as { session_id: string; pane_id: string; foreground: boolean; detail?: string }
    return res.foreground ? { to: res.session_id } : { to: res.session_id, unknown: res.detail ?? `pane ${res.pane_id} で claude が前景にならなかった` }
  } catch (err) {
    return { to: '', failure: reason(err, 'spawn') }
  }
}

// adopt は終了したセッションのタスクをパーキングへ移す。タスクの ID はノードの Key (<session_id>/<task_id>) から取る。
export async function adopt($: EngineInterface, node: GraphNode): Promise<string> {
  const taskId = node.key.slice(node.key.indexOf('/') + 1)
  const failure = await park($, ['adopt', node.session_id ?? '', taskId])
  return failure === '' ? `タスク「${node.title}」をパーキングへ移しました` : `パーキングへ移せませんでした: ${failure}`
}

// park は開いたセッションの ID と cwd を添えて `agents-daemon park` を走らせ、失敗の理由を返す (成功なら空)。
// ID は操作したセッションとしてログに残る。
async function park($: EngineInterface, args: string[]): Promise<string> {
  const ran = await run($, args)
  return 'failure' in ran ? ran.failure : ''
}

// run は park と同じ走らせ方で、成功なら stdout を返す。
async function run($: EngineInterface, args: string[]): Promise<{ stdout: string } | { failure: string }> {
  try {
    const argv = [await daemonBin($), 'park', ...args, '--session', await $.session.id(), '--cwd', await $.session.cwd()]
    const { exitCode, stdout, stderr } = await $.process.run(argv, { timeoutMs: 10000 })
    return exitCode === 0 ? { stdout } : { failure: stderr.trim() || `exit ${exitCode}` }
  } catch (err) {
    return { failure: String(err) }
  }
}
