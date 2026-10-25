import type { BoardGraph, GraphLink, GraphNode } from '../types'

// 依存の DAG (#134、`status --board` の graph) の見せ方。記号、数の行、レーンの行の分け方、Add blocker の候補を持つ。
// 段・下流の数・原因の鎖・レーンの行はバイナリ (internal/board/graph、internal/dag) が数えて渡すので、ここは並べるだけにする。
// 描くのは hooks/board.tsx (B の pane) と hooks/band.tsx (A の band、数の行だけ)。

// 記号は絵文字を避け、端末の等幅で 1 桁に収まるもの。語は OSC 7501 の state / kind と Task 系ツールの status。
export const ICON: Record<GraphNode['state'], string> = {
  working: '▸',
  done: '↵',
  unknown: '·',
  question: '?',
  permission: '!',
  error: '✗',
  limit: '⧗',
  pending: '○',
  completed: '✓',
  exited: '⊘',
}

// 色 (テーマの鍵) は 1 つの鍵に 1 つの意味だけを持たせ、B の pane と A の band で同じ意味に使う。
//   suggestion 進行中 / warning 人の返事待ち (question、permission) / permission 時間待ち (limit) /
//   error 失敗 / success longest chain の辺の罫線
// ほかの状態、レーンの罫線、メモ (◇) には色を使わない (メモの見出しは太字)。

// COLOR は記号の色。無い状態は既定の色。
export const COLOR: Partial<Record<GraphNode['state'], string>> = {
  working: 'suggestion',
  question: 'warning',
  permission: 'warning',
  limit: 'permission',
  error: 'error',
}

export const STOP = '■'
export const MEMO = '◇'
export const CYCLE = '⟳'

// LEGEND は B の pane の底に置く凡例。数の行の記号、行だけに出る状態、行の末尾の印の 3 群に分ける。語は記号と同じく
// OSC 7501 の state と Task 系ツールの status、それ以外は graph の JSON の欄の名前 (blocked_by、parked、downstream、
// deadlock)。↵ だけは state の done ではなく idle と書く。数の行の ↵ は isIdle (done と unknown) を数え、
// 隣の completed と並ぶと done は「終わった」と読めて、親の確認を待つセッションを見落とすため。
const LEGEND: readonly (readonly string[])[] = [
  [`${STOP} blocked`, `${ICON.pending} pending`, `${ICON.working} working`, `${ICON.done} idle`, `${ICON.completed} completed`, `${MEMO} parked`],
  [`${ICON.question} question`, `${ICON.permission} permission`, `${ICON.limit} limit`, `${ICON.error} error`, `${ICON.exited} exited`, `${ICON.unknown} unknown`],
  ['↓n downstream', `${CYCLE} deadlock`],
]

// LEGEND_GAP は凡例の列の間の桁数。
const LEGEND_GAP = 2

// legendRows は凡例を width 桁に収まる行にしたもの。群ごとに項目を格子で並べ、群の間は width 桁の罫線で分ける。
// 列の幅は全部の群で一番長い項目に揃え、列の数は幅から決める。折り返しに任せると項目の途中や行ごとに見た目が
// 変わるので、行は自分で組む。
export function legendRows(width: number): string[] {
  const cell = Math.max(...LEGEND.flatMap(g => g.map(cells)))
  const columns = Math.max(1, Math.floor((width + LEGEND_GAP) / (cell + LEGEND_GAP)))
  const pad = (text: string) => text + ' '.repeat(cell + LEGEND_GAP - cells(text))
  return LEGEND.flatMap((g, k) => {
    const rows: string[] = k === 0 ? [] : ['─'.repeat(Math.max(0, width))]
    for (let i = 0; i < g.length; i += columns) {
      const line = g.slice(i, i + columns)
      rows.push(line.map((item, j) => (j === line.length - 1 ? item : pad(item))).join(''))
    }
    return rows
  })
}

const STUCK = new Set<GraphNode['state']>(['question', 'permission', 'limit', 'error'])

// isStuck は人の手か時間を待っている、または失敗したノードか。
export function isStuck(node: GraphNode): boolean {
  return STUCK.has(node.state)
}

// isBlocked は止まっているノードか: isStuck か、待ちの輪の中にいる完了していないノード (人が解かないと進まない)。
export function isBlocked(node: GraphNode): boolean {
  return isStuck(node) || (node.deadlock && node.state !== 'completed')
}

// Graph は graph の JSON を Key で引けるようにしたもの。
export class Graph {
  readonly nodes: GraphNode[]
  readonly links: GraphLink[]
  private readonly byKey = new Map<string, GraphNode>()
  private readonly succs = new Map<string, GraphLink[]>()
  private readonly preds = new Map<string, GraphLink[]>()

  constructor(data: BoardGraph) {
    this.nodes = data.nodes
    this.links = data.links
    for (const n of data.nodes) this.byKey.set(n.key, n)
    for (const l of data.links) {
      this.succs.set(l.before, [...(this.succs.get(l.before) ?? []), l])
      this.preds.set(l.after, [...(this.preds.get(l.after) ?? []), l])
    }
  }

  node(key: string): GraphNode | undefined {
    return this.byKey.get(key)
  }

  // after は key の下流の直近 (key が済まないと始められないノード)。
  after(key: string): GraphNode[] {
    return (this.succs.get(key) ?? []).flatMap(l => this.byKey.get(l.after) ?? [])
  }

  // blockers は key を止めている辺 (上流の直近)。
  blockers(key: string): GraphLink[] {
    return this.preds.get(key) ?? []
  }

  // reach は key から下流へたどれるノードの Key (key 自身は含めない)。
  reach(key: string): Set<string> {
    return this.walk(key, k => this.after(k).map(n => n.key))
  }

  // upstream は key から上流へたどれるノードの Key (key 自身は含めない)。
  upstream(key: string): Set<string> {
    return this.walk(key, k => this.blockers(k).map(l => l.before))
  }

  private walk(key: string, next: (k: string) => string[]): Set<string> {
    const seen = new Set<string>()
    const stack = next(key)
    while (stack.length > 0) {
      const k = stack.pop() as string
      if (k === key || seen.has(k)) continue
      seen.add(k)
      stack.push(...next(k))
    }
    return seen
  }
}

// longest は longest chain を記号と title でつないだもの。止まっているノードが無ければ none。
export function longest(g: Graph, keys: string[]): string {
  const parts = keys.flatMap(k => {
    const n = g.node(k)
    return n === undefined ? [] : [`${ICON[n.state]} ${n.title}`]
  })
  return parts.length === 0 ? 'none' : parts.join(' → ')
}

// isIdle はターンを終えて待っているか、状態を分類できない (作業中とは限らない) ノードか。
function isIdle(node: GraphNode): boolean {
  return node.state === 'done' || node.state === 'unknown'
}

// blockedRoots は数の行の ■: 止まっているノードと、待ちの輪のノード。輪は既に数えたノードの下流にいるものを数えない
// (輪の 1 周を 1 つに数える)。受け取った順 (下流の多い順)。
export function blockedRoots(g: Graph): GraphNode[] {
  const blocked: GraphNode[] = []
  const covered = new Set<string>()
  for (const n of g.nodes) {
    if (!isBlocked(n)) continue
    if (!isStuck(n) && covered.has(n.key)) continue
    blocked.push(n)
    covered.add(n.key)
    for (const k of g.reach(n.key)) covered.add(k)
  }
  return blocked
}

// behind は止まっているノード (■) の下流にいるノードの数 (■ は数えない)。
export function behind(g: Graph, blocked: GraphNode[]): number {
  const roots = new Set(blocked.map(n => n.key))
  const seen = new Set<string>()
  for (const n of blocked) for (const k of g.reach(n.key)) if (!roots.has(k)) seen.add(k)
  return seen.size
}

// summary は数の 1 行: 止まっているノード、その下流、進行中、待っている (分類できないものを含む)、完了、メモ。
export function summary(g: Graph, memos: number): string {
  const blocked = blockedRoots(g)
  const count = (test: (n: GraphNode) => boolean) => g.nodes.filter(test).length
  return [
    `${STOP} ${blocked.length}`,
    `${ICON.pending} ${behind(g, blocked)}`,
    `${ICON.working} ${count(n => n.state === 'working')}`,
    `${ICON.done} ${count(isIdle)}`,
    `${ICON.completed} ${count(n => n.state === 'completed')}`,
    `${MEMO} ${memos}`,
  ].join('  ')
}

// CHAIN_COLOR は longest chain の辺の罫線の色。止まっているノードの記号 (warning など) と別の色にし、
// 「止まっている」と「先に解く道筋」を色で取り違えないようにする。
export const CHAIN_COLOR = 'success'

export type Piece = { text: string; marked: boolean }

// pieces は lanes の [from, to) を印 (marks、internal/dag の Row.Marks) の有無で切った区間。罫線は 1 桁 1 文字
// (BMP) なので、文字列の位置がそのまま桁になる。区間が無くても 1 つ返す (描く Text の数を行で揃える)。
export function pieces(lanes: string, marks: readonly number[] | undefined, from: number, to: number): Piece[] {
  const marked = new Set(marks ?? [])
  const out: Piece[] = []
  for (let i = from; i < to; i++) {
    const m = marked.has(i)
    const tail = out.at(-1)
    if (tail !== undefined && tail.marked === m) tail.text += lanes[i]
    else out.push({ text: lanes[i], marked: m })
  }
  return out.length === 0 ? [{ text: '', marked: false }] : out
}

// splitLanes はレーンの行の罫線を、ノードの記号の左右に分ける。`*` がノードの位置 (internal/dag の Rows)。
// `*` が無い行 (辺をつなぐだけの行) は全部が left。
export function splitLanes(lanes: string): { left: string; right: string } {
  const at = lanes.indexOf('*')
  if (at < 0) return { left: lanes, right: '' }
  return { left: lanes.slice(0, at), right: lanes.slice(at + 1) }
}

// candidates は selected の Add blocker の候補: 辺の端点にできる (ref のある) ノードのうち、自分、既にある上流、
// 下流 (足すと輪になる)、完了と終了したものを除く。
export function candidates(g: Graph, selected: GraphNode): GraphNode[] {
  const down = g.reach(selected.key)
  const already = new Set(g.blockers(selected.key).map(l => l.before))
  return g.nodes.filter(
    n =>
      n.ref !== undefined &&
      n.ref !== '' &&
      n.key !== selected.key &&
      !already.has(n.key) &&
      !down.has(n.key) &&
      n.state !== 'completed' &&
      n.state !== 'exited',
  )
}

// unlinked は handed (model が SendMessage で送った宛先の名前) のうち、辺の書き忘れを疑うもの: ボードに生きていて
// 済んでいないノードを持つセッションで、`edge add` の辺 (kind edge) がどの端点にも無いもの。辺はノードへ解決された
// 端点 (そのセッションのタスク) と、元の記録の参照 (`session:<名前>`) のどちらで当たってもよい。handed の順を保つ。
// 名前は表示名 (session、同名なら ID の先頭が付く) ではなく session_name (SendMessage の宛先に書く形) で当てる。
export function unlinked(g: Graph, handed: string[]): string[] {
  const sessionOf = (key: string) => g.node(key)?.session_name ?? ''
  return handed.filter(name => {
    const present = g.nodes.some(n => n.session_name === name && n.live && n.state !== 'completed')
    if (!present) return false
    return !g.links.some(
      l =>
        l.kind === 'edge' &&
        (sessionOf(l.before) === name || sessionOf(l.after) === name || l.edge?.before.ref === `session:${name}` || l.edge?.after.ref === `session:${name}`),
    )
  })
}

// tags は行の末尾の印: 下流の数 (showDown のとき)、途中で置いたメモの数、待ちの輪。B は止まっているノードに
// 下流の数を出す。
export function tags(node: GraphNode, showDown: boolean): string {
  const parts: string[] = []
  if (showDown && node.downstream > 0) parts.push(`↓${node.downstream}`)
  if (node.parked > 0) parts.push(`${MEMO}${node.parked}`)
  if (node.deadlock) parts.push(CYCLE)
  return parts.length === 0 ? '' : ` ${parts.join(' ')}`
}

// cells は端末に描いたときの桁数。全角 (CJK、ハングル、全角形) は 2 桁、それ以外は 1 桁と数える。記号 (○ ▸ ⊘) は
// 1 桁に収まるものを選んでいる。
export function cells(text: string): number {
  let n = 0
  for (const ch of text) n += wide(ch.codePointAt(0) ?? 0) ? 2 : 1
  return n
}

function wide(cp: number): boolean {
  return (
    (cp >= 0x1100 && cp <= 0x115f) ||
    (cp >= 0x2e80 && cp <= 0xa4cf) ||
    (cp >= 0xac00 && cp <= 0xd7a3) ||
    (cp >= 0xf900 && cp <= 0xfaff) ||
    (cp >= 0xfe30 && cp <= 0xfe4f) ||
    (cp >= 0xff00 && cp <= 0xff60) ||
    (cp >= 0xffe0 && cp <= 0xffe6)
  )
}

// fit は text を max 桁に収める。はみ出すときは末尾を「…」にする。折り返すとレーンの行が崩れるため、行は 1 行に
// 切り詰める。
export function fit(text: string, max: number): string {
  if (cells(text) <= max) return text
  let out = ''
  let n = 0
  for (const ch of text) {
    const w = wide(ch.codePointAt(0) ?? 0) ? 2 : 1
    if (n + w > max - 1) break
    out += ch
    n += w
  }
  return `${out}…`
}

// chain は原因の鎖 (blocked_by) を記号と title でつないだもの。
export function chain(g: Graph, node: GraphNode): string {
  const parts = node.blocked_by.flatMap(k => {
    const n = g.node(k)
    return n === undefined ? [] : [`${ICON[n.state]} ${n.title}`]
  })
  return parts.length === 0 ? 'none' : parts.join(' ← ')
}
