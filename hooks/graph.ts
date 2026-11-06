import type { DepsGraph, GraphLink, GraphNode } from '../types'

// 依存の DAG (#134、`agents-daemon deps` の graph) を Key で引く索引と、辺の書き忘れを疑う渡し先。描くのは
// アプリ (gui/) で、mod は hooks/handoff-notice.ts がここを使う。

// Graph は graph の JSON を Key で引けるようにしたもの。
export class Graph {
  readonly nodes: GraphNode[]
  readonly links: GraphLink[]
  private readonly byKey = new Map<string, GraphNode>()

  constructor(data: DepsGraph) {
    this.nodes = data.nodes
    this.links = data.links
    for (const n of data.nodes) this.byKey.set(n.key, n)
  }

  node(key: string): GraphNode | undefined {
    return this.byKey.get(key)
  }
}

// unlinked は handed (model が SendMessage で送った宛先の名前、hooks/handoff-notice.ts) のうち、辺の書き忘れを
// 疑うもの: 依存図に生きていて済んでいないノードを持つセッションで、`edge add` の辺 (kind edge) がどの端点にも
// 無いもの。辺はノードへ解決された端点 (そのセッションのタスク) と、元の記録の参照 (`session:<名前>`) のどちらで
// 当たってもよい。handed の順を保つ。
// 名前は表示名 (session、statusline の題) ではなく session_name (ListAgents の名前、SendMessage の宛先に書く形) で当てる。
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
