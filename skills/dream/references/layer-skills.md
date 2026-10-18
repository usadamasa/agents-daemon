# skill レイヤーの手順

対象はグローバル skill (`~/.claude/skills/*/SKILL.md`) とプロジェクト skill (`<repo>/.claude/skills/*/SKILL.md`)。

skill 本体の「手順」を dream で書き換えるのは、下位レイヤーの知見を受け入れるときだけ。
機能の改修そのものは dream の仕事ではない。

思想は SKILL.md の「統合の思想」にある。ここはその適用手順を持つ。

## body への受け入れ

auto memory から統合する判定と手順は [layer-auto-memory](layer-auto-memory.md) が持つ。
skill 側で見るのは、受け入れた記述が既存の節と重複していないか、既存の節を置き換えるべきかの一点。

CLAUDE.md と重複していたら CLAUDE.md 側を skill 名参照へ置き換える
([layer-claude-md](layer-claude-md.md))。skill 側を削らない。

## description の見直し

`description` を **増減の両方向** で見直す。description は全 skill ぶんが毎ターン注入されるため、
索引と同じく lean に保つ。
書き方の規範は [[writing-skills]] に従う (CADDi の skill は [[caddi-skill-manager]])。

dream で特に見るのは **本文の変更が description に染み出していないか** の一点。

| 本文側で起きたこと | description の扱い |
|---|---|
| 機能を足した | 「いつ使うか」の判断が変わらないなら伸ばさない。伸びていれば戻す |
| `references/` へ分離した | 分離した内容の記述を description からも落とす |
| 何も変わっていないが起動されない | ここで初めて「いつ使うか」を**補う** |

## 変更の検証 (Replay と Gate)

skill 本体を書き換える候補 (`kind: skill_body`) は、採用前に挙動の予測比較と採点を通す。
`kind: memory_dedup` と `kind: unhomed` は対象外。

pipeline 上の位置づけは [pipeline](pipeline.md) を参照。

### Replay (挙動予測、subagent 呼び出し)

旧 SKILL.md と新 SKILL.md (Adopt 前の diff を適用した仮想版) の挙動を、代表 prompt で予測比較する。

**副作用ゼロ原則**: 実 tool を再演せず、subagent に「挙動予測だけ」させる。

1. candidates.jsonl から `kind: skill_body` の candidate を全て読む
2. 各 candidate について:
   - `skill_path` (旧) と、Adopt 前 fixture (新) を用意する
   - 対応する harvest の user_prompts から代表 prompt を 1-3 個抽出する
   - [`prompts/replay.md`](../prompts/replay.md) の template を read し、`<REPRESENTATIVE_PROMPT>` /
     `<OLD_SKILL_BODY>` / `<NEW_SKILL_BODY>` を置換する
   - `general-purpose` subagent (Agent tool、または Workflow の `agent()`) に投げる
   - 出力 JSON を `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/replays/<candidate-id>.json` に保存する
3. 失敗 (rate limit / timeout) の候補は drop し、Stage レポートに「未検証」タグを付ける

Workflow tool の実行例は [`prompts/replay.md`](../prompts/replay.md) を参照。

**新 SKILL.md fixture の作り方**: Mine は `proposed_change` の diff 本文を生成しない。
Adopt の前に LLM が diff を書く際、あわせて新版 fixture を `./tmp` に置くのが実装パターン。

### Gate (3-judge parallel majority vote)

Replay の出力を rubric ([gate-rubric](gate-rubric.md)) に照らして採点し、accept / reject / revise を判定する。

1. `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/replays/*.json` を全て読む
2. [gate-rubric](gate-rubric.md) を read する
3. 各 replay について、3 judge を parallel に走らせる (Workflow tool の `parallel()`):
   - [`prompts/judge.md`](../prompts/judge.md) の template を read し、`<RUBRIC_BODY>` /
     `<REPLAY_RESULT_JSON>` / `<JUDGE_NUM>` を置換する
   - `general-purpose` subagent に投げる
   - 各 judge の verdict (accept / reject / revise) を回収する
4. majority vote で決める:
   - accept 2 件以上 → `accept`
   - reject 2 件以上 → `reject`
   - 割れたとき (accept 1 / reject 1 / revise 1 など) → `revise`
5. 最終 verdict を `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/verdicts/<candidate-id>.json` に保存する

Workflow tool の実行例は [`prompts/judge.md`](../prompts/judge.md) を参照。

**Rubric の要点**: [gate-rubric](gate-rubric.md) を single source of truth とする。
FN×3 重視、compliance_score が 4 以上で自動 accept 昇格、token_delta は +20% までを neutral とする設計。
