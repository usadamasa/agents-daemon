package rex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/premise"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// 2026-10-18 の実測で doctor を打った自分の block と session (testdata の measuredOct18 の応答はこの ID のもの)。
const (
	blockSelfOct18   = "block:01a14d5d-0dea-72fd-938b-169746065cb2"
	sessionSelfOct18 = "session:01a14d5d-0dea-71ec-97b7-ece67015d584"
	// sessionOther は自分の session と別の session。
	sessionOther = "session:01a14d5d-0000-7000-8000-000000000003"
)

// fakeRexPath は包みが叩く rex として premise に見せるパス。
const fakeRexPath = "/fake/Versions/Current/rex"

// errRexMissing は包みが叩く rex が見つからないときの失敗。
var errRexMissing = errors.New("rex が PATH にも既定の置き場にも無い")

// selfFixtures は実測どおりの応答を全部そろえた fake の API を返す。テストはここから 1 つずつ崩す。
// program_status、process は 10-18 の実測、describe_block と modes は 10-20 の実測 (CLI の block inspect は describe_block の
// 応答に session_id を足した形)、server.version は 11-18 の実測。
func selfFixtures(t *testing.T) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.AddSession(sessionSelfOct18, [2]string{blockSelfOct18, wire.ShellFlavor})
	f.PutBlock(blockSelfOct18, measured(t, measuredOct18, "program_status.json"), measured(t, measuredOct18, "process.json"))
	f.Put("describe_block", blockSelfOct18, measured(t, measuredOct20, "block-inspect.json"))
	f.Put("modes", blockSelfOct18, measured(t, measuredOct20, "modes.json"))
	f.SetVersion(measured(t, measuredNov18, "server-version.json"))
	saved := rexBinary
	rexBinary = func() (string, error) { return fakeRexPath, nil }
	t.Cleanup(func() { rexBinary = saved })
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

func ngTexts(findings []multiplexer.PremiseFinding) []string {
	var ng []string
	for _, f := range findings {
		if !f.OK {
			ng = append(ng, f.Text)
		}
	}
	return ng
}

// 実測どおりの応答なら全部 OK で、打つ method は api-checks.md の「doctor が見る行」と同じ。
// 自分の session は自分の block から target.resolve で引く (rex は新しい端末に REX_SESSION を入れない)。
func TestCheckPremises_AllOK(t *testing.T) {
	f := selfFixtures(t)
	findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18)

	for _, fd := range findings {
		if !fd.OK {
			t.Errorf("実測の応答で NG になった: %s", fd.Text)
		}
	}
	joined := strings.Join(findingTexts(findings), "\n")
	for _, want := range []string{
		"rex_version: 0.1.0-preview.19",
		"built_at 2026-11-13T19:49:36Z",
		"api_revision 14",
		fakeRexPath, // 包みが叩く rex (rex の自動更新で Versions/ のどれを引いたか)
		"not_found",
		"session_not_found",
		"owner.pid",
		"argv0",
		"com.superlogical.terminal.shell",
		"program_status_changed",
		"alternate",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("finding に %q が無い:\n%s", want, joined)
		}
	}
	f.assertCalls(t,
		"server.version",
		"target.resolve "+blockSelfOct18,
		"target.resolve "+premise.ProbeMissingBlock,
		"program_status "+premise.ProbeMissingBlock,
		"session.list_blocks "+premise.ProbeMissingSession,
		"program_status "+blockSelfOct18,
		"process "+blockSelfOct18,
		"session.list_blocks "+sessionSelfOct18,
		"session.describe_block "+blockSelfOct18,
		"modes "+blockSelfOct18,
	)
}

// 欄が欠けた応答や想定と違う失敗は、それを名指しした NG になる。他の点検は巻き込まれない。
func TestCheckPremises_NamesMissingField(t *testing.T) {
	tests := []struct {
		name    string
		degrade func(t *testing.T, f *fakeAPI)
		wantNG  string // NG の finding に含まれる文言
	}{
		{"server.version が失敗", func(t *testing.T, f *fakeAPI) { f.SetVersion("") }, "server.version"},
		{"server.version に rex_version が無い", func(t *testing.T, f *fakeAPI) { f.SetVersion(`{"platform":"darwin_arm64","api_revision":14}`) }, "rex_version"},
		{"server.version に api_revision が無い", func(t *testing.T, f *fakeAPI) {
			f.SetVersion(`{"rex_version":"0.1.0-preview.1","built_at":"2026-10-01T00:00:00Z"}`)
		}, "api_revision"},
		{"server の api_revision が daemon より古い", func(t *testing.T, f *fakeAPI) {
			f.SetVersion(`{"rex_version":"0.1.0-preview.1","built_at":"2026-10-01T00:00:00Z","api_revision":13}`)
		}, "13"},
		{"server の api_min_revision が daemon より新しい", func(t *testing.T, f *fakeAPI) {
			f.SetVersion(`{"rex_version":"0.2.0","built_at":"2027-01-01T00:00:00Z","api_revision":20,"api_min_revision":15}`)
		}, "api_min_revision"},
		{"包みが叩く rex が見つからない", func(t *testing.T, _ *fakeAPI) {
			rexBinary = func() (string, error) { return "", errRexMissing }
		}, "包み"},
		{"存在しない block が target.resolve で解決する", func(t *testing.T, f *fakeAPI) {
			f.AddUnlisted(sessionOther, [2]string{premise.ProbeMissingBlock, wire.ShellFlavor})
		}, "存在しない block"},
		{"存在しない block への block の method が成功してしまう", func(t *testing.T, f *fakeAPI) { f.Put("program_status", premise.ProbeMissingBlock, psEmpty) }, "存在しない block"},
		{"存在しない session の list_blocks が成功してしまう", func(t *testing.T, f *fakeAPI) { f.AddUnlisted(premise.ProbeMissingSession) }, "存在しない session"},
		{"root record に owner.pid が無い", func(t *testing.T, f *fakeAPI) { f.Put("program_status", blockSelfOct18, psNoOwner) }, "owner.pid"},
		{"root record が無い", func(t *testing.T, f *fakeAPI) { f.Put("program_status", blockSelfOct18, psEmpty) }, "root record"},
		{"process の foreground に cwd が無い", func(t *testing.T, f *fakeAPI) {
			f.Put("process", blockSelfOct18, `{"foreground":{"pid":29795,"argv0":"claude"}}`)
		}, "cwd"},
		{"process の foreground が無い", func(t *testing.T, f *fakeAPI) { f.Put("process", blockSelfOct18, `{"foreground":null}`) }, "foreground"},
		{"list_blocks に自分の block が無い", func(t *testing.T, f *fakeAPI) { f.SetBlocks(sessionSelfOct18) }, "自分の block"},
		{"自分の block の flavor が shell でない", func(t *testing.T, f *fakeAPI) {
			f.SetBlocks(sessionSelfOct18, [2]string{blockSelfOct18, "com.superlogical.browser"})
		}, "flavor"},
		{"describe_block に program_status_changed が無い", func(t *testing.T, f *fakeAPI) {
			f.Put("describe_block", blockSelfOct18, `{"api":{"methods":{"program_status":{},"process":{},"size":{},"modes":{}},"events":{"program_status_removed":{},"process_changed":{},"child_exited":{}}}}`)
		}, "program_status_changed"},
		{"describe_block に process の method が無い", func(t *testing.T, f *fakeAPI) {
			f.Put("describe_block", blockSelfOct18, `{"api":{"methods":{"program_status":{},"size":{},"modes":{}},"events":{"program_status_changed":{},"program_status_removed":{},"process_changed":{},"child_exited":{}}}}`)
		}, "process"},
		{"describe_block に size と modes の method が無い", func(t *testing.T, f *fakeAPI) {
			f.Put("describe_block", blockSelfOct18, measured(t, measuredOct18, "block-inspect.json"))
		}, "modes"},
		{"modes が失敗", func(t *testing.T, f *fakeAPI) { f.Fail("modes", blockSelfOct18) }, "modes"},
		{"自分の block の screen が primary", func(t *testing.T, f *fakeAPI) {
			f.Put("modes", blockSelfOct18, `{"screen":"primary","modes":[]}`)
		}, "primary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := selfFixtures(t)
			tt.degrade(t, f)
			findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18)

			ng := ngTexts(findings)
			if len(ng) != 1 {
				t.Fatalf("NG の件数 = %d, want 1:\n%s", len(ng), strings.Join(findingTexts(findings), "\n"))
			}
			if !strings.Contains(ng[0], tt.wantNG) {
				t.Errorf("NG の finding %q に %q が無い", ng[0], tt.wantNG)
			}
		})
	}
}

// 自分の block の session を引けなければ、その理由を NG 1 件で残し、session の要る点検を飛ばす。
func TestCheckPremises_UnresolvableSession(t *testing.T) {
	f := selfFixtures(t)
	f.SetBlocks(sessionSelfOct18)
	findings := f.client(staticResolver(nil)).CheckPremises(context.Background(), blockSelfOct18)
	ng := ngTexts(findings)
	if len(ng) != 1 || !strings.Contains(ng[0], "session") {
		t.Errorf("NG = %q, want session を引けない旨の 1 件", ng)
	}
	for _, line := range f.Calls() {
		if strings.HasPrefix(line, "program_status "+blockSelfOct18) || strings.HasPrefix(line, "session.describe_block") {
			t.Errorf("session を引けないのに block の点検を打った: %s", line)
		}
	}
}
