// パーキングのメモの見せ方。hooks/parking.ts のうち engine に依らない部分を移した。

// lastPlacedAt はメモが最後に置かれた時刻 (sources は新しい順)。
export function lastPlacedAt(memo) {
  return memo.sources[0]?.at ?? "";
}

const DAY_MS = 24 * 60 * 60 * 1000;

// isStale はメモが最後に置かれてから days 日より経ったか (now はミリ秒)。park discard の days と同じ境界。
export function isStale(memo, now, days) {
  const ms = Date.parse(lastPlacedAt(memo));
  return !Number.isNaN(ms) && now - ms > days * DAY_MS;
}

// kindLabel はメモの種類の語 (park tool の kind の語のまま)。終了したセッションから移したメモ (origin adopted) は
// adopted を添える。
export function kindLabel(memo) {
  return memo.origin === "adopted" ? `${memo.kind}・adopted` : memo.kind;
}

const JST_OFFSET_MS = 9 * 60 * 60 * 1000;

// stamp は RFC 3339 の時刻を JST の「MM/DD HH:MM」にする。記録の offset にもアプリの時刻帯にも頼らない。
export function stamp(at) {
  const ms = Date.parse(at);
  if (Number.isNaN(ms)) return at;
  const jst = new Date(ms + JST_OFFSET_MS);
  const two = (n) => String(n).padStart(2, "0");
  return `${two(jst.getUTCMonth() + 1)}/${two(jst.getUTCDate())} ${two(jst.getUTCHours())}:${two(jst.getUTCMinutes())}`;
}
