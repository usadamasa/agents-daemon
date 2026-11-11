package parkhand

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

const (
	memoID  = "20261106T142558-e891c654"
	peerSID = "6b1f3c2a-1111-4222-8333-944455556666"
	newSID  = "0a0a0a0a-2222-4333-8444-955566667777"
	paneID  = "block:01a15e6a-7fe3-7298-af51-8982a808f03b"
	repo    = "/src/github.com/usadamasa/agents-daemon"
)

var memo = parking.Item{
	ID: memoID, Repo: repo, Kind: "scope", Title: "lint の閾値を見直す", Detail: "複雑度の上限が低い",
	Files: []string{".golangci.yml"}, DefaultTaken: "やらない", Origin: "adopted", Status: "open",
	Sources: []parking.Source{
		{SessionID: "s-new", Detail: "後から足した補足", At: time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)},
		{SessionID: "s-old", At: time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)},
	},
}

type setCall struct{ id, status, from, to string }

type fixture struct {
	sets      []setCall
	setErr    map[string]error // status → 失敗させる理由
	told      []string
	tellErr   error
	spawned   []string
	spawnRes  protocol.SpawnResult
	spawnErr  error
	missingID bool
}

func newFixture() *fixture {
	return &fixture{setErr: map[string]error{}, spawnRes: protocol.SpawnResult{SessionID: newSID, PaneID: paneID, Foreground: true}}
}

func (f *fixture) runner() Runner {
	return Runner{
		Memo: func(id string) (parking.Item, error) {
			if f.missingID || id != memoID {
				return parking.Item{}, errors.New("メモ " + id + " が無い")
			}
			return memo, nil
		},
		Set: func(id, status, from, to string) error {
			f.sets = append(f.sets, setCall{id, status, from, to})
			return f.setErr[status]
		},
		Tell: func(_ context.Context, sessionID, text string) (protocol.TellResult, error) {
			f.told = append(f.told, sessionID+"\n"+text)
			if f.tellErr != nil {
				return protocol.TellResult{}, f.tellErr
			}
			return protocol.TellResult{PaneID: paneID}, nil
		},
		Spawn: func(_ context.Context, repo, prompt string) (protocol.SpawnResult, error) {
			f.spawned = append(f.spawned, repo+"\n"+prompt)
			return f.spawnRes, f.spawnErr
		},
		SessionNames: func() map[string]string { return map[string]string{"s-new": "lint-review"} },
	}
}

func toSession() protocol.ParkHandRequest {
	return protocol.ParkHandRequest{ID: memoID, Intent: "task", ToSession: peerSID, Note: " 急ぎ "}
}

func toRepo() protocol.ParkHandRequest {
	return protocol.ParkHandRequest{ID: memoID, Intent: "issue", ToRepo: "/src/github.com/usadamasa/claude-config"}
}

// 生きているセッションへ: 取ってから tell で打ち、届けば handed のまま。
func TestRunner_Run_toSession(t *testing.T) {
	f := newFixture()
	res, err := f.runner().Run(context.Background(), toSession())
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.ParkHandResult{Delivered: true, Status: "handed", To: peerSID, PaneID: paneID}
	if res != want {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
	if len(f.sets) != 1 || f.sets[0] != (setCall{memoID, "handed", "open", parking.ToSession(peerSID)}) {
		t.Fatalf("sets = %+v", f.sets)
	}
	if len(f.told) != 1 || !strings.HasPrefix(f.told[0], peerSID+"\n") {
		t.Fatalf("told = %q", f.told)
	}
	text := strings.TrimPrefix(f.told[0], peerSID+"\n")
	for _, s := range []string{
		"メモ「lint の閾値を見直す」(種類: scope・adopted) をタスクにしてください。",
		"先に、メモの前提",
		"メモの id (" + memoID + ")",
		"詳細: 複雑度の上限が低い",
		"補足 (lint-review): 後から足した補足",
		"関係するファイル: .golangci.yml",
		"置いたときの既定: やらない",
		"作ったら基本すぐ着手してください。",
		"(メモ " + memoID + ")",
		"\n利用者からの補足: 急ぎ",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("文面に %q が無い:\n%s", s, text)
		}
	}
	if strings.Contains(text, "新しいセッションとして起こしました") {
		t.Errorf("セッションへ送る文面に起こした経緯がある:\n%s", text)
	}
}

// 届かなければ open に戻し、理由を返す。
func TestRunner_Run_toSession_undelivered(t *testing.T) {
	f := newFixture()
	f.tellErr = errors.New("prompt が開いている")
	res, err := f.runner().Run(context.Background(), toSession())
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered || res.Status != "open" || res.To != peerSID || !strings.Contains(res.Reason, "prompt が開いている") {
		t.Fatalf("res = %+v", res)
	}
	if len(f.sets) != 2 || f.sets[1] != (setCall{memoID, "open", "handed", ""}) {
		t.Fatalf("sets = %+v", f.sets)
	}
}

// 戻せなければ handed のまま残り、理由に戻せなかったことを続ける。
func TestRunner_Run_revertFails(t *testing.T) {
	f := newFixture()
	f.tellErr = errors.New("rex が無い")
	f.setErr["open"] = errors.New("書けない")
	res, err := f.runner().Run(context.Background(), toSession())
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered || res.Status != "handed" || !strings.Contains(res.Reason, "rex が無い") || !strings.Contains(res.Reason, "書けない") {
		t.Fatalf("res = %+v", res)
	}
}

// 取れなければ (ほかが先に渡した、捨てた) 何も送らず error。
func TestRunner_Run_claimFails(t *testing.T) {
	f := newFixture()
	f.setErr["handed"] = errors.New("status は handed (期待 open)")
	_, err := f.runner().Run(context.Background(), toSession())
	if err == nil || !strings.Contains(err.Error(), "期待 open") {
		t.Fatalf("err = %v", err)
	}
	if len(f.told)+len(f.spawned) != 0 || len(f.sets) != 1 {
		t.Fatalf("told = %v, spawned = %v, sets = %v", f.told, f.spawned, f.sets)
	}
}

// 新しいセッションへ: 取ってから spawn で起こし、claude が前景になれば届いたことになる。issue は起こした repo を起票先にする。
func TestRunner_Run_toRepo(t *testing.T) {
	f := newFixture()
	res, err := f.runner().Run(context.Background(), toRepo())
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.ParkHandResult{Delivered: true, Status: "handed", To: newSID, PaneID: paneID}
	if res != want {
		t.Fatalf("res = %+v, want %+v", res, want)
	}
	if len(f.sets) != 1 || f.sets[0].to != parking.ToRepo("/src/github.com/usadamasa/claude-config") {
		t.Fatalf("sets = %+v", f.sets)
	}
	prompt := strings.TrimPrefix(f.spawned[0], "/src/github.com/usadamasa/claude-config\n")
	for _, s := range []string{
		"このメモを渡すため、claude-config (/src/github.com/usadamasa/claude-config) で新しいセッションとして起こしました。\n",
		"メモ「lint の閾値を見直す」を GitHub の issue にしてください。repo: claude-config。",
		"成り立つときは起票し、URL を返してください。",
	} {
		if !strings.Contains(prompt, s) {
			t.Errorf("文面に %q が無い:\n%s", s, prompt)
		}
	}
	if strings.Contains(prompt, "利用者からの補足") {
		t.Errorf("補足が無いのに補足の行がある:\n%s", prompt)
	}
}

// 起きたか分からない (claude が前景にならなかった) メモは戻さない。戻すと押し直しで 2 つ目が起きる。
func TestRunner_Run_toRepo_unknown(t *testing.T) {
	f := newFixture()
	f.spawnRes = protocol.SpawnResult{SessionID: newSID, PaneID: paneID, Foreground: false, Detail: "15s の間に前景にならなかった"}
	res, err := f.runner().Run(context.Background(), toRepo())
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered || res.Status != "handed" || res.To != newSID || res.PaneID != paneID || res.Reason != "15s の間に前景にならなかった" {
		t.Fatalf("res = %+v", res)
	}
	if len(f.sets) != 1 {
		t.Fatalf("戻している: %+v", f.sets)
	}
}

// 起こせなければ open に戻す。
func TestRunner_Run_toRepo_fails(t *testing.T) {
	f := newFixture()
	f.spawnErr = errors.New("多重化ツールの session を作れない")
	res, err := f.runner().Run(context.Background(), toRepo())
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered || res.Status != "open" || res.To != "" || !strings.Contains(res.Reason, "多重化ツールの session を作れない") {
		t.Fatalf("res = %+v", res)
	}
	if len(f.sets) != 2 || f.sets[1].status != "open" {
		t.Fatalf("sets = %+v", f.sets)
	}
}

// 入力の検査は取る前に行う。
func TestRunner_Run_rejectsInput(t *testing.T) {
	cases := []struct {
		name string
		req  protocol.ParkHandRequest
		want string
	}{
		{"宛先が無い", protocol.ParkHandRequest{ID: memoID, Intent: "task"}, "宛先"},
		{"宛先が 2 つ", protocol.ParkHandRequest{ID: memoID, Intent: "task", ToSession: peerSID, ToRepo: repo}, "宛先"},
		{"intent が違う", protocol.ParkHandRequest{ID: memoID, Intent: "pr", ToSession: peerSID}, "intent"},
		{"メモが無い", protocol.ParkHandRequest{ID: "20261106T000000-00000000", Intent: "task", ToSession: peerSID}, "無い"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			_, err := f.runner().Run(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.sets) != 0 {
				t.Fatalf("取っている: %+v", f.sets)
			}
		})
	}
}

// 文面の雛形: task は TaskCreate を頼み、issue は起票を頼む。補足は空なら足さない。
func TestText(t *testing.T) {
	task := text(memo, "task", "agents-daemon", "", nil)
	if !strings.Contains(task, "TaskCreate で作り") || strings.Contains(task, "利用者からの補足") {
		t.Errorf("task:\n%s", task)
	}
	if !strings.Contains(task, "作ったら基本すぐ着手してください。") || !strings.Contains(task, "着手できない理由があるときだけ見送ってください。") {
		t.Errorf("task に着手の既定が無い:\n%s", task)
	}
	issue := text(memo, "issue", "agents-daemon", "URL を貼って", nil)
	if !strings.Contains(issue, "repo: agents-daemon。") || !strings.HasSuffix(issue, "\n利用者からの補足: URL を貼って") {
		t.Errorf("issue:\n%s", issue)
	}
	// 名前の無いセッションは ID で書く。
	if !strings.Contains(task, "補足 (s-new): 後から足した補足") {
		t.Errorf("名前の無いセッションの補足:\n%s", task)
	}
}
