# hook-test-helpers.bash
# hook テスト用の共通ヘルパー

# hook スクリプトを本番と同じ bash で実行する｡
# 本番の hook shebang は #!/bin/bash (macOS の /bin/bash 3.2)｡
# PATH 経由の `bash` (homebrew の 5.x 等) だと bash 4+ builtin (mapfile 等) の
# 非互換が検出できずすり抜けるため､テストは既定で /bin/bash に固定する｡
# 別バージョンで検証したい場合は HOOK_BASH=/path/to/bash を設定する｡
#
# 先頭の `--` で始まる引数は bats の run へ渡す (例: run_hook --separate-stderr hook.sh)｡
run_hook() {
  local -a run_opts=()
  while [[ "${1:-}" == --* ]]; do
    run_opts+=("$1")
    shift
  done
  run ${run_opts[@]+"${run_opts[@]}"} "${HOOK_BASH:-/bin/bash}" "$@"
}
