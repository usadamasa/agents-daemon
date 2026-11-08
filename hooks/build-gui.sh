#!/bin/bash
# plugin のソースから依存図のアプリ (gui/、agent-tasks) を go build し、XDG cache の固定パスと ~/Applications の
# .app に置く。build-daemon.sh の末尾と、ensure-daemon.sh (daemon は最新で gui だけ古いとき) が呼ぶ。
# task gui:install も同じ。出力は build.log へ流れる。
#
# - macOS 以外、go / cc / codesign / plutil が無い環境では INFO 1 行で飛ばす (exit 0)。stamp は書かないので次の
#   SessionStart がまた試す。gui は daemon と違って無くても plugin の他の機能に影響しない
# - 同時に起きた build は lock で 1 本に絞る (build-lock.sh)
# - バイナリは一時ファイルから mv で差し替え、.app は一時の bundle を組んで rename で入れ替える。起動中のアプリは
#   元の inode を持ち続けるので、次に開いたときに新しくなる
# - 署名は ad hoc (手元で建てたものを手元で動かすだけ。quarantine が付かないので Gatekeeper は見ない)

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="build-gui"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"
# shellcheck disable=SC2034 # build-lock.sh が読む
BUILD_LOCK="$GUI_BUILD_LOCK"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/build-lock.sh"

if [ "$(uname -s)" != Darwin ]; then
  log_info "not macOS; skipping agent-tasks"
  exit 0
fi
for tool in go cc codesign plutil; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    log_info "$tool not found in PATH; skipping agent-tasks (needs the Go toolchain and Xcode Command Line Tools)"
    exit 0
  fi
done

if ! mkdir -p "$DAEMON_BIN_DIR"; then
  log_error "mkdir failed: $DAEMON_BIN_DIR"
  exit 1
fi

claim_lock || exit 0

tmp_bin=""
tmp_app=""
tmp_stamp=""
cleanup() {
  if [ -n "$tmp_bin" ] && [ -e "$tmp_bin" ]; then
    rm -f "$tmp_bin" || log_error "rm failed: $tmp_bin"
  fi
  if [ -n "$tmp_app" ] && [ -e "$tmp_app" ]; then
    rm -rf "$tmp_app" || log_error "rm failed: $tmp_app"
  fi
  if [ -n "$tmp_stamp" ] && [ -e "$tmp_stamp" ]; then
    rm -f "$tmp_stamp" || log_error "rm failed: $tmp_stamp"
  fi
  release_lock
}
# lock を取った後で trap を張る。先に張ると、lock を取れずに抜ける経路が他の build の lock を消す。
trap cleanup EXIT
trap 'exit 1' INT TERM

if ! version=$(plugin_version); then
  log_error "cannot read version from $PLUGIN_ROOT/.claude-plugin/plugin.json"
  exit 1
fi

log_info "$(date '+%Y-%m-%dT%H:%M:%S%z') building agent-tasks $version from $PLUGIN_ROOT/gui"

if ! tmp_bin=$(mktemp "$DAEMON_BIN_DIR/.agent-tasks.XXXXXX"); then
  log_error "mktemp failed in $DAEMON_BIN_DIR"
  exit 1
fi
# deployment target は Go 1.27 が要求する macOS の下限 13.0 (Taskfile の gui:build と揃える)。下げると ld が警告する。
# version は窓の title に出す (gui/main.go の version)。
if ! CGO_CFLAGS=-mmacosx-version-min=13.0 CGO_LDFLAGS=-mmacosx-version-min=13.0 MACOSX_DEPLOYMENT_TARGET=13.0 \
  go build -C "$PLUGIN_ROOT/gui" -ldflags "-X main.version=$version" -o "$tmp_bin" .; then
  log_error "go build failed"
  exit 1
fi
if ! mv "$tmp_bin" "$GUI_BIN"; then
  log_error "mv failed: $tmp_bin -> $GUI_BIN"
  exit 1
fi
tmp_bin=""

# .app は一時の bundle を組んでから rename で入れ替える。bundle の中身を直接書き換えると、起動中のアプリの
# 実行ファイルを上書きすることになる。
app_dir=$(dirname "$GUI_APP")
if ! mkdir -p "$app_dir"; then
  log_error "mkdir failed: $app_dir"
  exit 1
fi
if ! tmp_app=$(mktemp -d "$app_dir/.agent-tasks.app.XXXXXX"); then
  log_error "mktemp failed in $app_dir"
  exit 1
fi
if ! mkdir -p "$tmp_app/Contents/MacOS" "$tmp_app/Contents/Resources"; then
  log_error "mkdir failed: $tmp_app/Contents/MacOS, $tmp_app/Contents/Resources"
  exit 1
fi
# アイコンは生成済みの .icns を写すだけ (作り直しは task gui:icon)。Info.plist の CFBundleIconFile が名前で指す。
if ! cp "$PLUGIN_ROOT/gui/darwin/agent-tasks.icns" "$tmp_app/Contents/Resources/agent-tasks.icns"; then
  log_error "cp failed: $PLUGIN_ROOT/gui/darwin/agent-tasks.icns -> $tmp_app/Contents/Resources/agent-tasks.icns"
  exit 1
fi
if ! cp "$GUI_BIN" "$tmp_app/Contents/MacOS/agent-tasks"; then
  log_error "cp failed: $GUI_BIN -> $tmp_app/Contents/MacOS/agent-tasks"
  exit 1
fi
if ! cp "$PLUGIN_ROOT/gui/darwin/Info.plist" "$tmp_app/Contents/Info.plist"; then
  log_error "cp failed: $PLUGIN_ROOT/gui/darwin/Info.plist -> $tmp_app/Contents/Info.plist"
  exit 1
fi
# About の panel と Finder の「情報を見る」は Info.plist の version を出す。
for key in CFBundleShortVersionString CFBundleVersion; do
  if ! plutil -replace "$key" -string "$version" "$tmp_app/Contents/Info.plist"; then
    log_error "plutil failed: $key in $tmp_app/Contents/Info.plist"
    exit 1
  fi
done
# go build の linker が arm64 のバイナリに付けた ad hoc 署名を先に剥がす。残したまま bundle に署名すると、codesign が
# 上書きの案内行 (replacing existing signature) を毎回 build.log に出す。
if ! codesign --remove-signature "$tmp_app/Contents/MacOS/agent-tasks"; then
  log_error "codesign --remove-signature failed: $tmp_app/Contents/MacOS/agent-tasks"
  exit 1
fi
if ! codesign --force --sign - "$tmp_app"; then
  log_error "codesign failed: $tmp_app"
  exit 1
fi
old_app=""
if [ -e "$GUI_APP" ]; then
  old_app="$GUI_APP.old.$$"
  if ! mv "$GUI_APP" "$old_app"; then
    log_error "mv failed: $GUI_APP -> $old_app"
    exit 1
  fi
fi
if ! mv "$tmp_app" "$GUI_APP"; then
  log_error "mv failed: $tmp_app -> $GUI_APP"
  if [ -n "$old_app" ]; then
    mv "$old_app" "$GUI_APP" || log_error "mv failed: $old_app -> $GUI_APP"
  fi
  exit 1
fi
tmp_app=""
if [ -n "$old_app" ]; then
  rm -rf "$old_app" || log_error "rm failed: $old_app"
fi

# stamp は成果物を置いた後に書く。逆順だと、途中で落ちたとき古いアプリが新しい version を名乗る。
if ! tmp_stamp=$(mktemp "$DAEMON_BIN_DIR/.agent-tasks.version.XXXXXX"); then
  log_error "mktemp failed in $DAEMON_BIN_DIR"
  exit 1
fi
if ! printf '%s\n' "$version" >"$tmp_stamp"; then
  log_error "write failed: $tmp_stamp"
  exit 1
fi
if ! mv "$tmp_stamp" "$GUI_STAMP"; then
  log_error "mv failed: $tmp_stamp -> $GUI_STAMP"
  exit 1
fi
tmp_stamp=""

log_info "built $GUI_APP ($version)"
