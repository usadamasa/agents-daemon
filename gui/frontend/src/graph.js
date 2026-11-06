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
    for (const n of data.nodes) this.#byKey.set(n.key, n);
    for (const l of data.links) {
      this.#succs.set(l.before, [...(this.#succs.get(l.before) ?? []), l]);
      this.#preds.set(l.after, [...(this.#preds.get(l.after) ?? []), l]);
    }
  }

  node(key) {
    return this.#byKey.get(key);
  }

  // after は key の下流の直近 (key が済まないと始められないノード)。
  after(key) {
    return (this.#succs.get(key) ?? []).flatMap((l) => this.#byKey.get(l.after) ?? []);
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
