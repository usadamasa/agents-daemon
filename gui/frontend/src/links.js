// 単体ビューの本文を、素の文字とリンクに切り分ける。本文はセッションが書いた文字列なので、確かな形だけを拾う。
// 相対パスは拡張子か行番号を要求する (and/or、owner/repo を拾わないため)。\w は ASCII だけなので日本語に隣接しても切れる。

const SEG = String.raw`[\w.@+-]*[\w@+-]`;
const LINE = String.raw`:\d+`;
const BEFORE_PATH = String.raw`(?<![\w./:~@#-])`;
const REL_FIRST = String.raw`(?:\.{1,2}|\.?[\w@+-]+)`;
const REL_LAST = String.raw`(?:[\w.@+-]*\.[A-Za-z][A-Za-z0-9]*|${SEG}(?=${LINE}))`;

const PATTERN = new RegExp(
  [
    String.raw`(?<![\w+.-])(?<url>https?:\/\/(?:(?![<>"'\x60])[!-~])+)`,
    String.raw`(?<![\w./#-])(?:(?<kind>pr|issue):)?(?<repo>[\w-]+\/[\w.-]+)#(?<number>[1-9]\d*)(?!\w)`,
    String.raw`(?<![\w/#&.-])#(?<bare>[1-9]\d*)(?!\w)`,
    String.raw`${BEFORE_PATH}(?<abs>(?:\/${SEG}){2,}\/?)(?:${LINE})?`,
    String.raw`${BEFORE_PATH}(?<rel>${REL_FIRST}(?:\/${SEG})*\/${REL_LAST})(?:${LINE})?(?![\w/-])`,
  ].join("|"),
  "g",
);

const OPENING = { ")": "(", "]": "[", "}": "{" };
const count = (s, c) => s.split(c).length - 1;

// trimUrl は URL の末尾から文の記号と、対応の無い閉じ括弧を外す。
function trimUrl(url) {
  let u = url;
  for (;;) {
    const last = u.at(-1);
    const open = OPENING[last];
    if (".,;:!?".includes(last) || (open !== undefined && count(u, last) > count(u, open))) u = u.slice(0, -1);
    else return u;
  }
}

// issue の URL は PR の番号でも GitHub が PR のページへ送るので、pr: と書かれたときだけ pull にする。
const issueUrl = (repo, number, kind) => `https://github.com/${repo}/${kind === "pr" ? "pull" : "issues"}/${number}`;

function linkOf(m, { githubRepo, base }) {
  const g = m.groups;
  if (g.url !== undefined) {
    const url = trimUrl(g.url);
    return { text: url, target: url };
  }
  if (g.repo !== undefined) return { text: m[0], target: issueUrl(g.repo, g.number, g.kind) };
  if (g.bare !== undefined) {
    const repo = githubRepo?.(Number(g.bare));
    return repo ? { text: m[0], target: issueUrl(repo, g.bare) } : undefined;
  }
  if (g.abs !== undefined) return { text: m[0], target: g.abs };
  return base ? { text: m[0], target: `${base}/${g.rel}` } : undefined;
}

const REF = /^(?:pr|issue):(?<repo>[\w-]+\/[\w.-]+)#(?<number>[1-9]\d*)$/;

// refRepo は pr: / issue: の参照の owner/repo。daemon が状態を引けたかに依らず参照の文字列から取る。
const refRepo = (ref) => REF.exec(ref)?.groups.repo;

// edgeNoteRepo は辺の note の #number を補う owner/repo。repos は deps の repos。決め方の性質は models/noterepo が持つ。
// 辺の向きでは選ばない (note の番号は before の参照のことも after の参照のこともある)。番号の合う端点の参照があれば
// その repo。両端が同じ番号で別 repo なら決めない。合わなければ辺が触れる repo が 1 つのときだけそれを使う。
export function edgeNoteRepo(edge, repos, number) {
  const ends = [edge.before, edge.after];
  const matched = new Set(
    ends.flatMap((e) => {
      const m = REF.exec(e.ref);
      return m !== null && Number(m.groups.number) === number ? [m.groups.repo] : [];
    }),
  );
  if (matched.size > 0) return matched.size === 1 ? [...matched][0] : undefined;
  const touched = new Set(ends.flatMap((e) => [refRepo(e.ref), repos.find((r) => r.repo === e.repo)?.github_repo]).filter((r) => r !== undefined));
  // 端点の repo を repos で引けなければ、触れる repo が 1 つかは分からない。
  const known = ends.every((e) => repos.some((r) => r.repo === e.repo && r.github_repo));
  return known && touched.size === 1 ? [...touched][0] : undefined;
}

// textNoteRepo は text の #number を補う owner/repo。text の中の番号の合う pr: / issue: の参照があればその repo
// (別 repo が 2 つ以上なら決めない)。無ければ fallback。参照は segments がリンクにする位置のものだけを数える。
export function textNoteRepo(text, number, fallback) {
  const matched = new Set();
  for (const m of text.matchAll(PATTERN)) {
    const g = m.groups;
    if (g.kind !== undefined && Number(g.number) === number) matched.add(g.repo);
  }
  if (matched.size === 0) return fallback;
  return matched.size === 1 ? [...matched][0] : undefined;
}

// segments は text を { text } (素の文字) と { text, target } (リンク。target は URL か絶対パス) の並びにする。
// ctx.githubRepo は #123 の番号を受けて補う owner/repo を返す関数、ctx.base は相対パスの基準。無いか owner/repo が
// 分からなければ、その形は素の文字のまま。
export function segments(text, ctx) {
  const out = [];
  const re = new RegExp(PATTERN);
  let at = 0;
  const plain = (end) => {
    if (end > at) out.push({ text: text.slice(at, end) });
  };
  for (let m = re.exec(text); m !== null; m = re.exec(text)) {
    const link = linkOf(m, ctx);
    if (link === undefined) continue;
    plain(m.index);
    out.push(link);
    at = m.index + link.text.length;
    re.lastIndex = at;
  }
  plain(text.length);
  return out;
}
