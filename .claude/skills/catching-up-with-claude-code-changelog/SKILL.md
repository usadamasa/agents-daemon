---
name: catching-up-with-claude-code-changelog
description: >-
  Claude Code (`claude` CLI) が更新されたあと、agents-daemon が Claude Code に置いている前提を CHANGELOG・バイナリ・
  公式 docs と突き合わせて追従するときに使う。daemon の機能が本体の新機能 (idle compaction、auto-continue、
  retry watchdog など) と重なる・衝突する、`internal/detect` が拾う画面の文言が変わった、statusline や hook の
  新しい field で daemon を簡単にできる、本体の新機能を daemon の新機能のアイデアにしたい、といった場面。
  「Claude Code の更新で daemon が陳腐化していないか」「新しい版でできるようになったことは」「CHANGELOG を
  追って」「文言が変わっていないか」で引く。
---

# Claude Code の CHANGELOG に追従する

agents-daemon は Claude Code の「文書化されていない挙動」(画面の文言、hook の発火条件、statusline の stdin、
OSC 7501) の上に建っている。Claude Code が更新されたら、この repo が置いている前提を 3 つの一次資料と突き合わせ、
本体に入った新機能を daemon の機能のアイデア元としても読む。

| 資料 | 何が分かるか | 読み方 |
| ---- | ---- | ---- |
| CHANGELOG (GitHub raw) | 版ごとの追加・変更。flag 付きの新機能もここに載る | 範囲の版を全行読む。要約で済ませない |
| install 済みバイナリ | 画面に出る文言、設定キー、env 名、flag 名、既定値 | `rg -a -o -N` で前後の文脈ごと抜く |
| 公式 docs | hook の input field、statusline の field、設定の説明 | WebFetch を `offset` で分けて読む |

着地は「見つけたものを issue にするかどうか」。レポートを書いてから、項目ごとに issue にする・PR にする・
レポートに残すだけ、を利用者と決める。挙動が変わる対処をその場で実装しない (既存の挙動を変える変更は利用者の判断)。

## 手順

### 1. 範囲を決める

```sh
ls -lt ~/.local/share/claude/versions/
claude --version
```

mtime が install 日。前回のレポート (`./tmp/claude-code-drift/<YYYY-MM-DD>.md`) の範囲の次の版から今の版まで。
前回が無ければ利用者の指定 (「直近 1 週間」など) の期間に install された版。

### 2. CHANGELOG を落として範囲を読む

```sh
mkdir -p ./tmp/claude-code-drift
curl -fsSL https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md \
  -o ./tmp/claude-code-drift/CHANGELOG.md
rg -n '^## ' ./tmp/claude-code-drift/CHANGELOG.md | head -20
```

`## <version>` の行番号で範囲を切り、Read で全行読む (1 版で 200 行になることがあるので `offset` / `limit` で分ける)。
WebFetch は 100K 文字で切れて要約を返すので使わない。`code.claude.com/docs/en/whats-new/2026-wNN.md` は
index に載る週しか存在せず、404 が普通。

読みながら 2 つの目で拾う。

- **前提に当たるか**: 下の「前提の洗い出し」で出した版の引用と検索語に当たる項目
- **アイデアになるか**: daemon が pane 越しにやっていることを本体が直接できるようになった項目 (hook の新 event や
  新 field、statusline の新 field、新しい設定キー、OSC、`claude` のサブコマンド)。daemon の役目 (上限からの再開、
  compact の前後、cache の失効、多重化ツールへの状態の報告) に関わるものを候補にする

該当しない項目 (mods、cloud sessions、Slack、Code Review、VS Code) は飛ばしてよい。

### 3. バイナリで文言と既定値を突き合わせる

CHANGELOG に該当が無くても行う。文言の変更は CHANGELOG に載らない。
`internal/detect` の正規表現と test fixture (`native_continue_test.go` のバナー一覧、`real_sample_test.go` の実機の行、
`detect_test.go` の transient) が、今のバイナリの文言を拾うかを見る。

```sh
B=~/.local/share/claude/versions/<version>
rg -a -o -N '.{0,80}(Usage limit|hit your .{3,30} limit|press enter to|Automatic continue).{0,80}' "$B" | sort -u
rg -a -o -N '.{0,60}[Cc]ompacted.{0,80}' "$B" | sort -u
rg -a -o -N '.{0,160}(<CHANGELOG で見た設定キーや env 名>).{0,200}' "$B" | sort -u
```

- 新しい文言が出たら、`internal/detect/detect.go` のパターンに実際に当ててみる。確かめ方は fixture を一時的に足して
  `go test ./internal/detect/` を回し、結果を見たら戻す。fixture を残すかは別の変更としてレポートに書く。
  レポートには「当たる / 当たらない」まで書く。
- 新機能の既定値と flag 名 (`tengu_*`) はバイナリから読めるが、**flag の有効・無効は手元では確かめられない**
  (`~/.claude/statsig/` のキャッシュは key がハッシュ)。「on paper」と「動いている」を分けて書く。
- zsh で `echo ===X===` は `=cmd` 展開に当たる。区切りは `printf '%s\n' '--- X ---'`。

### 4. docs で field と発火条件を突き合わせる

これも CHANGELOG の該当の有無によらず行う。offset は目安で、返ってきた要約に目当ての節の見出しが無ければ
100000 ずらして読み直す。

| ページ | 見る節 | 突き合わせる先 |
| ---- | ---- | ---- |
| `https://code.claude.com/docs/en/hooks.md` (27 万文字。PostCompact は offset 200000 付近、UserPromptSubmit は 100000 付近) | PostCompact / PreCompact の matcher と input、UserPromptSubmit の順序、Stop の注意 | `hooks/*.sh`、`compact.md`、`architecture.md` [4] [5] |
| `https://code.claude.com/docs/en/statusline.md` | 「When the status line updates」、JSON の field 表、`rate_limits` と `prompt_cache` の節 | `internal/sessionstate`、`architecture.md` [1] |
| `https://code.claude.com/docs/en/settings-reference.md`、`env-vars.md` | compaction、usage limit、retry、prompt cache の設定キー | `rate-limit.md`、`compact.md`、`cache.md` |

### 5. レポートを書く

`./tmp/claude-code-drift/<YYYY-MM-DD>.md`。節は次の順。空の節も見出しだけ残す (「確認したが無かった」が伝わる)。

1. 競合・陳腐化のおそれ: 本体の新機能が daemon の経路と重なるもの。どの段 (1〜3 段目、ゲート、hook) に当たるか、
   flag 待ちか、window の大きさなど条件、対処の選択肢
2. 取り込める新機能 (アイデア): 本体の新 field・新 event・新コマンドで daemon の役目を直接果たせるもの、
   daemon に足せる機能。何を置き換えるか、何が簡単になるかを 1〜2 行
3. 前提が古くなっていた: 今回の範囲より前からの変化で、references の記述が成り立たないもの。別件として分ける
4. 文言の確認: 表で「バイナリの文言 / detect / 判定」。変更不要も明示する (書かないと未確認に読める)
5. docs の直し: references の記述と docs の食い違い。コードは変わらないもの
6. 確認して変更不要と判断したもの: 1 行ずつ根拠つき
7. 手順上の落とし穴: 次回の自分へ

### 6. issue にするか決める

レポートの要点を利用者へ伝え、1〜3 の各項目を次のどれにするか決めてもらう。

| 行き先 | 対象 | やること |
| ---- | ---- | ---- |
| issue | 挙動が変わる対処、設計の変更、新機能のアイデア | 本文を `./tmp` に Write し `gh issue create --body-file`。背景 (CHANGELOG の引用、バイナリの既定値)、検討すること、検証項目、レポートのパスを入れる |
| PR | docs の直し (5) | `docs/` ブランチで直して Draft PR。issue 番号があれば本文で指す |
| レポートのみ | 変更不要 (4、6)、flag 待ちで様子見のもの | 何もしない。次回のレポートが参照する |

issue と PR の本文は日本語。既存の issue と重ならないか `gh issue list` で見てから立てる。

## 前提の洗い出し

前提の一覧を手で持たない。Claude Code の版で変わりうる挙動を述べた箇所には marker が 1 行ある
(AGENTS.md「Claude Code の挙動を書くときの決めごと」)。

```text
cc-premise: <version> <topic> [メモ]
```

- `<version>`: その前提を最後に確かめた Claude Code の版 (`2.1.296`)
- `<topic>`: 下の表の 1 語
- メモ: 任意。人が読むためのもので、機械は読まない
- Go と sh はコメント行、md は `<!-- cc-premise: … -->` の 1 行

```sh
rg -n -o 'cc-premise: [0-9.]+ [a-z0-9-]+.*' --glob '!tmp/**' . | sort -t' ' -k3,3 -k2,2V
```

これが「どこに、どの版まで確かめた前提があるか」の一覧。topic ごとに並ぶ。

- `<version>` が今回の範囲の始まりより古い marker が点検の対象。topic の検索語で CHANGELOG を引き、
  手順 3・4 で突き合わせる
- 確かめ直して変更不要だった marker は `<version>` を今の版へ上げる。この差分が点検の証跡になる
- 前提が崩れていたら、直す PR か issue に marker のある行を指す
- 表に無い topic が出てきたら、この表に足す (`awk '{print $3}' | sort -u` で漏れを見る)

| topic | daemon の役目 | CHANGELOG の検索語 |
| ---- | ---- | ---- |
| `limit-screen` | 上限の検知と再開 | usage limit, rate limit, auto-continue, continuing automatically, press enter, spend limit, usage credits, autoContinueAtUsageLimit, rate-limit-options |
| `transient` | transient の引き取り | API Error, overloaded, 529, retry, backoff, RETRY_WATCHDOG |
| `compact` | compact の前後 (hook、境界行) | compaction, compact, PostCompact, PreCompact, autocompact |
| `idle-compact` | cache の失効前の compact | idle compaction, idleCompaction, IDLE_COMPACT |
| `cache` | prompt cache の TTL と ack | prompt cache, cache TTL, promptCacheTtl, transcript |
| `statusline` | statusline の sidecar | status line, statusline, rate_limits, context_window, prompt_cache |
| `hooks` | hook の配線 | UserPromptSubmit, Stop hook, SessionStart, onFailure, hook timeout |
| `osc7501` | 多重化ツールへの状態の報告 | OSC 7501, Program Status, terminal, title |
| `peer-messaging` | 別セッションへの送受信 | ListAgents, SendMessage, messaging socket, cross-session |

## やらないこと

- 範囲外の版や、daemon と無関係な項目の整理。レポートが長くなるだけで判断材料は増えない。
- 見つけた競合をその場で直す。挙動が変わるので利用者の判断。docs の食い違いも同じ PR に混ぜず、別の PR にする。
- CHANGELOG を WebFetch の要約で読む。flag 付きの 1 行 (「Both need v2.1.290 or later」のような) が落ちる。
