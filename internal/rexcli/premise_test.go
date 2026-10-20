package rexcli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// 2026-10-18 の実測で doctor を打った自分の block と session (testdata の measuredOct18 の応答はこの ID のもの)。
const (
	blockSelfOct18   = "block:01a14d5d-0dea-72fd-938b-169746065cb2"
	sessionSelfOct18 = "session:01a14d5d-0dea-71ec-97b7-ece67015d584"
)

// selfFixtures は実測どおりの応答を全部そろえた fake を返す。テストはここから 1 つずつ崩す。
func selfFixtures(t *testing.T) *fakeRex {
	t.Helper()
	f := newFakeRex(t)
	f.put(t, "version.json", measured(t, measuredOct18, "version.json"))
	f.put(t, "block-ls-"+sessionSelfOct18+".json", measured(t, measuredOct18, "block-ls.json"))
	f.putBlock(t, blockSelfOct18, measured(t, measuredOct18, "program_status.json"), measured(t, measuredOct18, "process.json"))
	f.put(t, "inspect-"+blockSelfOct18+".json", measured(t, measuredOct18, "block-inspect.json"))
	return f
}

func findingTexts(findings []multiplexer.PremiseFinding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		mark := "OK"
		if !f.OK {
			mark = "NG"
		}
		out = append(out, mark+": "+f.Text)
	}
	return out
}

// 実測どおりの応答なら全部 OK で、打つコマンドは cli-checks.md の「doctor が見る行」と同じ。
func TestCheckPremises_AllOK(t *testing.T) {
	f := selfFixtures(t)
	findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, sessionSelfOct18)

	for _, fd := range findings {
		if !fd.OK {
			t.Errorf("実測の応答で NG になった: %s", fd.Text)
		}
	}
	joined := strings.Join(findingTexts(findings), "\n")
	for _, want := range []string{
		"rex_version: 0.0.1792158932-g88fd41",
		"built_at 2026-10-16T13:55:32Z",
		"exit 3",
		"owner.pid",
		"argv0",
		"com.superlogical.terminal.shell",
		"program_status_changed",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("finding に %q が無い:\n%s", want, joined)
		}
	}
	f.assertCalls(t,
		"--autostart=false version --json",
		"--autostart=false block call -b "+probeMissingBlock+" program_status",
		"--autostart=false block call -b "+blockSelfOct18+" program_status",
		"--autostart=false block call -b "+blockSelfOct18+" process",
		"--autostart=false block ls -s "+sessionSelfOct18+" --json",
		"--autostart=false block inspect "+blockSelfOct18+" --json",
	)
}

// 欄が欠けた応答は、その欄を名指しした NG になる。他の点検は巻き込まれない。
func TestCheckPremises_NamesMissingField(t *testing.T) {
	tests := []struct {
		name    string
		degrade func(t *testing.T, f *fakeRex)
		wantNG  string // NG の finding に含まれる文言
	}{
		{"version --json が失敗", func(t *testing.T, f *fakeRex) { f.put(t, "fail-version.json", "") }, "version --json"},
		{"version --json に rex_version が無い", func(t *testing.T, f *fakeRex) { f.put(t, "version.json", `{"platform":"darwin_arm64"}`) }, "rex_version"},
		{"存在しない block への block call が exit 1", func(t *testing.T, f *fakeRex) { f.put(t, "fail-program_status-"+probeMissingBlock+".json", "") }, "exit 3"},
		{"存在しない block への block call が成功してしまう", func(t *testing.T, f *fakeRex) { f.put(t, "program_status-"+probeMissingBlock+".json", psEmpty) }, "exit 3"},
		{"root record に owner.pid が無い", func(t *testing.T, f *fakeRex) { f.put(t, "program_status-"+blockSelfOct18+".json", psNoOwner) }, "owner.pid"},
		{"root record が無い", func(t *testing.T, f *fakeRex) { f.put(t, "program_status-"+blockSelfOct18+".json", psEmpty) }, "root record"},
		{"process の foreground に cwd が無い", func(t *testing.T, f *fakeRex) {
			f.put(t, "process-"+blockSelfOct18+".json", `{"foreground":{"pid":29795,"argv0":"claude"}}`)
		}, "cwd"},
		{"process の foreground が無い", func(t *testing.T, f *fakeRex) { f.put(t, "process-"+blockSelfOct18+".json", `{"foreground":null}`) }, "foreground"},
		{"block ls に自分の block が無い", func(t *testing.T, f *fakeRex) { f.put(t, "block-ls-"+sessionSelfOct18+".json", blockLsJSON()) }, "自分の block"},
		{"自分の block の flavor が shell でない", func(t *testing.T, f *fakeRex) {
			f.put(t, "block-ls-"+sessionSelfOct18+".json", blockLsJSON([2]string{blockSelfOct18, "com.superlogical.browser"}))
		}, "flavor"},
		{"inspect に program_status_changed が無い", func(t *testing.T, f *fakeRex) {
			f.put(t, "inspect-"+blockSelfOct18+".json", `{"api":{"methods":{"program_status":{},"process":{}},"events":{"process_changed":{}}}}`)
		}, "program_status_changed"},
		{"inspect に process の method が無い", func(t *testing.T, f *fakeRex) {
			f.put(t, "inspect-"+blockSelfOct18+".json", `{"api":{"methods":{"program_status":{}},"events":{"program_status_changed":{}}}}`)
		}, "process"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := selfFixtures(t)
			tt.degrade(t, f)
			findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, sessionSelfOct18)

			ng := 0
			for _, fd := range findings {
				if fd.OK {
					continue
				}
				ng++
				if !strings.Contains(fd.Text, tt.wantNG) {
					t.Errorf("NG の finding %q に %q が無い", fd.Text, tt.wantNG)
				}
			}
			if ng != 1 {
				t.Errorf("NG の件数 = %d, want 1:\n%s", ng, strings.Join(findingTexts(findings), "\n"))
			}
		})
	}
}

// preview.16 で version --json に増えた api_revision は、あるときだけ出して server (api call server.status) の
// api_revision と比べる。無い版 (Oct18) では出さず、server にも打たない。
func TestCheckPremises_APIRevision(t *testing.T) {
	t.Run("CLI と server が同じ版なら OK に api_revision を出す", func(t *testing.T) {
		f := selfFixtures(t)
		f.put(t, "version.json", measured(t, measuredOct20, "version.json"))
		f.put(t, "server-status.json", measured(t, measuredOct20, "server_status.json"))
		findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, sessionSelfOct18)

		joined := strings.Join(findingTexts(findings), "\n")
		if !strings.Contains(joined, "api_revision 4") || strings.Contains(joined, "NG: ") {
			t.Errorf("api_revision 4 の OK が無いか NG がある:\n%s", joined)
		}
		calls := readLines(t, filepath.Join(f.dir, "calls.log"))
		if !slices.Contains(calls, "'--autostart=false' 'api' 'call' 'server.status' ") {
			t.Errorf("api call server.status を打っていない:\n%s", strings.Join(calls, "\n"))
		}
	})

	t.Run("CLI と server の api_revision が食い違えば NG 1 件", func(t *testing.T) {
		f := selfFixtures(t)
		f.put(t, "version.json", measured(t, measuredOct20, "version.json"))
		f.put(t, "server-status.json", `{"pid":31416,"api_revision":3}`)
		findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, sessionSelfOct18)

		var ng []string
		for _, fd := range findings {
			if !fd.OK {
				ng = append(ng, fd.Text)
			}
		}
		if len(ng) != 1 || !strings.Contains(ng[0], "api_revision") || !strings.Contains(ng[0], "4") || !strings.Contains(ng[0], "3") {
			t.Errorf("NG = %q, want api_revision の 4 と 3 を名指しした 1 件", ng)
		}
	})

	t.Run("server status に api_revision が無ければ NG 1 件", func(t *testing.T) {
		f := selfFixtures(t)
		f.put(t, "version.json", measured(t, measuredOct20, "version.json"))
		f.put(t, "server-status.json", measured(t, measuredOct10, "server_status.json"))
		findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, sessionSelfOct18)

		var ng []string
		for _, fd := range findings {
			if !fd.OK {
				ng = append(ng, fd.Text)
			}
		}
		if len(ng) != 1 || !strings.Contains(ng[0], "api_revision") {
			t.Errorf("NG = %q, want api_revision を名指しした 1 件", ng)
		}
	})

	t.Run("CLI に api_revision が無い版では出さず server にも打たない", func(t *testing.T) {
		f := selfFixtures(t)
		findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, sessionSelfOct18)

		joined := strings.Join(findingTexts(findings), "\n")
		if strings.Contains(joined, "api_revision") {
			t.Errorf("api_revision の無い版で api_revision に触れている:\n%s", joined)
		}
		for _, line := range readLines(t, filepath.Join(f.dir, "calls.log")) {
			if strings.Contains(line, "server.status") {
				t.Errorf("api_revision の無い版で server に打っている: %s", line)
			}
		}
	})
}

// session が分からなければ block ls の点検だけを飛ばし、理由を NG で残す。
func TestCheckPremises_WithoutSession(t *testing.T) {
	f := selfFixtures(t)
	findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18, "")
	joined := strings.Join(findingTexts(findings), "\n")
	if !strings.Contains(joined, "NG: ") || !strings.Contains(joined, "session") {
		t.Errorf("session 無しの finding が無い:\n%s", joined)
	}
	for _, line := range findingTexts(findings) {
		if strings.Contains(line, "block ls") && strings.HasPrefix(line, "OK") {
			t.Errorf("session 無しで block ls を点検している: %s", line)
		}
	}
}
