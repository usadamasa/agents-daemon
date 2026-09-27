#!/usr/bin/env bats
# hooks/lib/compact-markers.sh の定数と helper を検証する｡
#
# ここの置き場と値は daemon 側 (internal/sessionstate) と対になっている｡片方だけ変えると
# protocol が黙って壊れるので､hook 側の値をこのテストで固定しておく｡

setup() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  LIB="$REPO_ROOT/hooks/lib/compact-markers.sh"
  export XDG_STATE_HOME="/x/state"
  BASE="$XDG_STATE_HOME/agents-daemon"
}

@test "marker と state file の置き場は XDG state 配下の agents-daemon/" {
  # shellcheck disable=SC1090
  source "$LIB"
  [ "$COMPACTED_DIR" = "$BASE/compacted" ]
  [ "$COMPACT_STATE_DIR" = "$BASE/compact-state" ]
  [ "$(compact_state_file sess-1)" = "$BASE/compact-state/sess-1.md" ]
}

@test "cache ack マーカーは cache-ack/<session_id> で､sentinel は daemon-unknown" {
  # daemon (internal/sessionstate) が書き､TTL guard hook が読む｡拡張子なし｡
  # shellcheck disable=SC1090
  source "$LIB"
  [ "$CACHE_ACK_DIR" = "$BASE/cache-ack" ]
  [ "$(cache_ack_file sess-1)" = "$BASE/cache-ack/sess-1" ]
  [ "$CACHE_ACK_UNKNOWN" = "daemon-unknown" ]
}
