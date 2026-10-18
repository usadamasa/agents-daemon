import type { EngineInterface } from 'claude-code'

import type { BoardCard, BoardData, ParkedMemo, ParkingCard, ParkingSource } from '../types'
import { daemonBin } from './daemon-bin'
import { logEvent } from './log'

// パーキングのメモの見せ方と、ボード (#121 の段 6) からの操作。行と列の描き方には依存させず、ボードの形が
// 変わっても (#134) そのまま使えるようにする。描くのは hooks/board.tsx と hooks/band.tsx。

export const KIND_LABEL: Record<ParkingCard['kind'], string> = { aside: '脇', scope: '範囲', preference: '好み', task: 'タスク' }

// parkedMemos は `park list` の stdout を band と hint 行に出す形にする。at は最後に置かれた時刻。
export function parkedMemos(stdout: string): ParkedMemo[] {
  const items = JSON.parse(stdout) as { id: string; kind: ParkedMemo['kind']; title: string; sources: { at: string }[] }[]
  return items.map(item => ({ id: item.id, kind: item.kind, title: item.title, at: item.sources.at(-1)?.at ?? '' }))
}

// Destination はメモを渡す先のセッション。
export type Destination = { session_id: string; session: string }

// memoLabel はメモのカード 1 行: 種類、title、置かれた回数 (2 回以上のとき)。
export function memoLabel(memo: ParkingCard): string {
  const count = memo.sources.length > 1 ? ` ×${memo.sources.length}` : ''
  return `[${KIND_LABEL[memo.kind]}] ${memo.title}${count}`
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

// taskDestinations は「タスクにする」の宛先: cards (同じ repo のカード) のうち、生きている自分以外のセッション。
export function taskDestinations(cards: BoardCard[], self: string): Destination[] {
  return uniqueLive(cards, self)
}

// issueDestinations は「issue にするよう頼む」の宛先。私用のボードだけで、同じ repo のセッションを先に、
// ボードに載っているほかの生きているセッションを後に並べる。業務のものは外へ出さない (#121)。
export function issueDestinations(data: BoardData, cards: BoardCard[], self: string): Destination[] {
  if (data.domain !== 'home') return []
  return uniqueLive([...cards, ...data.rows.flatMap(row => row.cards)], self)
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

// canAdopt はタスクのカードをパーキングへ移せるか。終了したセッションの未完了のタスクだけ。
export function canAdopt(card: BoardCard, self: string): boolean {
  return card.kind === 'task' && !card.live && card.column !== 'completed' && !!card.task_id && card.session_id !== self
}

// taskText は「タスクにする」で送る文面。
export function taskText(memo: ParkingCard): string {
  return [
    `パーキングロットのメモ「${memo.title}」(種類: ${KIND_LABEL[memo.kind]}) をタスクにしてください。TaskCreate で作り、今の作業の区切りで進めてください。`,
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

// handOff は text を to へ送り、届いたときだけメモを handed にする。届かなかったメモは open のまま残す。
export async function handOff($: EngineInterface, memo: ParkingCard, to: Destination, text: string): Promise<string> {
  const sent = { via: 'memo', memo: memo.id, to: to.session_id }
  try {
    const result = await $.session.send({ to: { sessionId: to.session_id }, text })
    if (!result.isDelivered) {
      await logEvent($, 'mod.send', { ...sent, delivered: false, reason: result.reason })
      return `${to.session} へ送れませんでした: ${result.reason}`
    }
    await logEvent($, 'mod.send', { ...sent, delivered: true })
  } catch (err) {
    await logEvent($, 'mod.send', { ...sent, delivered: false, reason: String(err) })
    return `${to.session} へ送れませんでした: ${String(err)}`
  }
  const failure = await park($, ['set', memo.id, '--status', 'handed'])
  return failure === '' ? `${to.session} へ送り、メモを渡しました` : `${to.session} へ送りましたが、メモを handed にできませんでした: ${failure}`
}

// adopt は終了したセッションのタスクをパーキングへ移す。
export async function adopt($: EngineInterface, card: BoardCard): Promise<string> {
  const failure = await park($, ['adopt', card.session_id, card.task_id ?? ''])
  return failure === '' ? `タスク「${card.subject}」をパーキングへ移しました` : `パーキングへ移せませんでした: ${failure}`
}

// park は開いたセッションの ID と cwd を添えて `agents-daemon park` を走らせ、失敗の理由を返す (成功なら空)。
// domain はバイナリがこのセッションから決める (ボードと同じ domain のメモとタスクだけを触る)。
async function park($: EngineInterface, args: string[]): Promise<string> {
  try {
    const argv = [await daemonBin($), 'park', ...args, '--session', await $.session.id(), '--cwd', await $.session.cwd()]
    const { exitCode, stderr } = await $.process.run(argv, { timeoutMs: 10000 })
    return exitCode === 0 ? '' : stderr.trim() || `exit ${exitCode}`
  } catch (err) {
    return String(err)
  }
}
