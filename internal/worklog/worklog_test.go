package worklog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)

// cc-premise: 2.1.319 hooks UserPromptSubmit / Stop / SessionStart / SessionEnd の入力の欄 (probe plugin の実測)
func hook(event string, fields ...string) []byte {
	parts := append([]string{`"session_id": "s1"`, `"cwd": "/w"`, `"hook_event_name": "` + event + `"`}, fields...)
	return []byte("{" + strings.Join(parts, ", ") + "}")
}

func mustIngest(t *testing.T, s Store, input []byte, at time.Time) Result {
	t.Helper()
	res, err := s.Ingest(input, at)
	if err != nil {
		t.Fatalf("Ingest(%s) error = %v", input, err)
	}
	return res
}

func mustGet(t *testing.T, s Store, id string) Record {
	t.Helper()
	rec, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%s) = 無い", id)
	}
	return rec
}

func TestIngest_UserPromptSubmitが記録を作り最初のpromptを題にする(t *testing.T) {
	s := New(t.TempDir())
	res := mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "README の表を直して\n2 行目は無視"`), now)
	if !res.Created || res.SessionID != "s1" || res.Event != "UserPromptSubmit" {
		t.Errorf("Result = %+v, want Created", res)
	}
	got := mustGet(t, s, "s1")
	if got.Subject != "README の表を直して" || got.Origin != OriginPrompt || got.Turns != 1 || got.CWD != "/w" {
		t.Errorf("Get() = %+v", got)
	}
	if !got.StartedAt.Equal(now) || !got.UpdatedAt.Equal(now) || got.EndedAt != nil {
		t.Errorf("時刻 = %+v", got)
	}

	// 2 つ目の prompt は題を変えず、turns だけ進む。
	res = mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "次は lint"`), now.Add(time.Minute))
	if res.Created {
		t.Error("2 回目で Created")
	}
	got = mustGet(t, s, "s1")
	if got.Subject != "README の表を直して" || got.Turns != 2 || !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("2 回目の Get() = %+v", got)
	}
}

// peer の delivery は cross-session-message の包みごと prompt に入る (実測)。題は包みを剥いた本文の 1 行目。
// cc-premise: 2.1.319 peer-messaging delivery の prompt は <cross-session-message …>\n本文\n</cross-session-message>
func TestIngest_peerのdeliveryは包みを剥いて題にする(t *testing.T) {
	s := New(t.TempDir())
	prompt := `<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"workspace-fb\" from-mode=\"prompting\">\n` +
		`usadamasa/agents-daemon の issue #368 を設計から進めてほしい。\n- まず測る\n</cross-session-message>`
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "`+prompt+`"`), now)
	if got := mustGet(t, s, "s1").Subject; got != "usadamasa/agents-daemon の issue #368 を設計から進めてほしい。" {
		t.Errorf("Subject = %q", got)
	}
}

func TestIngest_題は空行を飛ばして120字で切る(t *testing.T) {
	s := New(t.TempDir())
	long := strings.Repeat("あ", 130)
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "\n\n  `+long+`"`), now)
	got := mustGet(t, s, "s1").Subject
	if len([]rune(got)) != 121 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "あああ") {
		t.Errorf("Subject = %q (%d runes)", got, len([]rune(got)))
	}
}

// 空の prompt (包みだけ、空白だけ) では題を書かず、次の prompt が題になる。
func TestIngest_空のpromptは題にしない(t *testing.T) {
	s := New(t.TempDir())
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "   "`), now)
	got := mustGet(t, s, "s1")
	if got.Subject != "" || got.Origin != "" || got.Turns != 1 {
		t.Errorf("Get() = %+v", got)
	}
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "本題"`), now)
	if got := mustGet(t, s, "s1"); got.Subject != "本題" || got.Origin != OriginPrompt {
		t.Errorf("Get() = %+v", got)
	}
}

func TestIngest_Stopはsummaryを書き記録が無ければ作る(t *testing.T) {
	s := New(t.TempDir())
	res := mustIngest(t, s, hook("Stop", `"last_assistant_message": "直したのん。\n次は lint。"`), now)
	if !res.Created {
		t.Errorf("Result = %+v, want Created", res)
	}
	got := mustGet(t, s, "s1")
	if got.Summary != "直したのん。" || got.Turns != 0 || got.Subject != "" {
		t.Errorf("Get() = %+v", got)
	}
	mustIngest(t, s, hook("Stop", `"last_assistant_message": "lint も通った"`), now.Add(time.Minute))
	if got := mustGet(t, s, "s1"); got.Summary != "lint も通った" || !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("Get() = %+v", got)
	}
}

// 最後の prompt と最後の Stop の時刻を残す。依存図が pane の写しより新しい prompt を見分けるのに使う (#469)。
func TestIngest_最後のpromptとStopの時刻を残す(t *testing.T) {
	s := New(t.TempDir())
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "p"`), now)
	got := mustGet(t, s, "s1")
	if got.PromptedAt == nil || !got.PromptedAt.Equal(now) || got.StoppedAt != nil {
		t.Fatalf("prompt の後 = prompted %v / stopped %v", got.PromptedAt, got.StoppedAt)
	}
	mustIngest(t, s, hook("Stop", `"last_assistant_message": "済んだ"`), now.Add(time.Minute))
	got = mustGet(t, s, "s1")
	if !got.PromptedAt.Equal(now) || got.StoppedAt == nil || !got.StoppedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("Stop の後 = prompted %v / stopped %v", got.PromptedAt, got.StoppedAt)
	}
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "次"`), now.Add(2*time.Minute))
	if got := mustGet(t, s, "s1"); !got.PromptedAt.Equal(now.Add(2*time.Minute)) || !got.StoppedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("2 つ目の prompt の後 = prompted %v / stopped %v", got.PromptedAt, got.StoppedAt)
	}
}

// SessionStart と SessionEnd は記録があるときだけ書く (prompt を打たずに終わったセッションの記録を残さない)。
func TestIngest_SessionStartとSessionEndは記録が無ければ何もしない(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	for _, in := range [][]byte{hook("SessionStart", `"source": "startup"`), hook("SessionEnd", `"reason": "prompt_input_exit"`)} {
		if res := mustIngest(t, s, in, now); res.Created || res.Written {
			t.Errorf("Result = %+v, want 書かない", res)
		}
	}
	if _, ok := s.Get("s1"); ok {
		t.Error("記録が作られた")
	}
}

func TestIngest_SessionEndはendedを書きresumeのSessionStartが消す(t *testing.T) {
	s := New(t.TempDir())
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "p"`), now)
	res := mustIngest(t, s, hook("SessionEnd", `"reason": "prompt_input_exit"`), now.Add(time.Hour))
	if !res.Written || !res.Ended {
		t.Errorf("Result = %+v, want Ended", res)
	}
	got := mustGet(t, s, "s1")
	if got.EndedAt == nil || !got.EndedAt.Equal(now.Add(time.Hour)) || got.EndReason != "prompt_input_exit" || !Ended(got) {
		t.Errorf("Get() = %+v", got)
	}
	// Stop は ended に触れない (EndedStays)。
	mustIngest(t, s, hook("Stop", `"last_assistant_message": "遅れた"`), now.Add(2*time.Hour))
	if got := mustGet(t, s, "s1"); !Ended(got) || got.Summary != "遅れた" {
		t.Errorf("Stop の後 = %+v", got)
	}
	// startup の SessionStart も消さない。resume だけが消す。
	mustIngest(t, s, hook("SessionStart", `"source": "startup"`), now.Add(3*time.Hour))
	if got := mustGet(t, s, "s1"); !Ended(got) {
		t.Errorf("startup の後 = %+v", got)
	}
	mustIngest(t, s, hook("SessionStart", `"source": "resume"`), now.Add(3*time.Hour))
	if got := mustGet(t, s, "s1"); Ended(got) || got.EndReason != "" {
		t.Errorf("resume の後 = %+v", got)
	}
}

func TestSet_人の題とcompanionの題(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Set("s1", Change{Subject: new("x")}, now); err == nil {
		t.Error("記録の無いセッションの Set が通った")
	}
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "最初の prompt"`), now)

	rec, err := s.Set("s1", Change{Inferred: new("推定の題")}, now.Add(time.Minute))
	if err != nil || rec.Inferred != "推定の題" || rec.Subject != "最初の prompt" || rec.Origin != OriginPrompt {
		t.Errorf("Set(inferred) = %+v, %v", rec, err)
	}
	if Title(rec) != "推定の題" {
		t.Errorf("Title() = %q, want 推定の題 (prompt より推定を見せる)", Title(rec))
	}

	rec, err = s.Set("s1", Change{Subject: new("人が直した題")}, now.Add(2*time.Minute))
	if err != nil || rec.Subject != "人が直した題" || rec.Origin != OriginHuman || rec.Inferred != "推定の題" {
		t.Errorf("Set(subject) = %+v, %v", rec, err)
	}
	if Title(rec) != "人が直した題" {
		t.Errorf("Title() = %q, want 人が直した題", Title(rec))
	}
	// 人が直した後の hook と companion は題を書き換えない (HumanSubjectKept)。
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "別の prompt"`), now.Add(3*time.Minute))
	if _, err := s.Set("s1", Change{Inferred: new("別の推定")}, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, s, "s1")
	if got.Subject != "人が直した題" || got.Origin != OriginHuman || Title(got) != "人が直した題" || got.Inferred != "別の推定" {
		t.Errorf("Get() = %+v", got)
	}
	if _, err := s.Set("s1", Change{Subject: new("  ")}, now); err == nil {
		t.Error("空の題が通った")
	}
	if _, err := s.Set("s1", Change{}, now); err == nil {
		t.Error("何も変えない Set が通った")
	}
}

func TestTitle_無ければ空(t *testing.T) {
	if got := Title(Record{}); got != "" {
		t.Errorf("Title() = %q", got)
	}
	if got := Title(Record{Subject: "p", Origin: OriginPrompt}); got != "p" {
		t.Errorf("Title() = %q", got)
	}
}

// Dismiss は終了したと確かめられた記録だけを消す。
func TestDismiss(t *testing.T) {
	s := New(t.TempDir())
	exited := func(string, time.Time) error { return nil }
	if err := s.Dismiss("s1", exited); err == nil {
		t.Error("無い記録の Dismiss が通った")
	}
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "p"`), now)
	var askedAfter time.Time
	alive := func(_ string, after time.Time) error { askedAfter = after; return errors.New("生きている") }
	if err := s.Dismiss("s1", alive); err == nil || !strings.Contains(err.Error(), "生きている") || !askedAfter.Equal(now) {
		t.Errorf("Dismiss(alive) = %v (asked after %v)", err, askedAfter)
	}
	if _, ok := s.Get("s1"); !ok {
		t.Error("生きているセッションの記録が消えた")
	}
	if err := s.Dismiss("s1", exited); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("s1"); ok {
		t.Error("Dismiss の後に残っている")
	}
}

func TestIngest_rejects(t *testing.T) {
	tests := map[string][]byte{
		"壊れた JSON":           []byte(`{"session_id": `),
		"扱わない hook の入力":      hook("PostToolUse"),
		"session_id が無い":     []byte(`{"hook_event_name": "Stop"}`),
		"ファイル名に使えない session": []byte(`{"session_id": "../x", "hook_event_name": "Stop"}`),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := New(dir).Ingest(input, now); err == nil {
				t.Error("Ingest() error = nil, want error")
			}
			entries, _ := os.ReadDir(filepath.Join(dir, "work"))
			for _, e := range entries {
				if e.Name() != lockFile {
					t.Errorf("書かれたファイル = %v, want なし", e.Name())
				}
			}
		})
	}
	if _, err := (Store{}).Ingest(hook("Stop"), now); err == nil {
		t.Error("ゼロ値の Store の Ingest が通った")
	}
}

func TestList_Remove_Prune(t *testing.T) {
	s := New(t.TempDir())
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "a"`), now)
	mustIngest(t, s, []byte(`{"session_id": "s2", "cwd": "/w", "hook_event_name": "Stop", "last_assistant_message": "b"}`), now)
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].SessionID != "s1" || list[1].SessionID != "s2" {
		t.Errorf("List() = %+v, %v", list, err)
	}
	if err := s.Remove("s2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("s2"); ok {
		t.Error("Remove の後に残っている")
	}
	if err := s.Remove("s2"); err != nil {
		t.Errorf("無い記録の Remove = %v", err)
	}

	old := now.Add(-Retention - time.Hour)
	for _, name := range []string{"s1.json", "broken.json", lockFile} {
		if err := os.Chtimes(filepath.Join(s.Dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Prune(now)
	if err != nil || n != 2 {
		t.Errorf("Prune() = %d, %v, want 2 (記録と壊れたファイル。lock は消さない)", n, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, lockFile)); err != nil {
		t.Errorf("lock が消えた: %v", err)
	}
	if n, err := (Store{}).Prune(now); err != nil || n != 0 {
		t.Errorf("ゼロ値の Prune() = %d, %v", n, err)
	}
	if list, err := (Store{}).List(); list != nil || err != nil {
		t.Errorf("ゼロ値の List() = %v, %v", list, err)
	}
}

// Touch した記録は Prune で消えず、中身も変わらない。触っていない記録は消え、無い記録は作らない。
func TestTouch_keepsRecordFromPrune(t *testing.T) {
	s := New(t.TempDir())
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "a"`), now)
	mustIngest(t, s, []byte(`{"session_id": "s2", "cwd": "/w", "hook_event_name": "Stop", "last_assistant_message": "b"}`), now)
	before := mustGet(t, s, "s1")

	old := now.Add(-Retention - time.Hour)
	for _, name := range []string{"s1.json", "s2.json"} {
		if err := os.Chtimes(filepath.Join(s.Dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Touch([]string{"s1", "absent"}, now); err != nil {
		t.Fatalf("Touch() = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "absent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("無い記録を作った (err=%v)", err)
	}
	if after := mustGet(t, s, "s1"); !reflect.DeepEqual(before, after) {
		t.Errorf("Touch で中身が変わった: %+v → %+v", before, after)
	}

	n, err := s.Prune(now)
	if err != nil || n != 1 {
		t.Errorf("Prune() = %d, %v, want 1 (触っていない s2 だけ)", n, err)
	}
	if _, ok := s.Get("s1"); !ok {
		t.Error("Touch した記録が消えた")
	}
	if _, ok := s.Get("s2"); ok {
		t.Error("触っていない記録が残った")
	}
}

func TestTouch_rejectsInvalidIDAndContinues(t *testing.T) {
	s := New(t.TempDir())
	mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "a"`), now)
	old := now.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(s.Dir, "s1.json"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch([]string{"../escape", "s1"}, now); err == nil {
		t.Error("ファイル名に使えない ID の Touch が通った")
	}
	info, err := os.Stat(filepath.Join(s.Dir, "s1.json"))
	if err != nil || !info.ModTime().Equal(now) {
		t.Errorf("後ろの ID を触っていない: %v, %v", info, err)
	}
}

func TestTouch_zeroStore(t *testing.T) {
	if err := (Store{}).Touch([]string{"s1"}, now); err != nil {
		t.Errorf("ゼロ値の Touch() = %v", err)
	}
}
