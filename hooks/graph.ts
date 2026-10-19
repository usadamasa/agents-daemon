import type { BoardGraph, GraphLink, GraphNode } from '../types'

// 依存の DAG (#134、`status --board` の graph) の見せ方。記号、節の根、木の行、Add blocker の候補を持つ。
// 段・下流の数・原因の鎖はバイナリ (internal/board、internal/dag) が数えて渡すので、ここは並べるだけにする。
// 描くのは hooks/board.tsx (B の pane) と hooks/band.tsx (A の band)。

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

// layers は A の band の列: 段 (前提の深さ) ごとのノード。完了を畳むときは完了を除き、残ったノードの段だけで詰める。
// 列の中は受け取った順 (下流の多い順)。
export function layers(g: Graph, showCompleted: boolean): GraphNode[][] {
  const shown = g.nodes.filter(n => showCompleted || n.state !== 'completed')
  const depths = [...new Set(shown.map(n => n.depth))].sort((a, b) => a - b)
  return depths.map(d => shown.filter(n => n.depth === d))
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

export type Section = {
  id: 'blocked' | 'working' | 'idle' | 'pending' | 'exited'
  icon: string
  label: string
  color?: string
  roots: GraphNode[]
}

// sections は木の節と根。順は ■ blocked → ▸ working → ↵ idle → ○ pending → ⊘ exited で、根は受け取った順 (下流の多い順)。
// - ■: 止まっているノードと待ちの輪のノード。輪は既に根にしたノードの下流にいるものを根にしない
// - ▸: working
// - ↵: done と unknown
// - ○: 止まっているノードの下流にいない pending のうち、上流の無いもの (置き場のノード、辺の無い未着手)
// - ⊘: exited。先の根の木に出たものは根にしない
// completed は根にしない。
export function sections(g: Graph): Section[] {
  const blocked: GraphNode[] = []
  const covered = new Set<string>()
  for (const n of g.nodes) {
    if (!isStuck(n) && !(n.deadlock && n.state !== 'completed')) continue
    if (!isStuck(n) && covered.has(n.key)) continue
    blocked.push(n)
    covered.add(n.key)
    for (const k of g.reach(n.key)) covered.add(k)
  }
  const pick = (test: (n: GraphNode) => boolean) => g.nodes.filter(n => !blocked.includes(n) && test(n))
  // exited は下流も木で出すので、先の根の木に出たノードを根にしない。
  const exited: GraphNode[] = []
  const shown = new Set<string>()
  for (const n of pick(n => n.state === 'exited')) {
    if (shown.has(n.key)) continue
    exited.push(n)
    for (const k of g.reach(n.key)) shown.add(k)
  }
  return [
    { id: 'blocked' as const, icon: STOP, label: 'blocked', color: 'warning', roots: blocked },
    { id: 'working' as const, icon: ICON.working, label: 'working', color: COLOR.working, roots: pick(n => n.state === 'working') },
    { id: 'idle' as const, icon: ICON.done, label: 'idle', roots: pick(isIdle) },
    {
      id: 'pending' as const,
      icon: ICON.pending,
      label: 'pending',
      roots: pick(n => n.state === 'pending' && !n.deadlock && n.blocked_by.length === 0 && !covered.has(n.key)),
    },
    { id: 'exited' as const, icon: ICON.exited, label: 'exited', roots: exited },
  ].filter(s => s.roots.length > 0)
}

// behind は止まっているノード (■ の根) の下流にいるノードの数 (根は数えない)。
export function behind(g: Graph, blocked: GraphNode[]): number {
  const roots = new Set(blocked.map(n => n.key))
  const seen = new Set<string>()
  for (const n of blocked) for (const k of g.reach(n.key)) if (!roots.has(k)) seen.add(k)
  return seen.size
}

// summary は数の 1 行: 止まっている根、その下流、進行中、待っている (分類できないものを含む)、完了、メモ。
export function summary(g: Graph, memos: number): string {
  const blocked = sections(g).find(s => s.id === 'blocked')?.roots ?? []
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

// TreeRow は木の 1 行。node はノード、more は畳んだ枝 (押すと unfold を開く)。
export type TreeRow =
  | { type: 'node'; key: string; prefix: string; node: GraphNode; isRoot: boolean }
  | { type: 'more'; key: string; prefix: string; hidden: number; unfold: string[] }

// treeRows は root から下流を字下げした行。1 段 2 桁 (`├ ` `│ ` `└ `)。maxDepth より深い段は畳むが、unfolded に
// あるノードと、selected を含む枝は畳まない。たどっている途中のノードへ戻る辺 (待ちの輪) はたどらない。
export function treeRows(g: Graph, root: GraphNode, maxDepth: number, unfolded: ReadonlySet<string>, selected: string): TreeRow[] {
  const rows: TreeRow[] = []
  const used = new Map<string, number>()
  const keyOf = (prefix: string, k: string) => {
    const base = `${prefix}:${root.key}:${k}`
    const n = used.get(base) ?? 0
    used.set(base, n + 1)
    return n === 0 ? base : `${base}#${n}`
  }
  rows.push({ type: 'node', key: keyOf('row', root.key), prefix: '', node: root, isRoot: true })
  const walk = (key: string, prefix: string, depth: number, onPath: Set<string>) => {
    const kids = g.after(key).filter(k => !onPath.has(k.key))
    kids.forEach((kid, i) => {
      const last = i === kids.length - 1
      const head = prefix + (last ? '└ ' : '├ ')
      const rest = prefix + (last ? '  ' : '│ ')
      const down = g.reach(kid.key)
      const holdsSel = selected !== '' && (kid.key === selected || down.has(selected))
      if (depth >= maxDepth && !unfolded.has(kid.key) && !holdsSel) {
        rows.push({ type: 'more', key: keyOf('more', kid.key), prefix: head, hidden: kid.downstream + 1, unfold: [kid.key, ...down] })
        return
      }
      rows.push({ type: 'node', key: keyOf('row', kid.key), prefix: head, node: kid, isRoot: false })
      walk(kid.key, rest, depth + 1, new Set([...onPath, kid.key]))
    })
  }
  walk(root.key, '', 1, new Set([root.key]))
  return rows
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

// tags は行の末尾の印: 下流の数 (showDown のとき)、途中で置いたメモの数、待ちの輪。B は根に、A は止まっている
// ノードに下流の数を出す。
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

// fit は text を max 桁に収める。はみ出すときは末尾を「…」にする。折り返すと木の字下げと段の列が崩れるため、
// 行は 1 行に切り詰める。
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
