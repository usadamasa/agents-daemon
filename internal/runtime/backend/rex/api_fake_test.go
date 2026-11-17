package rex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/apitest"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/cli"
)

// fakeAPI は rex の状態から応答を組む fake の server と、それに繋ぐ Client を作る口。
type fakeAPI struct {
	*apitest.Server
	// rex は Client が CLI を起こすときの fake の rex。
	rex *fakeRex
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return &fakeAPI{Server: apitest.New(t), rex: newFakeRex(t)}
}

// payloadOf は method の最初の request の payload を v へ読む。
func (f *fakeAPI) payloadOf(t *testing.T, method string, v any) {
	t.Helper()
	reqs := f.RequestsOf(method)
	if len(reqs) == 0 {
		t.Fatalf("%s の request が無い", method)
	}
	if err := json.Unmarshal(reqs[0], v); err != nil {
		t.Fatalf("%s の payload を読めない: %v", method, err)
	}
}

func (f *fakeAPI) assertCalls(t *testing.T, want ...string) {
	t.Helper()
	got := f.Calls()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("API の calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// apiTimeout は fake の API への request の制限時間。本番と同じく呼び出しごとに ctx を切り、
// 切った ctx が接続を巻き込まないことをどのテストも通す。
const apiTimeout = 5 * time.Second

// client は fake の API と、fixture を何も置かない fake の rex に繋ぐ Client。CLI を叩く method は fake の rex が
// exit 3 で断る。無いパスの rex を渡すと Runner が既定の置き場から本物の rex を引き直して叩くので、必ず fake を渡す。
func (f *fakeAPI) client(resolve SessionResolver) *execClient {
	return f.clientWith(f.rex, resolve)
}

// clientWith は fake の rex (書き込みと購読) と fake の API (読み取り) の両方に繋ぐ Client。
func (f *fakeAPI) clientWith(rex *fakeRex, resolve SessionResolver) *execClient {
	return &execClient{rex: api.New(fixedSocket(f.Socket()), apiTimeout), cli: cli.New(rex.bin, noCommandTimeout), resolve: resolve}
}

func fixedSocket(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}
