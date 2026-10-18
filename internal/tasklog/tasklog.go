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

// hookInput は PostToolUse hook の stdin のうち、記録に使う欄。
// cc-premise: 2.1.302 hooks PostToolUse の tool_input / tool_response (Task 系の形は testdata の実測)
type hookInput struct {
	SessionID    string          `json:"session_id"`
	AgentID      string          `json:"agent_id"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// Result は 1 回の Ingest で何をしたか。ボードの経路のログ (internal/boardlog) に出す。Tool が空なら記録しない
// ツールで、Domain が空なら置き場を決める前に失敗した。
type Result struct {
	Tool      string
	SessionID string
	AgentID   string
	Domain    domain.Domain
	// Written は書いたタスクの数 (TodoWrite はリストの項目の数)、Removed は消したタスクの数。
	Written, Removed int
}

// Ingest は PostToolUse hook の stdin を読み、ツールに応じてタスクを書く。Task 系でも TodoWrite でもない
// ツールは何もしない。domain は classify が cwd から決める。失敗しても、分かったところまでの Result を返す。
func (s Store) Ingest(input []byte, classify func(cwd string) domain.Domain, now time.Time) (Result, error) {
	if s.Dir == "" {
		return Result{}, errors.New("tasks の置き場所が無い")
	}
	var in hookInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{}, fmt.Errorf("hook 入力の JSON を読めない: %w", err)
	}
	if !statefile.ValidSessionID(in.SessionID) {
		return Result{Tool: in.ToolName}, fmt.Errorf("session ID %q はファイル名に使えない", in.SessionID)
	}
	switch in.ToolName {
	case "TaskCreate", "TaskUpdate", "TaskList", "TodoWrite":
	default:
		return Result{}, nil
	}
	res := Result{Tool: in.ToolName, SessionID: in.SessionID, AgentID: in.AgentID}
	dir, d, err := s.sessionDir(in.SessionID, classify(in.CWD))
	if err != nil {
		return res, err
	}
	res.Domain = d
	w := &writer{dir: dir, in: in, now: now}
	switch in.ToolName {
	case "TaskCreate":
		err = w.create()
	case "TaskUpdate":
		err = w.update()
	case "TaskList":
		err = w.sync()
	default:
		err = w.todos()
	}
	res.Written, res.Removed = w.written, w.removed
	return res, err
}

// sessionDir はセッションのタスクを置くディレクトリとその domain を決める。1 つのセッションが私用と業務の両方の
// ディレクトリを触ったら業務に倒す (#121): 業務と判定されたら私用の側にあった記録を業務へ移し、
// 私用と判定されても業務の側に記録があれば業務のまま書く。業務の記録が私用の側へ出ないようにするため。
func (s Store) sessionDir(sessionID string, d domain.Domain) (string, domain.Domain, error) {
	home := filepath.Join(s.Dir, string(domain.Home), sessionID)
	work := filepath.Join(s.Dir, string(domain.Work), sessionID)
	if d == domain.Home {
		if _, err := os.Stat(work); err == nil {
			return work, domain.Work, nil
		}
		return home, domain.Home, nil
	}
	if _, err := os.Stat(home); err == nil {
		if err := moveToWork(home, work); err != nil {
			return "", domain.Work, err
		}
	}
	return work, domain.Work, nil
}

// moveToWork は私用の側にあったセッションの記録を業務の側へ移す。業務の側にも既にあれば
// (並行した hook が先に作った)、業務の側を残して私用の側を消す。
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

// writer は 1 回の hook 入力ぶんの書き込み。書いた数と消した数を数える。
type writer struct {
	dir              string
	in               hookInput
	now              time.Time
	written, removed int
}

func (w *writer) create() error {
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

func (w *writer) update() error {
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
	t.CWD = w.in.CWD
	t.UpdatedAt = w.now
	return w.write(t)
}

// sync は TaskList の結果で全件を同期し直す。一覧に無いタスクのファイルは消す。
// TaskList の blockedBy には完了していない blocker しか載らないので、そのまま置き換える。
func (w *writer) sync() error {
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
		t.CWD = w.in.CWD
		t.UpdatedAt = w.now
		if err := w.write(t); err != nil {
			return err
		}
	}
	var errs []error
	for _, name := range taskFiles(w.dir) {
		if listed[name] {
			continue
		}
		if err := os.Remove(filepath.Join(w.dir, name)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		w.removed++
	}
	return errors.Join(errs...)
}

// todos は TodoWrite のリスト全体で todos.json を置き換える。
func (w *writer) todos() error {
	var input struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(w.in.ToolInput, &input); err != nil {
		return fmt.Errorf("TodoWrite の入力を読めない: %w", err)
	}
	list := TodoList{SessionID: w.in.SessionID, CWD: w.in.CWD, Todos: input.Todos, UpdatedAt: w.now}
	if list.Todos == nil {
		list.Todos = []Todo{}
	}
	if err := statefile.WriteJSONAtomic(filepath.Join(w.dir, todosFile), ".todos.*", list); err != nil {
		return err
	}
	w.written = len(list.Todos)
	return nil
}

// base は hook 入力から埋まる欄だけを持つタスク。
func (w *writer) base(id string) Task {
	return Task{ID: id, SessionID: w.in.SessionID, AgentID: w.in.AgentID, CWD: w.in.CWD, UpdatedAt: w.now}
}

func (w *writer) path(id string) (string, error) {
	if !statefile.ValidSessionID(id) {
		return "", fmt.Errorf("task ID %q はファイル名に使えない", id)
	}
	return filepath.Join(w.dir, id+".json"), nil
}

func (w *writer) read(id string) (Task, bool) {
	path, err := w.path(id)
	if err != nil {
		return Task{}, false
	}
	return readTask(path)
}

func (w *writer) write(t Task) error {
	path, err := w.path(t.ID)
	if err != nil {
		return err
	}
	if err := statefile.WriteJSONAtomic(path, ".task.*", t); err != nil {
		return err
	}
	w.written++
	return nil
}

func (w *writer) remove(id string) error {
	path, err := w.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("タスクの記録を消せない: %w", err)
	}
	w.removed++
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
