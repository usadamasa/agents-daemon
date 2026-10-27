package rexcli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// testdata の実測のディレクトリ。中身の由来は testdata/README.md。blockstate のテストも同じ fixture を読む。
const (
	measuredOct10 = "2026-10-10_rex-0.0.1791570264_cc-2.1.296"
	measuredOct18 = "2026-10-18_rex-preview.15_cc-2.1.302"
	measuredOct20 = "2026-10-20_rex-preview.16_cc-2.1.304"
	measuredOct27 = "2026-10-27_rex-preview.21_cc-2.1.311"
)

// 実測の fixture の owner.pid。foreground の process の pid と突き合わせる。
const (
	pidClaudeWorking = 15470
	pidClaudeDone    = 17633
)

// 2026-10-27 の events.jsonl に出る ID。
const (
	sessionOwn  = "session:01a17d3f-df41-7910-a1ba-34bf3c9384f2"
	blockOwn    = "block:01a17d3f-df41-7a0a-b43a-445fabff2f4f"
	pidOwnShell = 79054
	sidOwn      = "8c798e60-d1c2-4967-b62a-a1b2a4be9fc3"
	cwdOwn      = "/Users/usadamasa/src/github.com/usadamasa/agents-daemon"
)

// 実測に無い形の program_status。
const (
	psEmpty = `{"records":[]}`
	// rex が owner を付けなくなった (または名前を変えた) ときの形。
	psNoOwner = `{"records":[{"state":"done","app":"claude-code","effective_app":"claude-code","updated_at":"2026-10-17T14:00:00.000000+09:00"}]}`
)

// measured は testdata の実測の応答を返す。
func measured(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", dir, name)) // #nosec G304 -- テストの fixture
	if err != nil {
		t.Fatalf("fixture %s/%s の読み込みに失敗: %v", dir, name, err)
	}
	return string(b)
}

// processJSON は `block call process` の応答。2026-10-10 の実測の pid と argv0 と cwd を差し替える。
func processJSON(pid int, argv0, cwd string) string {
	return `{"last_exit":null,` +
		`"child":{"pid":33929,"user_id":0,"user":"root","name":"login","path":"/usr/bin/login"},` +
		`"foreground":{"pid":` + strconv.Itoa(pid) + `,"user_id":501,"user":"usadamasa","name":"2.1.296","path":"/Users/usadamasa/.local/share/claude/versions/2.1.296","invoked_path":"/Users/usadamasa/.local/bin/claude","argv0":"` + argv0 + `","cwd":"` + cwd + `"},` +
		`"ancestors":[{"pid":33930,"user_id":501,"user":"usadamasa","name":"zsh","path":"/bin/zsh","invoked_path":"/bin/zsh","argv0":"-zsh","cwd":"` + cwd + `"}],` +
		`"home":"/Users/usadamasa"}`
}

// psChangedLine は events --all --json の program_status_changed の行を 2026-10-27 の実測の形で組む (root record)。
func psChangedLine(session, block, state, msg string, pid int) string {
	record := `{"state":"` + state + `","app":"claude-code","effective_app":"claude-code","msg":"` + msg +
		`","updated_at":"2026-10-27T21:19:18.848469+09:00","owner":{"pid":` + strconv.Itoa(pid) + `,"name":"2.1.311"}}`
	return `{"type":"session_event","payload":{"session_id":"` + session + `","event":{"type":"block_event","payload":{"block_id":"` + block +
		`","creator_name":"com.superlogical.terminal","name":"program_status_changed","payload":{"id":"","record":` + record + `,"reason":"report"}}}}}`
}
