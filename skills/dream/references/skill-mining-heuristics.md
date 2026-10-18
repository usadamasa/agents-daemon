# Skill mining heuristics: dream Phase 2 Mine のヒューリスティクス

dream 7-phase pipeline の Phase 2 Mine (`agents-daemon dream mine`) が候補分類に使う判定基準と閾値。

**用途**: 実装 (agents-daemon の `internal/dream/mine/coverage.go`) が本ファイルの閾値をハードコードして反映している。
本ファイルを spec/実装の single source of truth とする。数値を変更したら
Go 定数 (`tokenMatchThreshold` / `shingleMatchThreshold` と `classify` の分岐) も同時に更新すること。

## 判定 1: 対象 skill の絞り込み

feedback memory の内容を反映するとき、対象 skill を絞り込む。

| 状況 | 判定 |
|------|-----|
| memory 名や description に既存 skill 名 (フル or key) が含まれる | 既存 skill body 変更 (`kind: skill_body`) |
| memory が hook / settings 系 | dream の対象外 (`claude-config-management` 等の下位 skill が担当) |
| 上記いずれにも該当しない | 行き先未定 (`kind: unhomed`) |

`findAssociatedSkill` (`coverage.go`) は語境界つきの一致 (`containsAsToken`) で最長の skill 名を返す。
二段目の match では、hyphen prefix (`daily-`, `caddi-`, `weekly-`) を除いた key でも一致を試みる。

**既知の限界 (name-based の false negative)**: 名前の一致は memory 名と skill 名の綴りが違うと
domain overlap を拾えない。たとえば memory 名 `background-job-tmp-denied` と skill 名 `sandbox-troubleshooting` は、
同じ domain (sandbox の書き込み拒否) を扱っていても名前が一致しない。このため CLI は association 判定に失敗する。

この取りこぼしを skip せず `kind: unhomed` として出すのが `generateCandidates` の設計。
skip すると stage レポートに 1 行も現れず、行き先を決める機会ごと消えて memory が残り続ける。
unhomed は coverage 判定を持たない (`confidence: unknown`) ため、
[layer-auto-memory](layer-auto-memory.md) の手動 pass が topic keyword ベースで行き先を埋める。
`agents-daemon dream adopt` は unhomed のまま呼ばれると拒否する。

## 判定 2: memory と skill body の grep coverage 閾値

`agents-daemon dream mine` の中核は、memory の主要フレーズが skill body に既に inline されているかの判定にある。
substring/token/shingle の match で coverage ratio を求め、その値で分類する。

**閾値** (`ClassifyCoverage` in `coverage.go`)。部分的な inline を積極的に「未完了 migration」として拾うため、`skill_body` 側は緩めに設定する。

| coverage ratio | 分類 | Confidence |
|---------------|------|-----------|
| ≥ 95% | `memory_dedup` | high (削除して安全) |
| ≥ 80% | `memory_dedup` | medium (要確認) |
| ≤ 15% | `skill_body` | high (migration 確度高い) |
| ≤ 40% | `skill_body` | medium (部分的に inline、要判断) |
| その他 (40-80%) | `skill_body` | low (人間判断) |

**Coverage 計算の 3 段判定** (`computeCoverage`):

1. phrase 全体の substring match (完全一致)
2. token match: 空白・句読点・記号で分割し、3 rune 以上の token の 60%+ が hit
3. n-gram shingle match: 4-rune shingle 集合の 40%+ が hit

閾値は shingle match の対称性 (Jaccard 相当) を意識しつつ、
日本語の paraphrasing 感度で default 60% (token) と 40% (shingle) に設定した。

## 判定 3: MEMORY.md 索引と本体の整合性

「MEMORY.md に索引があるのに memory 本体が古い」パターンとその逆の検出は、判定 2 の `memory_dedup` 分類で捕捉する。
判定 2 で coverage >= 95% と分類された memory は、そのまま MEMORY.md の索引ごと削除候補になる。
索引の除去そのものは、Adopt phase の後にユーザーが手動で行う。
