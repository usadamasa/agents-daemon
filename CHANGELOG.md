# Changelog

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
