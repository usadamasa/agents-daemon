// Package tasklog は Claude Code のセッションのタスク (TaskCreate / TaskUpdate / TaskList、無効なときは
// TodoWrite) を、PostToolUse hook の入力から XDG state 配下へ記録する (#121)。読み手はタスクのボード。
//
//	tasks/<domain>/<session_id>/<task_id>.json   タスク 1 つ (1 タスク 1 ファイル)
//	tasks/<domain>/<session_id>/todos.json       TodoWrite のリスト全体
//
// 1 タスク 1 ファイルにするのは、同じセッションの Task 系ツールが並列で走っても別のタスクなら
// 別のファイルになり、読み直して書く処理が取り合わないため。domain (home / work) で置き場を分け、
// 私用と業務を同じボードに混ぜない (internal/domain)。
//
// 本体の ~/.claude/tasks/ は読まない。単独セッションの保存先と形式が docs に無く、内部形式に依存するため。
// 取りこぼした更新は、次に model が TaskList を呼んだときにその結果で全件を同期し直して埋める。
package tasklog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// 保持期間。消すのは daemon の housekeeping (Prune) で、mtime で判定する。
const (
	// CompletedRetention は完了したタスクを残す期間。ボードの「完了 (直近)」で今日の進み具合が
	// 見えれば足りる。
	CompletedRetention = 24 * time.Hour
	// Retention は 1 週間更新されなかったタスクと TodoWrite のリストを残す期間。終了したセッションの
	// 未完了のタスクを、その間にパーキングへ移すか捨てるかを決める。生きているセッションで消えても、
	// 次の TaskList で戻る。
	Retention = 7 * 24 * time.Hour
)

// todosFile は TodoWrite のリスト全体を置くファイル名。タスクのファイル (<task_id>.json) と区別する。
const todosFile = "todos.json"

// Task はタスク 1 つの記録。
type Task struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	ActiveForm  string   `json:"active_form,omitempty"`
	Status      string   `json:"status"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
	Blocks      []string `json:"blocks,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	SessionID   string   `json:"session_id"`
	// Repo は cwd のリポジトリの main worktree のパス (RepoRoot)。worktree のタスクも同じ値になり、
	// ボードの同じ行に入る。git の外なら空。
	Repo string `json:"repo,omitempty"`
	// AgentID はサブエージェントが作ったタスクのとき、そのエージェントの ID。main なら空。
	AgentID   string    `json:"agent_id,omitempty"`
	CWD       string    `json:"cwd"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Todo は TodoWrite の 1 項目。
type Todo struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

// TodoList は TodoWrite のリスト全体の記録。
type TodoList struct {
	SessionID string    `json:"session_id"`
	CWD       string    `json:"cwd"`
	Repo      string    `json:"repo,omitempty"`
	Todos     []Todo    `json:"todos"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store は tasks/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "tasks")}
}

// Resolver は hook 入力に無い欄を cwd から決める。Domain が nil ならすべて業務、Repo が nil なら repo は空。
type Resolver struct {
	Domain func(cwd string) domain.Domain
	Repo   func(cwd string) string
}

func (r Resolver) domain(cwd string) domain.Domain {
	if r.Domain == nil {
		return domain.Work
	}
	return r.Domain(cwd)
}

func (r Resolver) repo(cwd string) string {
	if r.Repo == nil {
		return ""
	}
	return r.Repo(cwd)
}

// hookInput は PostToolUse hook の stdin のうち、記録に使う欄。
// cc-premise: 2.1.302 tasks PostToolUse の tool_input / tool_response の Task 系の形 (testdata の実測)。TaskList の blockedBy は完了していない blocker だけ
type hookInput struct {
	SessionID    string          `json:"session_id"`
	AgentID      string          `json:"agent_id"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// Ingest は PostToolUse hook の stdin を読み、ツールに応じてタスクを書く。Task 系でも TodoWrite でもない
// ツールは何もしない。domain と repo は r が cwd から決める。
func (s Store) Ingest(input []byte, r Resolver, now time.Time) error {
	if s.Dir == "" {
		return errors.New("tasks の置き場所が無い")
	}
	var in hookInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("hook 入力の JSON を読めない: %w", err)
	}
	if !statefile.ValidSessionID(in.SessionID) {
		return fmt.Errorf("session ID %q はファイル名に使えない", in.SessionID)
	}
	switch in.ToolName {
	case "TaskCreate", "TaskUpdate", "TaskList", "TodoWrite":
	default:
		return nil
	}
	dir, err := s.sessionDir(in.SessionID, r.domain(in.CWD))
	if err != nil {
		return err
	}
	w := writer{dir: dir, in: in, repo: r.repo(in.CWD), now: now}
	switch in.ToolName {
	case "TaskCreate":
		return w.create()
	case "TaskUpdate":
		return w.update()
	case "TaskList":
		return w.sync()
	default:
		return w.todos()
	}
}

// sessionDir はセッションのタスクを置くディレクトリを決める。1 つのセッションが私用と業務の両方の
// ディレクトリを触ったら業務に倒す (#121): 業務と判定されたら私用の側にあった記録を業務へ移し、
// 私用と判定されても業務の側に記録があれば業務のまま書く。業務の記録が私用の側へ出ないようにするため。
func (s Store) sessionDir(sessionID string, d domain.Domain) (string, error) {
	home := filepath.Join(s.Dir, string(domain.Home), sessionID)
	work := filepath.Join(s.Dir, string(domain.Work), sessionID)
	if d == domain.Home {
		if _, err := os.Stat(work); err == nil {
			return work, nil
		}
		return home, nil
	}
	if _, err := os.Stat(home); err == nil {
		if err := moveToWork(home, work); err != nil {
			return "", err
		}
	}
	return work, nil
}

// moveToWork は私用の側にあるセッションの記録を業務の側へ移す。業務の側に既にあれば私用の側を捨てる
// (業務の側の記録が新しい)。
func moveToWork(home, work string) error {
	if err := os.MkdirAll(filepath.Dir(work), 0o750); err != nil {
		return fmt.Errorf("tasks の作成に失敗: %w", err)
	}
	if _, err := os.Stat(work); err == nil {
		if err := os.RemoveAll(home); err != nil {
			return fmt.Errorf("私用の側の記録を消せない: %w", err)
		}
		return nil
	}
	if err := os.Rename(home, work); err != nil {
		return fmt.Errorf("私用の側の記録を業務へ移せない: %w", err)
	}
	return nil
}

// List は domain の全セッションのタスクを返す。壊れたファイルは読み飛ばす。
func (s Store) List(d domain.Domain) ([]Task, error) {
	if s.Dir == "" {
		return nil, nil
	}
	root := filepath.Join(s.Dir, string(d))
	sessions, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks の走査に失敗: %w", err)
	}
	var tasks []Task
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		dir := filepath.Join(root, session.Name())
		for _, name := range taskFiles(dir) {
			if t, ok := readTask(filepath.Join(dir, name)); ok {
				tasks = append(tasks, t)
			}
		}
	}
	return tasks, nil
}

// writer は 1 回の hook 入力ぶんの書き込み。
type writer struct {
	dir  string
	in   hookInput
	repo string
	now  time.Time
}

func (w writer) create() error {
	var input struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
		ActiveForm  string `json:"activeForm"`
	}
	var resp struct {
		Task struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"task"`
	}
	if err := unmarshalBoth(w.in, &input, &resp); err != nil {
		return err
	}
	t := w.base(resp.Task.ID)
	t.Subject = firstNonEmpty(resp.Task.Subject, input.Subject)
	t.Description = input.Description
	t.ActiveForm = input.ActiveForm
	t.Status = "pending"
	return w.write(t)
}

func (w writer) update() error {
	var input struct {
		TaskID       string   `json:"taskId"`
		Subject      *string  `json:"subject"`
		Description  *string  `json:"description"`
		ActiveForm   *string  `json:"activeForm"`
		Status       *string  `json:"status"`
		AddBlocks    []string `json:"addBlocks"`
		AddBlockedBy []string `json:"addBlockedBy"`
		Owner        *string  `json:"owner"`
	}
	var resp struct {
		Success bool `json:"success"`
	}
	if err := unmarshalBoth(w.in, &input, &resp); err != nil {
		return err
	}
	// 失敗した更新 (存在しないタスクなど) は記録に反映しない。
	if !resp.Success {
		return nil
	}
	if input.Status != nil && *input.Status == "deleted" {
		return w.remove(input.TaskID)
	}
	t, ok := w.read(input.TaskID)
	if !ok {
		// TaskCreate を記録する前に作られたタスク。分かる欄だけで作り、残りは次の TaskList で埋まる。
		t = w.base(input.TaskID)
		t.Status = "pending"
	}
	setIf(&t.Subject, input.Subject)
	setIf(&t.Description, input.Description)
	setIf(&t.ActiveForm, input.ActiveForm)
	setIf(&t.Status, input.Status)
	setIf(&t.Owner, input.Owner)
	t.Blocks = appendUnique(t.Blocks, input.AddBlocks)
	t.BlockedBy = appendUnique(t.BlockedBy, input.AddBlockedBy)
	w.touch(&t)
	return w.write(t)
}

// sync は TaskList の結果で全件を同期し直す。一覧に無いタスクのファイルは消す。
// TaskList の blockedBy には完了していない blocker しか載らないので、そのまま置き換える。
func (w writer) sync() error {
	var resp struct {
		Tasks []struct {
			ID        string   `json:"id"`
			Subject   string   `json:"subject"`
			Status    string   `json:"status"`
			Owner     string   `json:"owner"`
			BlockedBy []string `json:"blockedBy"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(w.in.ToolResponse, &resp); err != nil {
		return fmt.Errorf("TaskList の結果を読めない: %w", err)
	}
	listed := map[string]bool{}
	for _, item := range resp.Tasks {
		if !statefile.ValidSessionID(item.ID) {
			continue
		}
		listed[item.ID+".json"] = true
		t, ok := w.read(item.ID)
		if !ok {
			t = w.base(item.ID)
		}
		t.Subject = item.Subject
		t.Status = item.Status
		t.Owner = item.Owner
		t.BlockedBy = item.BlockedBy
		w.touch(&t)
		if err := w.write(t); err != nil {
			return err
		}
	}
	var errs []error
	for _, name := range taskFiles(w.dir) {
		if listed[name] {
			continue
		}
		if err := os.Remove(filepath.Join(w.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// todos は TodoWrite のリスト全体で todos.json を置き換える。
func (w writer) todos() error {
	var input struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(w.in.ToolInput, &input); err != nil {
		return fmt.Errorf("TodoWrite の入力を読めない: %w", err)
	}
	list := TodoList{SessionID: w.in.SessionID, CWD: w.in.CWD, Repo: w.repo, Todos: input.Todos, UpdatedAt: w.now}
	if list.Todos == nil {
		list.Todos = []Todo{}
	}
	return statefile.WriteJSONAtomic(filepath.Join(w.dir, todosFile), ".todos.*", list)
}

// base は hook 入力から埋まる欄だけを持つタスク。
func (w writer) base(id string) Task {
	t := Task{ID: id, SessionID: w.in.SessionID, AgentID: w.in.AgentID}
	w.touch(&t)
	return t
}

// touch は書くたびに今の値へ揃える欄 (cwd、repo、更新時刻) を埋める。
func (w writer) touch(t *Task) {
	t.CWD = w.in.CWD
	t.Repo = w.repo
	t.UpdatedAt = w.now
}

func (w writer) path(id string) (string, error) {
	if !statefile.ValidSessionID(id) {
		return "", fmt.Errorf("task ID %q はファイル名に使えない", id)
	}
	return filepath.Join(w.dir, id+".json"), nil
}

func (w writer) read(id string) (Task, bool) {
	path, err := w.path(id)
	if err != nil {
		return Task{}, false
	}
	return readTask(path)
}

func (w writer) write(t Task) error {
	path, err := w.path(t.ID)
	if err != nil {
		return err
	}
	return statefile.WriteJSONAtomic(path, ".task.*", t)
}

func (w writer) remove(id string) error {
	path, err := w.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("タスクの記録を消せない: %w", err)
	}
	return nil
}

// taskFiles は dir のタスクのファイル名 (todos.json と書きかけの一時ファイルを除く *.json)。
func taskFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == todosFile || filepath.Ext(name) != ".json" || name[0] == '.' {
			continue
		}
		names = append(names, name)
	}
	return names
}

func readTask(path string) (Task, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- tasks/ 配下の検証済みのパス
	if err != nil {
		return Task{}, false
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil || t.ID == "" {
		return Task{}, false
	}
	return t, true
}

func unmarshalBoth(in hookInput, input, resp any) error {
	if err := json.Unmarshal(in.ToolInput, input); err != nil {
		return fmt.Errorf("%s の入力を読めない: %w", in.ToolName, err)
	}
	if err := json.Unmarshal(in.ToolResponse, resp); err != nil {
		return fmt.Errorf("%s の結果を読めない: %w", in.ToolName, err)
	}
	return nil
}

func setIf(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

func appendUnique(list, add []string) []string {
	for _, v := range add {
		if !slices.Contains(list, v) {
			list = append(list, v)
		}
	}
	return list
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
