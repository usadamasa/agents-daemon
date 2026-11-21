#!/bin/bash
# リリースの前に、常用の環境から切り離した検証の環境を作り、commit から建てたバイナリを実データの複製に当てる。
# 使い方と手順は .claude/skills/changing-agents-daemon-internals/references/release-verify.md。
#
#   release-verify.sh up              環境を作る (REF の commit を展開して建て、実データを複製する)
#   release-verify.sh check           機械の点検を回し、通れば記録を書く
#   release-verify.sh open            隔離した env で依存図のアプリを起こす (人の確認の入口)
#   release-verify.sh approve         人の確認を記録に書く
#   release-verify.sh status [ref]    ref の tree に通った記録があれば exit 0。stdout は 1 行の JSON
#   release-verify.sh down            環境を消す
#   release-verify.sh run             up → check → down (KEEP=1 なら残す。check が落ちても残す)
#
# - 記録の鍵は commit ではなく tree の hash。リリース PR は merge commit で入り、tag が付く commit の SHA は
#   検証した PR head と違うので、同じ中身かどうかは tree でしか言えない
# - CLI とアプリには存在しない rex の socket を渡す。spawn / tell / park hand は --dry-run で止まらず、
#   本物の rex に届くと常用の環境に Claude のセッションを起こす
# - daemon だけは本物の rex と Claude の設定ディレクトリを読む (pane と transcript が要る)。--dry-run を外す引数は
#   作らない。常用の daemon と同じ pane を見るので、外すと二重に送信する

set -euo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO="${VERIFY_REPO:-$(cd "$SCRIPT_DIR/.." && pwd)}"
BASE="${VERIFY_BASE:-$HOME/tmp/agents-daemon-verify}"
SRC_STATE="${SRC_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon}"
SRC_CLAUDE="${SRC_CLAUDE:-${CLAUDE_CONFIG_DIR:-$HOME/.claude}}"
SRC_CONFIG="${SRC_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/agents-daemon/config.json}"
BASELINE_BIN="${VERIFY_BASELINE_BIN:-${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon}"
REX_SOCKET="${VERIFY_REX_SOCKET:-$HOME/Library/Application Support/rex/server.sock}"
# daemon を回す秒数。tick の既定は 5 秒なので、数 tick 回る長さにする。
DAEMON_SECONDS="${VERIFY_DAEMON_SECONDS:-20}"
# auto は macOS でだけアプリを建てる。skip は建てない (アプリの確認ができない)。
GUI_MODE="${VERIFY_GUI:-auto}"
# sandbox-exec は、daemon が本物の Claude の設定ディレクトリへ書くことを OS に拒ませる。none は fixture のテスト用。
WRITE_GUARD="${VERIFY_WRITE_GUARD:-sandbox-exec}"
REAL_APP="$HOME/Applications/agent-tasks.app"

NOTES_REF=release-verify
MARKER=.agents-daemon-verify
# 複製する state。daemon の動作の引き金になるもの (spawned-sessions、rate-limits、quota-resume、turn-failures) と
# daemon 自身のファイルは写さない。
STATE_DIRS=(work tasks edges parking refs sessions context dream)
# 人の確認が要るかを決めるパス。画面と mod は機械の点検で見ていない。
HUMAN_PATHS=(gui types ':(glob)hooks/*.ts' ':(glob)hooks/*.tsx')

info() {
  printf 'release-verify: %s\n' "$*" >&2
}

die() {
  printf 'release-verify: ERROR: %s\n' "$*" >&2
  exit 2
}

physical() {
  (cd "$1" && pwd -P)
}

# ref を commit と tree に解決し、環境の root と、その下のパスを決める。
resolve() {
  local ref="$1"
  COMMIT=$(git -C "$REPO" rev-parse --verify --quiet "${ref}^{commit}") || die "ref を commit に解決できない: $ref"
  TREE=$(git -C "$REPO" rev-parse --verify "${COMMIT}^{tree}")
  ROOT="$BASE/${TREE:0:12}"
  R_HOME="$ROOT/home"
  R_STATE="$R_HOME/.local/state/agents-daemon"
  R_CLAUDE="$R_HOME/.claude"
  R_BIN="$R_HOME/.cache/agents-daemon/bin/agents-daemon"
  R_APP="$R_HOME/Applications/agent-tasks.app"
  R_REPORT="$ROOT/report"
  common_env=(env -i "HOME=$R_HOME" "CFFIXED_USER_HOME=$R_HOME" "XDG_STATE_HOME=$R_HOME/.local/state"
    "XDG_CACHE_HOME=$R_HOME/.cache" "XDG_CONFIG_HOME=$R_HOME/.config" "PATH=/usr/bin:/bin:/usr/sbin:/sbin"
    "LANG=${LANG:-en_US.UTF-8}" "TMPDIR=$ROOT/tmp")
  cli_env=("${common_env[@]}" "CLAUDE_CONFIG_DIR=$R_CLAUDE" "REX_SERVER=unix://$ROOT/rex-dead.sock")
  # REX_SERVER は URL なので空白を percent-encode する。REX_SESSION は起動の条件で、ループは値を読まない。
  daemon_env=("${common_env[@]}" "CLAUDE_CONFIG_DIR=$SRC_CLAUDE" "REX_SERVER=unix://${REX_SOCKET// /%20}"
    "REX_SESSION=session:release-verify")
}

# ROOT が、この script が作った環境であることを確かめる。消す前と、中のバイナリを動かす前に呼ぶ。
require_env() {
  [ ! -L "$ROOT" ] || die "環境の root が symlink になっている: $ROOT"
  [ -d "$ROOT" ] || die "環境が無い: $ROOT (先に up を打つ)"
  [ -f "$ROOT/$MARKER" ] || die "印 ($MARKER) が無いので、この script が作った環境ではない: $ROOT"
  [ "$(physical "$(dirname "$ROOT")")" = "$(physical "$BASE")" ] || die "環境の root が置き場の外を指している: $ROOT"
  [ "$(jq -r .tree "$ROOT/$MARKER")" = "$TREE" ] || die "印の tree が ref の tree と違う: $ROOT"
}

# 常用の環境のうち、この script が変えてはいけないファイルの中身の要約を出す。
fingerprint() {
  local f
  for f in "$BASELINE_BIN" "$(dirname "$BASELINE_BIN")/agents-daemon.version" \
    "$(dirname "$BASELINE_BIN")/agent-tasks.version" "$REAL_APP/Contents/Info.plist" \
    "$REAL_APP/Contents/MacOS/agent-tasks" "$SRC_CONFIG"; do
    if [ -f "$f" ]; then
      printf '%s %s\n' "$(cksum <"$f")" "$f"
    else
      printf 'absent %s\n' "$f"
    fi
  done
}

# 複製元と環境の置き場が重なっていれば拒む。重なると、down が原本を消す。
refuse_overlap() {
  local base src
  base=$(physical "$BASE")
  for src in "$SRC_STATE" "$SRC_CLAUDE" "$(dirname "$SRC_CONFIG")"; do
    [ -d "$src" ] || continue
    src=$(physical "$src")
    case "$src/" in "$base/"*) die "複製元が環境の置き場の下にある: $src" ;; esac
    case "$base/" in "$src/"*) die "環境の置き場が複製元の下にある: $base" ;; esac
  done
}

# cp は mtime を保ち (-p。カードの状態と保持期間は mtime から導く)、symlink を辿らない (-P。辿ると skill の実体の
# 配置が本番と変わる)。常用の daemon が rename で置き換えた瞬間に当たると cp が落ちるので、3 回まで取り直す。
copy_tree() {
  local src="$1" dst="$2" i
  for i in 1 2 3; do
    rm -rf "$dst"
    if cp -RPp "$src" "$dst"; then
      return 0
    fi
    info "複製を取り直す ($i 回目): $src"
  done
  die "複製できない: $src"
}

snapshot() {
  local name dir project
  mkdir -p "$R_STATE" "$R_CLAUDE/projects" "$R_CLAUDE/plugins" "$R_HOME/.config/agents-daemon"
  for name in "${STATE_DIRS[@]}"; do
    [ -d "$SRC_STATE/$name" ] || continue
    copy_tree "$SRC_STATE/$name" "$R_STATE/$name"
    find "$R_STATE/$name" -type f \( -name '.lock' -o -name '.*.tmp' \) -delete
  done
  for name in CLAUDE.md skills; do
    if [ -e "$SRC_CLAUDE/$name" ] || [ -L "$SRC_CLAUDE/$name" ]; then
      copy_tree "$SRC_CLAUDE/$name" "$R_CLAUDE/$name"
    fi
  done
  for dir in "$SRC_CLAUDE"/projects/*/memory; do
    [ -d "$dir" ] || continue
    project=$(basename "$(dirname "$dir")")
    mkdir -p "$R_CLAUDE/projects/$project"
    copy_tree "$dir" "$R_CLAUDE/projects/$project/memory"
  done
  if [ -f "$SRC_CLAUDE/plugins/installed_plugins.json" ]; then
    cp -p "$SRC_CLAUDE/plugins/installed_plugins.json" "$R_CLAUDE/plugins/installed_plugins.json"
  fi
  if [ -f "$SRC_CONFIG" ]; then
    cp -p "$SRC_CONFIG" "$R_HOME/.config/agents-daemon/config.json"
  fi
}

# アプリは bundle に包む (IME と Info.plist の設定が素のバイナリでは本番と揃わない)。bundle id は常用のアプリと分け、
# 設定の保存先を共有させない。
build_app() {
  local version="$1" key
  mkdir -p "$R_APP/Contents/MacOS" "$R_APP/Contents/Resources"
  CGO_CFLAGS=-mmacosx-version-min=13.0 CGO_LDFLAGS=-mmacosx-version-min=13.0 MACOSX_DEPLOYMENT_TARGET=13.0 \
    go build -C "$ROOT/src/gui" -ldflags "-X main.version=$version" -o "$R_APP/Contents/MacOS/agent-tasks" .
  cp "$ROOT/src/gui/darwin/agent-tasks.icns" "$R_APP/Contents/Resources/agent-tasks.icns"
  cp "$ROOT/src/gui/darwin/Info.plist" "$R_APP/Contents/Info.plist"
  for key in CFBundleShortVersionString CFBundleVersion; do
    plutil -replace "$key" -string "$version" "$R_APP/Contents/Info.plist"
  done
  plutil -replace CFBundleIdentifier -string com.github.usadamasa.agents-daemon.agent-tasks.verify \
    "$R_APP/Contents/Info.plist"
  codesign --remove-signature "$R_APP/Contents/MacOS/agent-tasks"
  codesign --force --sign - "$R_APP"
}

cmd_up() {
  local version="verify-${COMMIT:0:7}"
  if [ -e "$ROOT" ] || [ -L "$ROOT" ]; then
    die "同じ tree の環境が残っている: $ROOT (down で消してから打つ)"
  fi
  mkdir -p "$BASE"
  refuse_overlap
  mkdir "$ROOT"
  jq -n --arg commit "$COMMIT" --arg tree "$TREE" --arg at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    '{schema: 1, commit: $commit, tree: $tree, created_at: $at}' >"$ROOT/$MARKER"
  mkdir -p "$ROOT/src" "$ROOT/tmp" "$R_REPORT" "$(dirname "$R_BIN")"
  fingerprint >"$ROOT/fingerprint.before"

  info "commit ${COMMIT:0:12} (tree ${TREE:0:12}) を展開して建てる: $ROOT"
  git -C "$REPO" archive "$COMMIT" | tar -x -C "$ROOT/src"
  # 建てるのは実環境の Go の cache で行い、置き先だけを環境の下にする。共有の置き場へ書く hooks/build-*.sh は呼ばない。
  go build -C "$ROOT/src" -ldflags "-X main.version=$version" -o "$R_BIN" ./cmd/agents-daemon
  if [ "$GUI_MODE" = auto ] && [ "$(uname -s)" = Darwin ]; then
    build_app "$version"
  else
    info "アプリは建てない (VERIFY_GUI=${GUI_MODE}、$(uname -s))"
  fi

  info "実データを複製する"
  snapshot
  info "環境を作った: $ROOT"
}

# 隔離の env で CLI を 1 つ回し、stdout と stderr を report に残す。終了コードを返す。
run_cli() {
  local bin="$1" name="$2" stdin="$3" code=0
  shift 3
  printf '%s' "$stdin" | "${cli_env[@]}" "$bin" "$@" >"$R_REPORT/$name.out" 2>"$R_REPORT/$name.err" || code=$?
  return "$code"
}

# check の 1 項目の結果を記録する。
record() {
  local name="$1" result="$2" detail="$3"
  printf '%s\t%s\t%s\n' "$name" "$result" "$detail" >>"$R_REPORT/checks.tsv"
  info "[$result] $name${detail:+: $detail}"
  if [ "$result" = fail ]; then
    failed=1
  fi
}

# panic と、利用者向けのエラー (exit 1) を超える終了コードを落とす。指摘を exit 1 で返すコマンドは落とさない。
check_no_crash() {
  local name="$1" code="$2"
  if [ "$code" -gt 1 ] || grep -q -e '^panic:' -e '^goroutine [0-9]' "$R_REPORT/$name.err"; then
    record "$name" fail "exit ${code}。$R_REPORT/$name.err"
  else
    record "$name" pass "exit $code"
  fi
}

check_cli() {
  local project code=0
  # dream の project は cwd ではなく、worktree の親の checkout から決める (memory はそこに紐づく)。
  project="${VERIFY_PROJECT:-$(dirname "$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)")}"

  run_cli "$R_BIN" deps '{}' deps || code=$?
  if [ "$code" -eq 0 ] && jq -e . "$R_REPORT/deps.out" >/dev/null; then
    record deps pass ""
  else
    record deps fail "exit ${code}。$R_REPORT/deps.err"
  fi
  code=0
  run_cli "$R_BIN" dream-policy '' dream policy || code=$?
  check_no_crash dream-policy "$code"
  code=0
  run_cli "$R_BIN" dream-check '' dream check --json --project "$project" || code=$?
  check_no_crash dream-check "$code"

  # 常用のバイナリを同じ複製に当てた出力との差分。挙動が変わった箇所を人が読む材料で、合否には入れない。
  if [ -x "$BASELINE_BIN" ]; then
    run_cli "$BASELINE_BIN" baseline-deps '{}' deps || true
    run_cli "$BASELINE_BIN" baseline-dream-check '' dream check --json --project "$project" || true
    diff -u "$R_REPORT/baseline-deps.out" "$R_REPORT/deps.out" >"$R_REPORT/deps.diff" || true
    diff -u "$R_REPORT/baseline-dream-check.out" "$R_REPORT/dream-check.out" >"$R_REPORT/dream-check.diff" || true
    record baseline-diff info "deps $(wc -l <"$R_REPORT/deps.diff" | tr -d ' ') 行、dream check $(wc -l <"$R_REPORT/dream-check.diff" | tr -d ' ') 行。$R_REPORT/*.diff"
  else
    record baseline-diff info "常用のバイナリが無いので比べない: $BASELINE_BIN"
  fi
}

# daemon を起こし、数 tick の後に止める。起動から停止までをこの呼び出しに閉じる (sandbox の中では、後の
# コマンドから前のコマンドの process に signal を送れない)。
check_daemon() {
  local pid code=0 guard=() log="$R_STATE/logs/daemon.log"
  if [ ! -e "$REX_SOCKET" ]; then
    record daemon fail "rex の socket が無い: $REX_SOCKET"
    return 0
  fi
  case "$WRITE_GUARD" in
    sandbox-exec)
      guard=(sandbox-exec -p "(version 1)(allow default)(deny file-write* (subpath \"$(physical "$SRC_CLAUDE")\"))")
      ;;
    none) ;;
    *) die "VERIFY_WRITE_GUARD は sandbox-exec か none: $WRITE_GUARD" ;;
  esac
  ${guard[@]+"${guard[@]}"} "${daemon_env[@]}" "$R_BIN" daemon --foreground --dry-run >"$R_REPORT/daemon.out" 2>&1 &
  pid=$!
  sleep "$DAEMON_SECONDS"
  if ! kill -TERM "$pid" 2>/dev/null; then
    wait "$pid" || code=$?
    record daemon fail "$DAEMON_SECONDS 秒もたずに終わった (exit $code)。$R_REPORT/daemon.out"
    return 0
  fi
  wait "$pid" || code=$?
  if [ -f "$log" ]; then
    cp "$log" "$R_REPORT/daemon.log"
  fi
  if [ "$code" -ne 0 ]; then
    record daemon fail "exit ${code}。$R_REPORT/daemon.out"
  elif grep -q -e '^panic:' -e '^goroutine [0-9]' "$R_REPORT/daemon.out"; then
    record daemon fail "panic。$R_REPORT/daemon.out"
  elif [ -f "$log" ] && grep -qi 'operation not permitted' "$log"; then
    record daemon fail "書き込みを拒まれた (Claude の設定ディレクトリへ書こうとした疑い)。$R_REPORT/daemon.log"
  else
    record daemon pass "$DAEMON_SECONDS 秒回して SIGTERM で終わった"
  fi
}

cmd_check() {
  local note
  require_env
  failed=0
  mkdir -p "$R_REPORT"
  : >"$R_REPORT/checks.tsv"

  if [ -x "$R_BIN" ]; then
    record binary pass "$R_BIN"
  else
    record binary fail "建てたバイナリが無い: $R_BIN"
  fi
  check_cli
  check_daemon
  fingerprint >"$ROOT/fingerprint.after"
  if diff -u "$ROOT/fingerprint.before" "$ROOT/fingerprint.after" >"$R_REPORT/fingerprint.diff"; then
    record untouched pass "常用のバイナリとアプリと設定は変わっていない"
  else
    record untouched fail "常用の環境が up の後に変わった。$R_REPORT/fingerprint.diff"
  fi

  if [ "$failed" -ne 0 ]; then
    info "機械の点検が落ちた。記録は書かない。report: $R_REPORT"
    return 1
  fi
  note=$(jq -cn --arg commit "$COMMIT" --arg tree "$TREE" --arg at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --rawfile checks "$R_REPORT/checks.tsv" \
    '{schema: 1, tree: $tree, commit: $commit, machine: {result: "pass", at: $at, checks: ($checks | split("\n") | map(select(. != "") | split("\t") | {name: .[0], result: .[1]}))}, human: null}')
  git -C "$REPO" notes --ref="$NOTES_REF" add -f -m "$note" "$TREE"
  info "機械の点検が通った。記録を tree ${TREE:0:12} に書いた"
}

# tree に付いた記録を出す。無ければ空。
read_note() {
  # 記録が無い tree では git が exit 1 で理由を stderr に出す。無いことは正常なので捨てる。
  git -C "$REPO" notes --ref="$NOTES_REF" show "$TREE" 2>/dev/null || true
}

cmd_approve() {
  local note
  require_env
  note=$(read_note)
  if [ "$(jq -r '.machine.result // empty' <<<"$note")" != pass ]; then
    die "機械の点検が通った記録が無い (先に check を通す)"
  fi
  note=$(jq -c --arg at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" '.human = {approved_at: $at}' <<<"$note")
  git -C "$REPO" notes --ref="$NOTES_REF" add -f -m "$note" "$TREE"
  info "人の確認を tree ${TREE:0:12} に書いた"
}

cmd_status() {
  local note result=unverified tag="" human_required=true
  note=$(read_note)
  # 前のリリースの tag から画面と mod に差分が無ければ、人の確認は求めない。tag が無ければ求める。
  if tag=$(git -C "$REPO" describe --tags --abbrev=0 "$COMMIT" 2>/dev/null) &&
    git -C "$REPO" diff --quiet "$tag" "$COMMIT" -- "${HUMAN_PATHS[@]}"; then
    human_required=false
  fi
  if [ "$(jq -r '.machine.result // empty' <<<"$note")" = pass ]; then
    if [ "$human_required" = false ] || [ "$(jq -r '.human.approved_at // empty' <<<"$note")" != "" ]; then
      result=pass
    else
      result=human_required
    fi
  fi
  jq -cn --arg result "$result" --arg tree "$TREE" --arg commit "$COMMIT" --arg tag "$tag" \
    --argjson human_required "$human_required" --argjson note "${note:-null}" \
    '{schema: 1, result: $result, tree: $tree, commit: $commit, previous_tag: $tag, human_required: $human_required,
      record: $note, not_covered: ["spawn", "tell", "park hand", "dream adopt", "dream forget", "見た目と IME (人の確認)"]}'
  [ "$result" = pass ]
}

cmd_open() {
  require_env
  [ -x "$R_APP/Contents/MacOS/agent-tasks" ] || die "アプリを建てていない: $R_APP"
  info "隔離した env でアプリを起こす (窓の題は agent-tasks verify-${COMMIT:0:7})。閉じると戻る"
  # open は env を継がないので、bundle の中の実行ファイルを直に起こす。
  exec "${cli_env[@]}" "$R_APP/Contents/MacOS/agent-tasks"
}

cmd_down() {
  if [ ! -e "$ROOT" ] && [ ! -L "$ROOT" ]; then
    info "環境は無い: $ROOT"
    return 0
  fi
  require_env
  rm -rf "${ROOT:?}"
  info "環境を消した: $ROOT"
}

cmd_run() {
  cmd_up
  if ! cmd_check; then
    info "環境は残してある。report を読んだら down で消す: $ROOT"
    return 1
  fi
  if [ "${KEEP:-0}" = 1 ]; then
    info "環境を残した (KEEP=1)。open で人が確かめ、approve の後に down で消す: $ROOT"
  else
    cmd_down
  fi
}

command="${1:-}"
case "$command" in
  status) resolve "${2:-${REF:-HEAD}}" ;;
  up | check | open | approve | down | run) resolve "${REF:-HEAD}" ;;
  *) die "使い方: release-verify.sh <up|check|open|approve|status [ref]|down|run>" ;;
esac
"cmd_$command"
