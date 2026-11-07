#!/usr/bin/env bats
# hooks/lib/daemon-bin.sh の version_older を検証する。
#
# version は `YYYY.MMDD.N` で、欄ごとに 10 進の数として比べる。0 始まりの欄 (`0926`、`01`) を 8 進と読まず、
# `9` と `10` を文字列順で取り違えない。

bats_require_minimum_version 1.5.0

setup() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  SCRIPT_DIR="$REPO_ROOT/hooks"
  # shellcheck disable=SC1091 # Dynamically resolved path
  source "$SCRIPT_DIR/lib/daemon-bin.sh"
}

@test "月日の欄が小さければ古い (0 始まりを 8 進と読まない)" {
  run -0 version_older 2026.0926.01 2026.1102.1
  run -1 version_older 2026.1102.1 2026.0926.01
}

@test "連番の欄は数として比べる (9 は 10 より古い)" {
  run -0 version_older 2026.1018.9 2026.1018.10
  run -1 version_older 2026.1018.10 2026.1018.9
}

@test "0 始まりの 8 と 9 を含む欄も比べられる" {
  run -0 version_older 2026.0809.0 2026.0908.0
  run -1 version_older 2026.0908.0 2026.0809.0
}

@test "同じ version は古くない" {
  run -1 version_older 2026.1106.0 2026.1106.0
  run -1 version_older 2026.0926.01 2026.0926.1
}

@test "欄の数が違えば、足りない欄を 0 として比べる" {
  run -1 version_older 2026.1106 2026.1106.0
  run -1 version_older 2026.1106.0 2026.1106
  run -0 version_older 2026.1106 2026.1106.1
}

@test "どちらかが空か数でなければ 2 (古いとも新しいとも言わない)" {
  run -2 version_older "" 2026.1106.0
  run -2 version_older 2026.1106.0 ""
  run -2 version_older garbage 2026.1106.0
  run -2 version_older 2026..0 2026.1106.0
}

@test "stamp_current は stamp が無いか古いか読めなければ偽、同じか新しければ真" {
  stamp="$BATS_TEST_TMPDIR/stamp"
  run -1 stamp_current "$stamp" 2026.1106.0
  printf '2026.1102.1\n' >"$stamp"
  run -1 stamp_current "$stamp" 2026.1106.0
  printf 'garbage\n' >"$stamp"
  run -1 stamp_current "$stamp" 2026.1106.0
  printf '2026.1106.0\n' >"$stamp"
  run -0 stamp_current "$stamp" 2026.1106.0
  printf '2026.1107.0\n' >"$stamp"
  run -0 stamp_current "$stamp" 2026.1106.0
}

@test "年の欄が先に効く" {
  run -0 version_older 2026.1231.9 2027.0101.0
  run -1 version_older 2027.0101.0 2026.1231.9
}
