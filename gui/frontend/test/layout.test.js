import { test } from "node:test";
import assert from "node:assert/strict";
import { layout } from "../src/layout.js";

// session の枠 1 つに window の区切りを 6 つ並べた図。区切りとノードと辺の並び、段は、2 本の辺が左右から同じ区切りへ
// 入って交差した実データの形を写した (dagre の並びは入力の順で変わるので、順も写す)。区切り H の中で、右の区切り W
// から来る辺を受ける lane1 と、左の区切り P から来る辺を受ける lane3 が同じ段に並ぶ。lone は lane2 を辺の無い
// 最上段の箱にした形 (O から lane2 への辺が無い)。
function crossedTree({ lone = false } = {}) {
  const top = "\0s";
  const windows = ["P", "W", "S", "O", "H", "G"];
  const frames = [{ key: top, label: "session", level: "h2" }, ...windows.map((w) => ({ key: `${top}\0${w}`, label: w, level: "h3", parent: top }))];
  const members = [
    ["p0", 0, "P"],
    ["w0", 0, "W"],
    ["p1", 1, "P"],
    ["s0", 0, "S"],
    ["wPR", 1, "W"],
    ["s1", 1, "S"],
    ["o0", 0, "O"],
    ["sPR", 2, "S"],
    ["pPR", 2, "P"],
    ["lane1", 2, "H"],
    ["lane3", 2, "H"],
    ["lane2", lone ? 0 : 1, "H"],
    ["g0", 0, "G"],
  ];
  const links = [
    ["wPR", "lane1"],
    ["s1", "sPR"],
    ["o0", "lane2"],
    ["w0", "wPR"],
    ["p1", "pPR"],
    ["p1", "lane3"],
    ["s0", "s1"],
    ["p0", "p1"],
  ]
    .filter(([, after]) => !(lone && after === "lane2"))
    .map(([before, after]) => ({ before, after, kind: "edge" }));
  return {
    tree: { nodes: members.map(([key]) => ({ key })), frames, parentOf: new Map(members.map(([key, , w]) => [key, `${top}\0${w}`])) },
    links,
    depth: new Map(members.map(([key, d]) => [key, d])),
  };
}

// 2 本の線は同じ高さの水平線で重なって交わるので、点列の線分の交差では判定できない。受ける側の左右の順で確かめる。
for (const lone of [false, true]) {
  test(`layout は同じ区切りの隣どうしのノードを、入ってくる辺が交差しない順に並べる (lone=${lone})`, () => {
    const { tree, links, depth } = crossedTree({ lone });
    const { pos } = layout(tree, links, depth);
    assert.ok(pos.get("p1").x < pos.get("wPR").x, `p1=${pos.get("p1").x} wPR=${pos.get("wPR").x}`);
    assert.ok(pos.get("lane3").x < pos.get("lane1").x, `lane3=${pos.get("lane3").x} lane1=${pos.get("lane1").x}`);
  });
}

// session の枠 1 つに window の区切りを 4 つ並べた図。区切りとノードと辺の並び、段は実データの写し。gh の a から
// 隣の段の uc の u へ辺が出る。dagre の並べ替えは交差の数だけを見るので、交差の数が同じなら uc を gh から最も遠い
// 端へ置き、a→u の線が間の 2 つの区切りの上を横切る。
function farTree() {
  const top = "\0s";
  const windows = ["gh", "uc", "dry", "har"];
  const frames = [{ key: top, label: "session", level: "h2" }, ...windows.map((w) => ({ key: `${top}\0${w}`, label: w, level: "h3", parent: top }))];
  const members = [
    ["a", 0, "gh"],
    ["u", 1, "uc"],
    ["t5", 2, "dry"],
    ["s2", 3, "dry"],
    ["p688", 4, "dry"],
    ["p54", 1, "gh"],
    ["p55", 2, "uc"],
    ["t6", 4, "har"],
    ["t7", 2, "har"],
    ["h", 0, "har"],
  ];
  const links = [
    ["s2", "p688"],
    ["s2", "t6"],
    ["a", "p54"],
    ["a", "u"],
    ["a", "t6"],
    ["u", "p55"],
    ["u", "t5"],
    ["u", "t7"],
    ["t5", "s2"],
    ["u", "s2"],
  ].map(([before, after]) => ({ before, after, kind: "edge" }));
  return {
    tree: { nodes: members.map(([key]) => ({ key })), frames, parentOf: new Map(members.map(([key, , w]) => [key, `${top}\0${w}`])) },
    links,
    depth: new Map(members.map(([key, d]) => [key, d])),
    frameOf: (key) => `${top}\0${members.find(([k]) => k === key)[2]}`,
    windows: windows.map((w) => `${top}\0${w}`),
  };
}

test("layout は辺でつながる隣の段の区切りどうしの間に、ほかの区切りを挟まない", () => {
  const { tree, links, depth, frameOf, windows } = farTree();
  const { boxes } = layout(tree, links, depth);
  const span = (key) => {
    const b = boxes.get(key);
    return { left: b.x - b.width / 2, right: b.x + b.width / 2 };
  };
  const [from, to] = [span(frameOf("a")), span(frameOf("u"))].sort((p, q) => p.left - q.left);
  const between = windows.filter((w) => {
    const s = span(w);
    return s.left >= from.right && s.right <= to.left;
  });
  assert.deepEqual(between, [], `gh=${JSON.stringify(span(frameOf("a")))} uc=${JSON.stringify(span(frameOf("u")))}`);
});
