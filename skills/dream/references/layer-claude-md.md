# CLAUDE.md レイヤーの手順

対象はグローバル CLAUDE.md (`~/.claude/CLAUDE.md`) とプロジェクト CLAUDE.md
(`<repo-root>/CLAUDE.md`、サブディレクトリの CLAUDE.md)。

思想は SKILL.md の「統合の思想」にある。ここはその適用手順を持つ。

## 重複の削り方

- グローバル CLAUDE.md は、下位から重複を削るときの基準として参照する。書き足すのは下位の内容を
  昇格させるときだけ。
- プロジェクト CLAUDE.md は、グローバルと重複していたら自分側を削る。
- skill 本体と重複していたら、CLAUDE.md 側を「skill 名で参照」に置き換える
  (例: 「PR 作成は `commit-push-pr` スキルに従う」)。

## 昇格の条件

auto memory から昇格させるのは **1〜2 行に圧縮できるものだけ**。CLAUDE.md は全文が毎ターン
注入されるため、長い記述を寄せると常時ロードが太る。圧縮できないなら skill 側へ置く
(判定は [layer-auto-memory](layer-auto-memory.md) の行き先テーブル)。

## 剪定

下位から削るだけでは、昇格のぶん CLAUDE.md が単調に増える。昇格させた dream の中で剪定まで行う。

- 昇格させた記述と、CLAUDE.md に元からある記述の重複を削る。昇格は 1〜2 行の追加と既存記述の
  書き換えを同時に行うものとして扱い、追記だけで終わらせない。
- 節が長くなったら skill へ切り出し、CLAUDE.md 側は skill 名での参照 1 行に置き換える
  (行き先が下へ戻る方向の移動も dream の仕事)。切り出しは `writing-skills` skill へ渡す。
- グローバルとプロジェクトの切り分けを見る。特定リポジトリでしか成立しない記述がグローバル側に
  あれば、そのリポジトリの CLAUDE.md / AGENTS.md へ移す。

## 編集先

`~/.claude/CLAUDE.md` は symlink で、実体は agents-config の `dotclaude/CLAUDE-global.md`。
生成物ではなく、このファイル自体が原本なので直接編集する。

## memory への参照を作らない

CLAUDE.md が auto memory のメモリ名を直接指す形は作らない
(例: 「詳細は auto memory の `xxx-rules` を参照」)。memory はロードされる保証がないため、
参照した側が完結しなくなる。参照したくなったのは置き場所を間違えたということなので、
[layer-auto-memory](layer-auto-memory.md) の行き先テーブルで決め直す。

スコープをまたぐ参照も同じ理由で作らない。グローバル CLAUDE.md は、特定のリポジトリの
CLAUDE.md / AGENTS.md や `.claude/skills/` を名指ししない。そのリポジトリで作業していない
セッションでは解決しないため。逆向き (プロジェクト → グローバル) は、グローバルが常にロードされる
ので参照してよい。

skill 名での参照は別扱いで、依存関係として健全。memory への参照と性質が違う点は 3 つ。

- skill は description で明示的にトリガーされ、必要なときにだけロードされる
- auto memory は関連時に想起される個別メモリで、ロード保証がない
- skill のトリガー条件は description に書かれるので、上位から参照しても挙動が予測できる
