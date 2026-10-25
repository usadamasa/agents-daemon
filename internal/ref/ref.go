// Package ref は依存図の作業の識別子 (Ref) の文法を持つ。依存図のノードの key と、セッションをまたぐ辺
// (internal/edge) の端点はこの形で書く。mod は Ref を不透明な文字列として扱い、切り出さない。
//
//	task:<session_id>/<task_id>     TaskCreate で作られたタスク
//	todo:<session_id>/<n>           TodoWrite のリストの n 番目 (0 始まり)。並びが変わると別の項目を指すので、辺の端点にしない
//	sid:<session_id>                セッションそのもの
//	session:<セッション名>          `claude --name` で渡したセッション。渡す時点では session ID がまだ無い
//	issue:<owner>/<repo>#<番号>     issue
//	pr:<owner>/<repo>#<番号>        PR
package ref

import (
	"regexp"
	"strconv"
)

// endpointPattern は辺の端点にできる Ref の形。issue / pr の repo は GitHub の owner/name。
var endpointPattern = regexp.MustCompile(`^(?:session:\S+|sid:[^\s/]+|(?:issue|pr):[\w.-]+/[\w.-]+#[0-9]+|task:[^\s/]+/[^\s/]+)$`)

var taskPattern = regexp.MustCompile(`^task:([^\s/]+)/([^\s/]+)$`)

// Task はタスクの Ref。
func Task(sessionID, taskID string) string {
	return "task:" + sessionID + "/" + taskID
}

// Todo は TodoWrite のリストの index 番目の項目の Ref。
func Todo(sessionID string, index int) string {
	return "todo:" + sessionID + "/" + strconv.Itoa(index)
}

// SessionID はセッションそのものの Ref。
func SessionID(sessionID string) string {
	return "sid:" + sessionID
}

// ParseTask はタスクの Ref から session ID と task ID を取り出す。タスクの Ref でなければ ok が false。
func ParseTask(r string) (sessionID, taskID string, ok bool) {
	m := taskPattern.FindStringSubmatch(r)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// Endpoint は r が辺の端点にできる Ref か。
func Endpoint(r string) bool {
	return endpointPattern.MatchString(r)
}
