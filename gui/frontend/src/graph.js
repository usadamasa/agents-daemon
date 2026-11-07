// 依存の DAG (`agents-daemon deps` の graph) を key で引けるようにしたもの。hooks/graph.ts の Graph を移した。
// 段、下流の数、原因の鎖はバイナリ (deps/graph、graph/dag) が数えて渡すので、ここはたどるだけにする。

export class Graph {
  /** @type {Map<string, object>} */
  #byKey = new Map();
  /** @type {Map<string, object[]>} */
  #succs = new Map();
  /** @type {Map<string, object[]>} */
  #preds = new Map();

  constructor(data) {
    this.nodes = data.nodes;
    this.links = data.links;
    this.folded = data.folded;
    for (const n of data.nodes) this.#byKey.set(n.key, n);
    for (const l of data.links) {
      this.#succs.set(l.before, [...(this.#succs.get(l.before) ?? []), l]);
      this.#preds.set(l.after, [...(this.#preds.get(l.after) ?? []), l]);
    }
  }

  node(key) {
    return this.#byKey.get(key);
  }

  // view は図に描くノードと辺と段 (key → depth)。完了を隠すときはバイナリが組んだ folded を使い、隠したノードを
  // 通り抜けた辺を kind "folded" (via に通り抜けたノードの key) として足す。段も folded で数え直したものを使う。
  // folded.nodes の無い JSON は、plugin の更新直後にアプリだけが新しく、cache の daemon のバイナリが古い間に届く。
  // そのときは完了を落とし、両端が見える辺だけを描く。
  view(showCompleted) {
    const nodeDepth = (nodes) => new Map(nodes.map((n) => [n.key, n.depth]));
    if (showCompleted) return { nodes: this.nodes, links: this.links, depth: nodeDepth(this.nodes) };
    const folded = this.folded?.nodes === undefined ? null : this.folded;
    const depth = folded === null ? nodeDepth(this.nodes.filter((n) => n.state !== "completed")) : nodeDepth(folded.nodes);
    return {
      depth,
      nodes: this.nodes.filter((n) => depth.has(n.key)),
      links: [
        ...this.links.filter((l) => depth.has(l.before) && depth.has(l.after)),
        ...(folded?.links ?? []).map((l) => ({ ...l, kind: "folded" })),
      ],
    };
  }

  // after は key の下流の直近 (key が済まないと始められないノード)。
  after(key) {
    return (this.#succs.get(key) ?? []).flatMap((l) => this.#byKey.get(l.after) ?? []);
  }

  // waiters は key から出ていく辺 (key が済むのを待っている下流の直近への辺)。
  waiters(key) {
    return this.#succs.get(key) ?? [];
  }

  // settled は key の下流の直近があり、全部完了しているか。渡した作業の報告を受け取った側が済ませた peer で、
  // idle でもこちらの返事は要らない。下流の無いノードはこちらの返事を待っているかもしれないので含めない。
  settled(key) {
    const down = this.after(key);
    return down.length > 0 && down.every((n) => n.state === "completed");
  }

  // blockers は key を止めている辺 (上流の直近)。
  blockers(key) {
    return this.#preds.get(key) ?? [];
  }

  // reach は key から下流へたどれるノードの key (key 自身は含めない)。
  reach(key) {
    return this.#walk(key, (k) => this.after(k).map((n) => n.key));
  }

  // upstream は key から上流へたどれるノードの key (key 自身は含めない)。
  upstream(key) {
    return this.#walk(key, (k) => this.blockers(k).map((l) => l.before));
  }

  #walk(key, next) {
    const seen = new Set();
    const stack = next(key);
    while (stack.length > 0) {
      const k = stack.pop();
      if (k === key || seen.has(k)) continue;
      seen.add(k);
      stack.push(...next(k));
    }
    return seen;
  }
}
