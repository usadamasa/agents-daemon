package tasklog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
)

// testdata/*.json は Claude Code 2.1.302 の PostToolUse hook に届いた入力の実測 (パスだけ置き換えた)。
const fixtureSession = "0842a96d-f680-5b48-85cf-becc2edcaa49"

var t0 = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func allHome(string) domain.Domain { return domain.Home }
func allWork(string) domain.Domain { return domain.Work }

func ingest(t *testing.T, s Store, classify func(string) domain.Domain, names ...string) {
	t.Helper()
	for i, name := range names {
		if err := s.Ingest(fixture(t, name), classify, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Ingest(%s) error = %v", name, err)
		}
	}
}

func byID(t *testing.T, s Store, d domain.Domain) map[string]Task {
	t.Helper()
	tasks, err := s.List(d)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	m := map[string]Task{}
	for _, task := range tasks {
		m[task.ID] = task
	}
	return m
}

// 実測の入力の流れ (作成 2 件、blockedBy の追加、in_progress、completed) を書いて、List で読み戻す。
func TestIngest_realHookInputs(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, allHome, "task-create-alpha", "task-create-beta", "task-update-blocked-by", "task-update-in-progress", "task-update-completed")

	got := byID(t, s, domain.Home)
	if len(got) != 2 {
		t.Fatalf("タスクの数 = %d, want 2: %+v", len(got), got)
	}
	alpha, beta := got["1"], got["2"]
	if alpha.Subject != "alpha" || alpha.Status != "completed" || alpha.SessionID != fixtureSession {
		t.Errorf("alpha = %+v", alpha)
	}
	if alpha.CWD != "/home/u/src/github.com/usadamasa/agents-daemon" || alpha.Description != "alpha" {
		t.Errorf("alpha の cwd / description = %q / %q", alpha.CWD, alpha.Description)
	}
	if beta.Status != "pending" || len(beta.BlockedBy) != 1 || beta.BlockedBy[0] != "1" {
		t.Errorf("beta = %+v", beta)
	}
	if !alpha.UpdatedAt.Equal(t0.Add(4 * time.Minute)) {
		t.Errorf("alpha.UpdatedAt = %v, want 最後の更新の時刻", alpha.UpdatedAt)
	}
}

// TaskList の結果で全件を同期し直す。完了した blocker は blockedBy から外れる (実測)。
func TestIngest_taskListResyncs(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, allHome, "task-create-alpha", "task-create-beta", "task-update-blocked-by", "task-list")

	got := byID(t, s, domain.Home)
	if got["1"].Status != "completed" {
		t.Errorf("TaskList の status が反映されていない: %+v", got["1"])
	}
	if len(got["2"].BlockedBy) != 0 {
		t.Errorf("TaskList の blockedBy で置き換わっていない: %+v", got["2"])
	}
	// TaskList に無い欄 (description) は残す。
	if got["1"].Description != "alpha" {
		t.Errorf("description が消えた: %+v", got["1"])
	}
}

// hook を入れる前に作られたタスクも、TaskList で記録に入り、一覧から消えたものは記録から消える。
func TestIngest_taskListAddsAndRemoves(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, allHome, "task-create-alpha", "task-create-beta")
	input := withTool(t, fixture(t, "task-list"), "TaskList", nil,
		map[string]any{"tasks": []map[string]any{{"id": "2", "subject": "beta", "status": "in_progress", "blockedBy": []string{}}, {"id": "7", "subject": "before hook", "status": "pending", "blockedBy": []string{}}}})
	if err := s.Ingest(input, allHome, t0); err != nil {
		t.Fatal(err)
	}

	got := byID(t, s, domain.Home)
	var ids []string
	for id := range got {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "2" || ids[1] != "7" {
		t.Errorf("ids = %v, want [2 7]", ids)
	}
	if got["7"].Subject != "before hook" {
		t.Errorf("TaskList だけで知ったタスク = %+v", got["7"])
	}
}

func TestIngest_updateCases(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		resp  map[string]any
		check func(t *testing.T, got map[string]Task)
	}{
		{
			name:  "deleted になったら記録を消す",
			input: map[string]any{"taskId": "1", "status": "deleted"},
			resp:  map[string]any{"success": true, "taskId": "1", "updatedFields": []string{"status"}},
			check: func(t *testing.T, got map[string]Task) {
				if _, ok := got["1"]; ok {
					t.Errorf("deleted のタスクが残っている")
				}
			},
		},
		{
			name:  "失敗した更新は反映しない",
			input: map[string]any{"taskId": "1", "status": "completed"},
			resp:  map[string]any{"success": false, "taskId": "1", "updatedFields": []string{}, "error": "Task not found"},
			check: func(t *testing.T, got map[string]Task) {
				if got["1"].Status != "pending" {
					t.Errorf("失敗した更新が反映された: %+v", got["1"])
				}
			},
		},
		{
			name:  "作成を記録していないタスクの更新は、分かる欄だけで作る",
			input: map[string]any{"taskId": "9", "status": "in_progress", "subject": "late"},
			resp:  map[string]any{"success": true, "taskId": "9", "updatedFields": []string{"status", "subject"}},
			check: func(t *testing.T, got map[string]Task) {
				if got["9"].Status != "in_progress" || got["9"].Subject != "late" {
					t.Errorf("task 9 = %+v", got["9"])
				}
			},
		},
		{
			name:  "同じ blocker を二度足しても 1 つ",
			input: map[string]any{"taskId": "1", "addBlockedBy": []string{"5", "5"}, "owner": "teammate"},
			resp:  map[string]any{"success": true, "taskId": "1", "updatedFields": []string{"blockedBy", "owner"}},
			check: func(t *testing.T, got map[string]Task) {
				if len(got["1"].BlockedBy) != 1 || got["1"].Owner != "teammate" {
					t.Errorf("task 1 = %+v", got["1"])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(t.TempDir())
			ingest(t, s, allHome, "task-create-alpha")
			if err := s.Ingest(withTool(t, fixture(t, "task-update-completed"), "TaskUpdate", tc.input, tc.resp), allHome, t0); err != nil {
				t.Fatal(err)
			}
			tc.check(t, byID(t, s, domain.Home))
		})
	}
}

func TestIngest_todoWrite(t *testing.T) {
	s := New(t.TempDir())
	todos := []map[string]any{{"content": "a", "status": "completed", "activeForm": "doing a"}, {"content": "b", "status": "pending", "activeForm": "doing b"}}
	input := withTool(t, fixture(t, "task-list"), "TodoWrite", map[string]any{"todos": todos}, map[string]any{"oldTodos": []any{}, "newTodos": todos})
	if err := s.Ingest(input, allHome, t0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "home", fixtureSession, "todos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list TodoList
	if err := json.Unmarshal(data, &list); err != nil || len(list.Todos) != 2 || list.Todos[1].Content != "b" {
		t.Errorf("todos.json = %s (%v)", data, err)
	}
	// todos.json はタスクの一覧に混ざらない。
	if tasks, _ := s.List(domain.Home); len(tasks) != 0 {
		t.Errorf("List に todos.json が混ざった: %+v", tasks)
	}
}

func TestIngest_ignoresOtherToolsAndBadIDs(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ingest(withTool(t, fixture(t, "task-list"), "Bash", map[string]any{"command": "ls"}, map[string]any{}), allHome, t0); err != nil {
		t.Errorf("Task 系でないツールでエラー: %v", err)
	}
	if _, err := os.Stat(s.Dir); err == nil {
		t.Errorf("Task 系でないツールで何かを書いた")
	}

	bad := withSession(t, fixture(t, "task-create-alpha"), "../escape")
	if err := s.Ingest(bad, allHome, t0); err == nil {
		t.Errorf("ディレクトリの外へ出る session ID を受け付けた")
	}
	badTask := withTool(t, fixture(t, "task-create-alpha"), "TaskCreate", map[string]any{"subject": "x"}, map[string]any{"task": map[string]any{"id": "../x", "subject": "x"}})
	if err := s.Ingest(badTask, allHome, t0); err == nil {
		t.Errorf("ディレクトリの外へ出る task ID を受け付けた")
	}
}

// 私用と業務を分けて置く。1 つのセッションが両方を触ったら業務に倒す。
func TestIngest_domains(t *testing.T) {
	t.Run("domain ごとに分けて置く", func(t *testing.T) {
		s := New(t.TempDir())
		ingest(t, s, allWork, "task-create-alpha")
		if len(byID(t, s, domain.Home)) != 0 || len(byID(t, s, domain.Work)) != 1 {
			t.Errorf("業務のタスクが業務の側にだけ無い")
		}
	})
	t.Run("私用で始めたセッションが業務を触ったら、記録ごと業務へ移す", func(t *testing.T) {
		s := New(t.TempDir())
		ingest(t, s, allHome, "task-create-alpha")
		ingest(t, s, allWork, "task-create-beta")
		if home := byID(t, s, domain.Home); len(home) != 0 {
			t.Errorf("私用の側に残っている: %+v", home)
		}
		if work := byID(t, s, domain.Work); len(work) != 2 {
			t.Errorf("業務の側 = %+v, want 2 件", work)
		}
	})
	t.Run("業務の記録があるセッションは、私用の cwd でも業務に書く", func(t *testing.T) {
		s := New(t.TempDir())
		ingest(t, s, allWork, "task-create-alpha")
		ingest(t, s, allHome, "task-create-beta")
		if home := byID(t, s, domain.Home); len(home) != 0 {
			t.Errorf("私用の側に書いた: %+v", home)
		}
	})
}

// withTool は fixture の hook 入力のツールと入出力を差し替える。nil は元のまま。
func withTool(t *testing.T, base []byte, tool string, input, resp any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	m["tool_name"] = tool
	if input != nil {
		m["tool_input"] = input
	}
	if resp != nil {
		m["tool_response"] = resp
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func withSession(t *testing.T, base []byte, sessionID string) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	m["session_id"] = sessionID
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestListTodosAndHasSession(t *testing.T) {
	s := New(t.TempDir())
	todos := []map[string]any{{"content": "a", "status": "in_progress", "activeForm": "doing a"}}
	input := withTool(t, fixture(t, "task-list"), "TodoWrite", map[string]any{"todos": todos}, map[string]any{})
	if err := s.Ingest(input, allWork, t0); err != nil {
		t.Fatal(err)
	}
	lists, err := s.ListTodos(domain.Work)
	if err != nil || len(lists) != 1 || lists[0].SessionID != fixtureSession || lists[0].Todos[0].Content != "a" {
		t.Errorf("ListTodos(work) = %+v, %v", lists, err)
	}
	if lists, _ := s.ListTodos(domain.Home); len(lists) != 0 {
		t.Errorf("ListTodos(home) = %+v, want 空", lists)
	}
	if !s.HasSession(domain.Work, fixtureSession) || s.HasSession(domain.Home, fixtureSession) {
		t.Errorf("HasSession が記録の置き場と合わない")
	}
	if s.HasSession(domain.Work, "../x") || (Store{}).HasSession(domain.Work, fixtureSession) {
		t.Errorf("不正な session ID かゼロ値の Store で true")
	}
}
