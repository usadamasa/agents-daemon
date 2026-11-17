package rex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/apitest"
)

// fakeAPI は rex の状態から応答を組む fake の server と、それに繋ぐ Client を作る口。
type fakeAPI struct {
	*apitest.Server
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return &fakeAPI{Server: apitest.New(t)}
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

// client は fake の API に繋ぐ Client。
func (f *fakeAPI) client(resolve SessionResolver) *execClient {
	return &execClient{rex: api.New(fixedSocket(f.Socket()), apiTimeout), resolve: resolve}
}

func fixedSocket(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}
