// Package record は tasklog が書くタスクの記録 (tasks/<session_id>/ の JSON) の書式を持つ。
// 読み手 (依存図とパーキングロット) は書式だけを使い、置き場所と読み書きは tasklog が持つ。
package record

import "time"

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
