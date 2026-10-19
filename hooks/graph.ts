import type { BoardGraph, GraphLink, GraphNode } from '../types'

// 依存の DAG (#134、`status --board` の graph) の見せ方。記号、節の根、木の行、Add blocker の候補を持つ。
// 段・下流の数・原因の鎖はバイナリ (internal/board、internal/dag) が数えて渡すので、ここは並べるだけにする。
// 描くのは hooks/board.tsx (B の pane) と hooks/band.tsx (A の band)。

// 記号は絵文字を避け、端末の等幅で 1 桁に収まるもの。語は OSC 7501 の state / kind と Task 系ツールの status。
export const ICON: Record<GraphNode['state'], string> = {
  working: '▸',
  done: '↵',
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
    const seen = new Set<string>()
    const stack = this.after(key).map(n => n.key)
    while (stack.length > 0) {
      const k = stack.pop() as string
      if (k === key || seen.has(k)) continue
      seen.add(k)
      stack.push(...this.after(k).map(n => n.key))
    }
    return seen
  }
}

export type Section = {
  id: 'blocked' | 'working' | 'pending' | 'exited'
  icon: string
  label: string
  color?: string
  roots: GraphNode[]
}

// sections は木の節と根。順は ■ blocked → ▸ working → ○ pending → ⊘ exited で、根は受け取った順 (下流の多い順)。
// - ■: 止まっているノードと待ちの輪のノード。輪は既に根にしたノードの下流にいるものを根にしない
// - ▸: working と done
// - ○: 止まっているノードの下流にいない pending のうち、上流の無いもの (置き場のノード、辺の無い未着手)
// - ⊘: exited
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
  return [
    { id: 'blocked' as const, icon: STOP, label: 'blocked', color: 'warning', roots: blocked },
    { id: 'working' as const, icon: ICON.working, label: 'working', color: COLOR.working, roots: pick(n => n.state === 'working' || n.state === 'done') },
    {
      id: 'pending' as const,
      icon: ICON.pending,
      label: 'pending',
      roots: pick(n => n.state === 'pending' && !n.deadlock && n.blocked_by.length === 0 && !covered.has(n.key)),
    },
    { id: 'exited' as const, icon: ICON.exited, label: 'exited', roots: pick(n => n.state === 'exited') },
  ].filter(s => s.roots.length > 0)
}

// behind は止まっているノード (■ の根) の下流にいるノードの数 (根は数えない)。
export function behind(g: Graph, blocked: GraphNode[]): number {
  const roots = new Set(blocked.map(n => n.key))
  const seen = new Set<string>()
  for (const n of blocked) for (const k of g.reach(n.key)) if (!roots.has(k)) seen.add(k)
  return seen.size
}

// summary は数の 1 行: 止まっている根、その下流、進行中、完了、メモ。
export function summary(g: Graph, memos: number): string {
  const blocked = sections(g).find(s => s.id === 'blocked')?.roots ?? []
  const count = (test: (n: GraphNode) => boolean) => g.nodes.filter(test).length
  return [
    `${STOP} ${blocked.length}`,
    `${ICON.pending} ${behind(g, blocked)}`,
    `${ICON.working} ${count(n => n.state === 'working' || n.state === 'done')}`,
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

// tags は行の末尾の印: 根の下流の数、途中で置いたメモの数、待ちの輪。
export function tags(node: GraphNode, isRoot: boolean): string {
  const parts: string[] = []
  if (isRoot && node.downstream > 0) parts.push(`↓${node.downstream}`)
  if (node.parked > 0) parts.push(`${MEMO}${node.parked}`)
  if (node.deadlock) parts.push(CYCLE)
  return parts.length === 0 ? '' : ` ${parts.join(' ')}`
}

// chain は原因の鎖 (blocked_by) を記号と title でつないだもの。
export function chain(g: Graph, node: GraphNode): string {
  const parts = node.blocked_by.flatMap(k => {
    const n = g.node(k)
    return n === undefined ? [] : [`${ICON[n.state]} ${n.title}`]
  })
  return parts.length === 0 ? 'none' : parts.join(' ← ')
}
