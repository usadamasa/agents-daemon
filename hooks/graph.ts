import type { BoardGraph, GraphLink, GraphNode } from '../types'

// 依存の DAG (#134、`status --board` の graph) の見せ方。記号、数の行、レーンの行の分け方、Add blocker の候補を持つ。
// 段・下流の数・原因の鎖・レーンの行はバイナリ (internal/board、internal/dag) が数えて渡すので、ここは並べるだけにする。
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

// COLOR は記号の色 (テーマの鍵)。無い状態は既定の色。
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
  private readonly byKey = new Map<string, GraphNode>()
  private readonly succs = new Map<string, GraphLink[]>()
  private readonly preds = new Map<string, GraphLink[]>()

  constructor(data: BoardGraph) {
    this.nodes = data.nodes
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
