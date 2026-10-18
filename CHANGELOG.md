# Changelog

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
