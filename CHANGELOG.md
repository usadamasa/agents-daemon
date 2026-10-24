# Changelog

## [2026.1024.4](https://github.com/usadamasa/agents-daemon/compare/2026.1024.3...2026.1024.4) - 2026-10-24

- 形式手法の評価と、不変条件を乱択と fuzz で確かめる実行できる仕様の導入 by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/240
- Phase 1: ボードの受け渡しとタスクの記録を TLA+ に写し、TLC で #242 の交錯を反例として出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/246
- ボードの受け渡しの直しをモデルで確かめてから実装する (Phase 2) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/247
- test: 受け渡しの不変条件を実物の Store の交錯で確かめる乱択テストにする (#245) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/248

## [2026.1024.3](https://github.com/usadamasa/agents-daemon/compare/2026.1024.2...2026.1024.3) - 2026-10-24

- prompt の hint 行に出すパーキングのメモの数を消す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/235
- go-arch-metrics を 2026.1024.0 へ上げ、外から参照されない exported を unexport する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/238
- mtime で消す掃除を statefile.PruneDir に集める by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/239

## [2026.1024.2](https://github.com/usadamasa/agents-daemon/compare/2026.1024.1...2026.1024.2) - 2026-10-24

- fix: CLI の error を main が agents-daemon: 付きで 1 行だけ stderr に出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/233

## [2026.1024.1](https://github.com/usadamasa/agents-daemon/compare/2026.1024.0...2026.1024.1) - 2026-10-24

- docs: rex の changelog の platform 別投稿に合わせて rex-premise の <version> の規則を決める by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/228
- fix(rexcli): rex の binary の置き場 (Versions/Current/) に locate の前提と doctor の出力を合わせる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/230
- ボードの凡例の ↵ の語を done から idle にする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/231
- fix(daemon): 起動の排他を daemon.lock の flock で取り、バイナリ差し替え時に 2 つ起きないようにする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/232

## [2026.1024.0](https://github.com/usadamasa/agents-daemon/compare/2026.1021.1...2026.1024.0) - 2026-10-24

- build(deps): Bump actions/create-github-app-token from 3.2.0 to 3.3.0 by @dependabot[bot] in https://github.com/usadamasa/agents-daemon/pull/210
- build(deps): Bump actions/checkout from 6.0.3 to 7.0.1 by @dependabot[bot] in https://github.com/usadamasa/agents-daemon/pull/208
- build(deps): Bump actions/upload-artifact from 7.0.1 to 7.0.2 by @dependabot[bot] in https://github.com/usadamasa/agents-daemon/pull/209
- build(deps): Bump Songmu/tagpr from 1.20.3 to 1.21.3 by @dependabot[bot] in https://github.com/usadamasa/agents-daemon/pull/207
- refactor: ファイルの原子的な書き込みと JSON の読み込みを statefile に寄せる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/217
- go.mod に toolchain go1.26.10 を足し、利用者の手元のビルドを crypto/x509 の修正版にそろえる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/218
- build(go): x509roots/fallback を 2026-10-20 版へ上げる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/219
- docs: statusline の stdin にモデル単位の週次ウィンドウが来ない前提を置く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/220
- ci: setup-nix を gh-manage の共有 composite action へ移す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/222
- docs: rex preview.19 (CLI bd45734) の点検で rex-premise の marker を上げる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/225

## [2026.1021.1](https://github.com/usadamasa/agents-daemon/compare/2026.1021.0...2026.1021.1) - 2026-10-21

- ci: govulncheck を週次で定期実行する (合否は symbol、module は表示) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/212

## [2026.1021.0](https://github.com/usadamasa/agents-daemon/compare/2026.1020.2...2026.1021.0) - 2026-10-21

- build: nixpkgs の rev を 2026-10-20 の nixos-unstable へ上げる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/199
- docs: rex preview.17 の点検で rex-premise marker を上げる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/201
- board: 自セッションで置いたメモを自セッションへ To task / Ask for issue で回す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/202
- board: メモを渡す先に、新しいセッションを起こす経路を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/204
- devShell の Go を 1.26.10 に差し替えて GO-2026-6809 を解消する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/205
- Dependabot で go.mod・GitHub Actions・flake.lock を週次で上げる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/206
- feat: SendMessage で渡したのに辺の無いセッションを band に出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/211

## [2026.1020.2](https://github.com/usadamasa/agents-daemon/compare/2026.1020.1...2026.1020.2) - 2026-10-20

- ボードの pane の底に記号の凡例を置く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/197

## [2026.1020.1](https://github.com/usadamasa/agents-daemon/compare/2026.1020.0...2026.1020.1) - 2026-10-20

- go-arch-lint の走査から .claude/ を外し、別セッションの worktree があっても task lint が通るようにする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/188
- 依存の DAG を pane にレーンで描き、band から図を外す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/192

## [2026.1020.0](https://github.com/usadamasa/agents-daemon/compare/2026.1019.1...2026.1020.0) - 2026-10-20

- ratelimit の fresh で書き込みとの競合による数秒の負の age を許容する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/174
- chore(rexcli): rex preview.16 に追従し marker を確かめ直す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/178
- doctor に rex の api_revision を取り込み、--json のグローバルオプションを足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/183
- 上限の出来事を limit-events/ に残し、modes / size で block の高さと screen を doctor に出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/185
- パーキングのメモを見せる面に溜まったものを刈り込めるようにする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/186

## [2026.1019.1](https://github.com/usadamasa/agents-daemon/compare/2026.1019.0...2026.1019.1) - 2026-10-19

- /board の行を枠なしにし、選んだ項目の詳細と操作を枠で囲む by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/168
- 待っているノードを idle の節に分け、要約の無いセッションに名前を出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/170

## [2026.1019.0](https://github.com/usadamasa/agents-daemon/compare/2026.1018.11...2026.1019.0) - 2026-10-19

- feat(board): status --board の JSON に依存の DAG を載せる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/163
- feat(board): 止まっているノードを根にした依存の木を dock の pane に描く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/164
- feat(band): 入力欄の上の band に依存の DAG とパーキングのメモの 1 行を出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/165

## [2026.1018.11](https://github.com/usadamasa/agents-daemon/compare/2026.1018.10...2026.1018.11) - 2026-10-18

- lint で gofmt のずれを拾い、housekeeping_test.go を整形する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/149
- domain から config への依存を外し、Classifier の組み立てを呼び出し側へ戻す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/150
- リポジトリの外なら cwd を鍵にする規則を Place.Key の 1 箇所にまとめる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/151
- パーキングのメモを入力欄の上の band に出し、時刻を JST で見せる (#121 段 7) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/153
- セッションをまたぐ辺を書く・消す・一覧する CLI (edge add|rm|list) を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/154
- ボードのカードに branch ではなくセッション名を出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/155
- タスクの無いセッションのカードに今の作業を出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/156
- cmd/agents-daemon を root の登録だけにし、コマンドの組み立てを機能ごとの cli パッケージへ移す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/158
- タスクのボードの経路の出来事を domain ごとのログに出す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/159
- パーキングのものを docs と CLI で「件」から「メモ」と呼び直す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/160

## [2026.1018.10](https://github.com/usadamasa/agents-daemon/compare/2026.1018.9...2026.1018.10) - 2026-10-18

- ボードにパーキングの列と操作を足す (#121 段 6) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/147

## [2026.1018.9](https://github.com/usadamasa/agents-daemon/compare/2026.1018.8...2026.1018.9) - 2026-10-18

- パーキングロットにメモを置く park tool と status 行のメモの数を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/143

## [2026.1018.8](https://github.com/usadamasa/agents-daemon/compare/2026.1018.7...2026.1018.8) - 2026-10-18

- chore: Claude Code が生成する tsconfig.json を ignore する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/131
- パーキングの件を park add / set / list で保存する (#121 段 4) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/135
- cmd/agents-daemon の責務の置き場を見直す (#121) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/136

## [2026.1018.7](https://github.com/usadamasa/agents-daemon/compare/2026.1018.6...2026.1018.7) - 2026-10-18

- feat(board): タスクのボードを repo の行と状態の列で mod に描く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/128

## [2026.1018.6](https://github.com/usadamasa/agents-daemon/compare/2026.1018.5...2026.1018.6) - 2026-10-18

- feat(domain): cwd から私用と業務を判定する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/123
- refactor(cmd): status と statusline の本体を internal へ移す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/125
- feat(tasklog): PostToolUse hook でセッションのタスクを記録する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/126

## [2026.1018.5](https://github.com/usadamasa/agents-daemon/compare/2026.1018.4...2026.1018.5) - 2026-10-18

- refactor: sessionstate を rate-limits と context の書式ごとに分ける by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/117
- chore(metrics): go-arch-metrics を 2026.1018.3 に上げ、spm-go を外して測り直す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/119
- chore(deps): go mod tidy で go.sum の使わない go-arch-metrics の行を消す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/120

## [2026.1018.4](https://github.com/usadamasa/agents-daemon/compare/2026.1018.3...2026.1018.4) - 2026-10-18

- fix(rexcli): rex の自動更新で旧 build が消えても rex を引き直して続ける by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/110
- feat(doctor): rex CLI の応答の形の点検を自分の block で行う節を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/111
- feat(monitor): record の kind / msg を出し、利用者が答える prompt が開いた pane へは再開を送らない by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/113

## [2026.1018.3](https://github.com/usadamasa/agents-daemon/compare/2026.1018.2...2026.1018.3) - 2026-10-18

- docs(skills): agents-daemon skill の SKILL.md を症状表と references の表に絞る by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/105
- fix(hooks): prune-worktrees.sh に実行ビットを付ける by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/109

## [2026.1018.2](https://github.com/usadamasa/agents-daemon/compare/2026.1018.1...2026.1018.2) - 2026-10-18

- feat: 放置された Claude Code の worktree の回収を plugin へ移す (prune-worktrees) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/103

## [2026.1018.1](https://github.com/usadamasa/agents-daemon/compare/2026.1018.0...2026.1018.1) - 2026-10-18

- docs(setup): dream forget の excludedCommands の行を先頭固定の形にする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/100
- chore: 旧 cc-dream の state の移行スクリプトと bats を消す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/102

## [2026.1018.0](https://github.com/usadamasa/agents-daemon/compare/2026.1017.0...2026.1018.0) - 2026-10-18

- docs: daemon の役割を「セッションの中からはできないことを担う土台」に書き直す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/85
- docs: md の句読点を全角の「。、」へ揃える by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/92
- style: Go・sh・設定ファイルの句読点を全角の「。、」へ揃える by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/93
- rexcli のテストを振る舞い単位に畳み、配線と写像でファイルを分ける by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/94
- Pane.TerminalID を PaneID に統合する (#73) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/95
- capture の読み取り範囲と block の高さの下限を実測に合わせる (#73) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/96
- feat: cc-dream (harvest / mine / stage / adopt / forget) と dream skill を plugin に取り込む by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/98

## [2026.1017.0](https://github.com/usadamasa/agents-daemon/compare/2026.1016.0...2026.1017.0) - 2026-10-17

- ci: GitHub Actions の固定を pinact から gh-actions-lock へ移行する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/67
- docs: rex の症状表に server の資源上限の行を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/70
- herdr のサポートを廃止して rex 専用にする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/71
- 開発ツールの管理を aqua から nix の devShell へ移す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/72
- docs: multiplexer.Client を残す理由を doc comment に書く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/74
- rex preview.15 の program_status の自己申告に合わせ、前景にいない process の record を使わない by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/76
- rex の更新に追従する maintainer 用 skill (changelog と rex-premise marker の突き合わせ) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/81
- daemon から compact の自動化と prompt cache の追跡を外し、利用上限からの再開だけにする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/83

## [2026.1016.0](https://github.com/usadamasa/agents-daemon/compare/2026.1011.1...2026.1016.0) - 2026-10-16

- ci: 全ジョブ成功時に ci-status-check を success にする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/57
- docs: Claude Code 2.1.290〜2.1.299 の点検に沿って references を直す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/61
- feat: Claude Code の更新に追従する skill と cc-premise marker を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/63
- docs: rex の情報源に superlogical の MCP を足す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/65

## [2026.1011.1](https://github.com/usadamasa/agents-daemon/compare/2026.1011.0...2026.1011.1) - 2026-10-11

- idle compact が送らなかった理由を inspect とログで追えるようにする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/55

## [2026.1011.0](https://github.com/usadamasa/agents-daemon/compare/2026.1010.3...2026.1011.0) - 2026-10-11

- feat: peer-sessions skill を plugin に移し、peer messaging の診断を doctor と setup に持たせる (#35 フェーズ 3) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/53

## [2026.1010.3](https://github.com/usadamasa/agents-daemon/compare/2026.1010.2...2026.1010.3) - 2026-10-10

- feat: herdr-claude-title.sh を plugin の herdr-title hook に移す (#35 フェーズ 2 の前半) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/48
- feat: herdr の session_id を agent_session ではなく statusline の sidecar から引く (#35 フェーズ 2 の後半) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/51

## [2026.1010.2](https://github.com/usadamasa/agents-daemon/compare/2026.1010.1...2026.1010.2) - 2026-10-10

- docs: plugin 更新後にセッションを開き直さず daemon を入れ替える手順を書く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/43
- simulate を rex でも使えるようにする by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/45
- feat: statusline を agents-daemon statusline サブコマンドに移す (#35 フェーズ 4) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/47

## [2026.1010.1](https://github.com/usadamasa/agents-daemon/compare/2026.1010.0...2026.1010.1) - 2026-10-10

- fix(detect): transient の判定を Claude Code の API Error 行に限る by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/41

## [2026.1010.0](https://github.com/usadamasa/agents-daemon/compare/2026.0930.0...2026.1010.0) - 2026-10-10

- docs: dream で AGENTS.md の重複を削り、リリース後の plugin 更新を書く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/32
- rex に対応する: 多重化ツールを抽象化し herdr と rex の backend を置く by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/36

## [2026.0930.0](https://github.com/usadamasa/agents-daemon/compare/2026.0929.0...2026.0930.0) - 2026-09-30

- fix: idle compact と cache の状態を daemon が transcript から求める by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/25

## [2026.0929.0](https://github.com/usadamasa/agents-daemon/compare/2026.0928.0...2026.0929.0) - 2026-09-29

- idle compact: prompt cache の失効前に離席中の pane を compact する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/22

## [2026.0928.0](https://github.com/usadamasa/agents-daemon/compare/2026.0927.2...2026.0928.0) - 2026-09-28

- TTL guard: cache 失効後の最初の prompt を 1 回止める (#11) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/19

## [2026.0927.2](https://github.com/usadamasa/agents-daemon/compare/2026.0927.1...2026.0927.2) - 2026-09-27

- feat: ingest-stop で Stop hook から prompt cache の状態を cache/ へ写す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/16
- feat: daemon が cache の失効を知った上で prompt を送る (ack マーカー・ログ・status) by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/18

## [2026.0927.1](https://github.com/usadamasa/agents-daemon/compare/2026.0927.0...2026.0927.1) - 2026-09-27

- feat: ingest-statusline で statusline の書き出しを plugin のバイナリへ移す by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/14

## [2026.0927.0](https://github.com/usadamasa/agents-daemon/compare/2026.0926.01...2026.0927.0) - 2026-09-27

- setup skill を追加する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/4
- README: plugin install コマンドの marketplace 名を実態に合わせる by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/5
- feat: tagpr でリリース PR を作る by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/7

## [2026.0926.01](https://github.com/usadamasa/agents-daemon/commits/2026.0926.01) - 2026-09-26

- claude-auto-retry を agents-daemon plugin として移植する by @usadamasa in https://github.com/usadamasa/agents-daemon/pull/1
