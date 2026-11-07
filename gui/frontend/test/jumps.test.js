import { test } from "node:test";
import assert from "node:assert/strict";
import { findJumps, jumpPath } from "../src/jumps.js";

const OPT = { radius: 5, margin: 20 };

test("findJumps は後から描く線に、先に描いた線との交点の飛び越しを付ける", () => {
  const horizontal = [{ x: 0, y: 100 }, { x: 200, y: 100 }];
  const vertical = [{ x: 100, y: 0 }, { x: 100, y: 200 }];
  const got = findJumps([horizontal, vertical], OPT);
  assert.deepEqual(got[0], []);
  assert.deepEqual(got[1], [{ seg: 0, x: 100, y: 100 }]);
});

test("findJumps は折れ線の何本目の線分で交わるかを返し、線分の上の順に並べる", () => {
  const a = [{ x: 0, y: 50 }, { x: 300, y: 50 }];
  const b = [{ x: 0, y: 150 }, { x: 300, y: 150 }];
  // 1 本目の線分で a、b と交わる縦の折れ線。上から下へ進むので a が先。
  const c = [{ x: 0, y: 0 }, { x: 100, y: 0 }, { x: 100, y: 200 }, { x: 150, y: 260 }];
  const got = findJumps([b, a, c], OPT);
  assert.deepEqual(got[2], [
    { seg: 1, x: 100, y: 50 },
    { seg: 1, x: 100, y: 150 },
  ]);
});

test("findJumps はどちらかの線の端 (ノードの縁、矢じり) から margin より近い交点を飛ばす", () => {
  const horizontal = [{ x: 0, y: 100 }, { x: 200, y: 100 }];
  // 縦の線の始点から 10 の所で交わる。
  const nearStart = [{ x: 100, y: 90 }, { x: 100, y: 300 }];
  // 横の線の終点から 10 の所で交わる。
  const nearOtherEnd = [{ x: 190, y: 0 }, { x: 190, y: 300 }];
  const got = findJumps([horizontal, nearStart, nearOtherEnd], OPT);
  assert.deepEqual(got[1], []);
  assert.deepEqual(got[2], []);
});

test("findJumps は端を共有する線や平行な線、線分の折れ目から radius より近い交点に飛び越しを付けない", () => {
  const a = [{ x: 0, y: 0 }, { x: 100, y: 100 }];
  const shared = [{ x: 0, y: 0 }, { x: 100, y: 0 }];
  const parallel = [{ x: 0, y: -100 }, { x: 100, y: 0 }];
  // 折れ目 (53, 50) から 3 の所で a と交わる (前の線分は (50, 50)、後の線分は (53, 53))。弧が折れ目にかかる。
  const bent = [{ x: 0, y: 50 }, { x: 53, y: 50 }, { x: 53, y: 200 }];
  const got = findJumps([a, shared, parallel, bent], OPT);
  assert.deepEqual(got[1], []);
  assert.deepEqual(got[2], []);
  assert.deepEqual(got[3], []);
});

test("findJumps は同じ線分の上で弧が重なるほど近い交点を 1 つにまとめる", () => {
  const v1 = [{ x: 100, y: 0 }, { x: 100, y: 200 }];
  const v2 = [{ x: 106, y: 0 }, { x: 106, y: 200 }];
  const horizontal = [{ x: 0, y: 100 }, { x: 300, y: 100 }];
  const got = findJumps([v1, v2, horizontal], OPT);
  assert.deepEqual(got[2], [{ seg: 0, x: 100, y: 100 }]);
});

test("jumpPath は飛び越しの所で線を切り、半径 radius の弧で跨ぐ", () => {
  const points = [{ x: 0, y: 100 }, { x: 200, y: 100 }, { x: 200, y: 300 }];
  assert.equal(jumpPath(points, [], 5), "M 0.0 100.0 L 200.0 100.0 L 200.0 300.0");
  // 右へ進む線分は上へ膨らむ (時計回り)。
  assert.equal(
    jumpPath(points, [{ seg: 0, x: 80, y: 100 }], 5),
    "M 0.0 100.0 L 75.0 100.0 A 5 5 0 0 1 85.0 100.0 L 200.0 100.0 L 200.0 300.0",
  );
  // 左へ進む線分も上へ膨らむ (反時計回り)。
  assert.equal(
    jumpPath([{ x: 200, y: 0 }, { x: 0, y: 0 }], [{ seg: 0, x: 100, y: 0 }], 5),
    "M 200.0 0.0 L 105.0 0.0 A 5 5 0 0 0 95.0 0.0 L 0.0 0.0",
  );
});
