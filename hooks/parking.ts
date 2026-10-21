import type { EngineInterface } from 'claude-code'

import type { BoardCard, BoardData, BoardRow, GraphNode, ParkedMemo, ParkingCard, ParkingSource } from '../types'
import { daemonBin } from './daemon-bin'
import { logEvent, reason } from './log'

// パーキングのメモの見せ方と、ボード (#121 の段 6) からの操作。行と列の描き方には依存させず、ボードの形が
// 変わっても (#134) そのまま使えるようにする。描くのは hooks/board.tsx と hooks/band.tsx。

// parkedMemos は `park list` の stdout を band に出す形にする。at は最後に置かれた時刻 (sources は置いた順)。
export function parkedMemos(stdout: string): ParkedMemo[] {
  const items = JSON.parse(stdout) as { id: string; kind: ParkedMemo['kind']; title: string; sources: { at: string }[] }[]
  return items.map(item => ({ id: item.id, kind: item.kind, title: item.title, at: item.sources.at(-1)?.at ?? '' }))
}

// lastPlacedAt はボードのメモが最後に置かれた時刻 (ボードの sources は新しい順)。
export function lastPlacedAt(memo: ParkingCard): string {
  return memo.sources[0]?.at ?? ''
}

const DAY_MS = 24 * 60 * 60 * 1000

// isStale はメモが最後に置かれてから days 日より経ったか (now はミリ秒)。`park discard --days` と同じ境界。
export function isStale(memo: ParkingCard, now: number, days: number): boolean {
  const ms = Date.parse(lastPlacedAt(memo))
  return !Number.isNaN(ms) && now - ms > days * DAY_MS
}

// Destination はメモを渡す先のセッション。self は開いたセッション自身で、peer メッセージではなく自分の入力へ渡す (#190)。
export type Destination = { session_id: string; session: string; self?: true }

// SELF_LABEL は自セッションの宛先の名前。セッション名はカードに頼らず固定にする (自分が生きているのは自明で、
// カードが無くても宛先に出す)。
const SELF_LABEL = 'このセッション'

// memoLabel はメモのカード 1 行: 種類 (park tool の kind の語のまま)、title、置かれた回数 (2 回以上のとき)。
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
// (architecture.md「時刻の書き方」)。
export function stamp(at: string): string {
  const ms = Date.parse(at)
  if (Number.isNaN(ms)) return at
  const jst = new Date(ms + JST_OFFSET_MS)
  const two = (n: number) => String(n).padStart(2, '0')
  return `${two(jst.getUTCMonth() + 1)}/${two(jst.getUTCDate())} ${two(jst.getUTCHours())}:${two(jst.getUTCMinutes())}`
}

// taskDestinations は「タスクにする」の宛先: メモの行 (row) が開いたセッションの repo (here) なら自セッションを先に、
// 続けて同じ repo の生きているほかのセッション。
export function taskDestinations(data: BoardData, row: BoardRow, self: string): Destination[] {
  const own = row.repo === data.here ? [selfDestination(self)] : []
  return [...own, ...uniqueLive(row.cards, self)]
}

// issueDestinations は「issue にするよう頼む」の宛先。私用のボードだけで、自セッションを先に、同じ repo のセッション、
// ボードに載っているほかの生きているセッションの順に並べる。業務のものは外へ出さない (#121)。
export function issueDestinations(data: BoardData, cards: BoardCard[], self: string): Destination[] {
  if (data.domain !== 'home') return []
  return [selfDestination(self), ...uniqueLive([...cards, ...data.rows.flatMap(row => row.cards)], self)]
}

function selfDestination(self: string): Destination {
  return { session_id: self, session: SELF_LABEL, self: true }
}

function uniqueLive(cards: BoardCard[], self: string): Destination[] {
  const seen = new Set<string>()
  const out: Destination[] = []
  for (const card of cards) {
    if (!card.live || card.session_id === '' || card.session_id === self || seen.has(card.session_id)) continue
    seen.add(card.session_id)
    out.push({ session_id: card.session_id, session: card.session })
  }
  return out
}

// canAdopt はタスクのノードをパーキングへ移せるか。終了したセッションの未完了のタスクだけ。
export function canAdopt(node: GraphNode, self: string): boolean {
  return node.kind === 'task' && node.state === 'exited' && node.session_id !== undefined && node.session_id !== self
}

// taskText は「タスクにする」で送る文面。
export function taskText(memo: ParkingCard): string {
  return [
    `パーキングロットのメモ「${memo.title}」(種類: ${memo.kind}) をタスクにしてください。TaskCreate で作り、今の作業の区切りで進めてください。`,
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

// discard はメモを捨て、結果の文面を返す。
export async function discard($: EngineInterface, memo: ParkingCard): Promise<string> {
  const failure = await park($, ['set', memo.id, '--status', 'discarded'])
  return failure === '' ? `メモ「${memo.title}」を捨てました` : `メモを捨てられませんでした: ${failure}`
}

// discardStale は最後に置かれてから days 日より経った open のメモをまとめて捨て、結果の文面を返す。捨てたメモは
// 7 日残り (internal/parking の ClosedRetention)、id (`logs --board` の park.discard に残る) で戻せる。
export async function discardStale($: EngineInterface, days: number): Promise<string> {
  const ran = await run($, ['discard', '--days', String(days)])
  if ('failure' in ran) return `古いメモを捨てられませんでした: ${ran.failure}`
  let count: number
  try {
    count = (JSON.parse(ran.stdout) as unknown[]).length
  } catch (err) {
    return `古いメモを捨てましたが、結果を読めませんでした: ${reason(err, 'park discard')}`
  }
  return `${days} 日より前のメモ ${count} つを捨てました (戻すには park set <id> --status open、id は logs --board に残る)`
}

// handOff は text を to へ送り、届いたときだけメモを handed にする。届かなかったメモは open のまま残す。
export async function handOff($: EngineInterface, memo: ParkingCard, to: Destination, text: string): Promise<string> {
  const sent = { via: 'memo', memo: memo.id, to: to.session_id }
  const failed = await deliver($, to, text)
  if (failed !== '') {
    await logEvent($, 'mod.send', { ...sent, delivered: false, reason: failed })
    return `${to.session} へ送れませんでした: ${failed}`
  }
  await logEvent($, 'mod.send', { ...sent, delivered: true })
  const failure = await park($, ['set', memo.id, '--status', 'handed'])
  return failure === '' ? `${to.session} へ送り、メモを渡しました` : `${to.session} へ送りましたが、メモを handed にできませんでした: ${failure}`
}

// deliver は text を to へ届け、届かなかった理由を返す (届いたら空)。自セッションへは peer メッセージを送れないので、
// 自分の入力に渡す (`$.prompt.submit`)。入力はセッションが idle になってから 1 ターンとして入り、drop されたら
// 入っていない (`isDelivered` の false と同じ扱い)。
// cc-premise: 2.1.306 mods $.prompt.submit は idle になってから自分のターンとして入り、drop で入らなかったことが分かる
async function deliver($: EngineInterface, to: Destination, text: string): Promise<string> {
  try {
    if (to.self) {
      const result = await $.prompt.submit({ text })
      return result.drop === undefined ? '' : result.drop
    }
    const result = await $.session.send({ to: { sessionId: to.session_id }, text })
    return result.isDelivered ? '' : result.reason
  } catch (err) {
    return String(err)
  }
}

// adopt は終了したセッションのタスクをパーキングへ移す。タスクの ID はノードの Key (<session_id>/<task_id>) から取る。
export async function adopt($: EngineInterface, node: GraphNode): Promise<string> {
  const taskId = node.key.slice(node.key.indexOf('/') + 1)
  const failure = await park($, ['adopt', node.session_id ?? '', taskId])
  return failure === '' ? `タスク「${node.title}」をパーキングへ移しました` : `パーキングへ移せませんでした: ${failure}`
}

// park は開いたセッションの ID と cwd を添えて `agents-daemon park` を走らせ、失敗の理由を返す (成功なら空)。
// domain はバイナリがこのセッションから決める (ボードと同じ domain のメモとタスクだけを触る)。
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
