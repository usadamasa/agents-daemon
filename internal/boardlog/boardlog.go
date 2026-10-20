// Package boardlog はタスクのボードの経路 (タスクの記録、パーキング、mod) で起きたことを、後から読めるログに
// 1 行 1 件で書く (#146)。daemon のプロセスを通らない CLI と mod (`board-log` 経由) が書き、
// `agents-daemon logs --board` が読む。
//
//	logs/board-<domain>.log              現行のログ
//	logs/board-<domain>.log.YYYY-MM-DD   日付が変わって退避したもの (daemon の housekeeping が消す)
//
// daemon.log に混ぜず domain ごとに分けるのは、業務の domain の出来事を私用の側から読める場所に
// 出さないため (#121)。書くのは session ID、task ID、メモの id、件数、失敗の理由だけにし、タスクの subject、
// メモの title と detail、送った文面は書かない。
//
// 書式は `<RFC 3339 の時刻> <出来事> key=value …`。出来事:
//
//	ingest-task               Task 系ツールと TodoWrite を記録した (tool、written、removed)
//	ingest-task.failed        記録できなかった (tool、reason)
//	park.add                  メモを置いた (memo、kind、created、task)
//	park.set                  メモの status を書き換えた (memo、from、to)
//	park.discard              古い open のメモをまとめて捨てた (days、memos、ids)
//	park.adopt                終了したセッションのタスクをメモにした (from_session、task、memo、created)
//	park.failed               park の操作が失敗した (op、reason)
//	mod.tool-register.failed  session.start で park の tool を登録できなかった (reason)
//	mod.board.open            ボードを開いた (rows、cards、memos、daemon_running)
//	mod.board.changed         ボードの行・カード・メモの数か daemon の稼働が変わった (同上)
//	mod.board.failed          status --board が失敗した (reason)
//	mod.send                  ボードから指示やメモを送った (via、to、delivered、reason)
//	mod.hint.failed           prompt の hint 行のメモの数を読めなかった (reason)
//	mod.band.failed           band のメモを読めなかった (reason)
//
// mod の出来事は同じ理由が続く間は書き直さない (hooks/log.ts)。
package boardlog

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/domain"
)

// field は 1 行に添える key=value。
type field struct {
	key   string
	value string
}

// f は value を文字列にした field を作る。
func f(key string, value any) field {
	return field{key: key, value: fmt.Sprint(value)}
}

// Path は domain の現行のログのパス。
func Path(stateDir string, d domain.Domain) string {
	return filepath.Join(stateDir, "logs", "board-"+string(d)+".log")
}

// Paths は全 domain の現行のログのパス。housekeeping が rotate 済みのものを消すのに使う。
func Paths(stateDir string) []string {
	return []string{Path(stateDir, domain.Home), Path(stateDir, domain.Work)}
}

// write は domain のログへ 1 行書く。CLI は短命なので、1 回開いて書いて閉じる。書けなくても本処理を
// 止めない (applog.Logger.Logf と同じ)。
//
// 並行する CLI の行は、O_APPEND と 1 行を 1 回の write で書くことで混ざらない。日付の境目で別の
// プロセスが先に退避した後でも、退避前に開いていたプロセスの行は退避したファイルへ入る。それは許す。
func write(stateDir string, d domain.Domain, now func() time.Time, event string, fields ...field) {
	log := applog.NewLogger(Path(stateDir, d), now)
	defer func() { _ = log.Close() }()
	log.Logf("%s", format(event, fields))
}

func format(event string, fields []field) string {
	var b strings.Builder
	b.WriteString(event)
	for _, fd := range fields {
		b.WriteString(" ")
		b.WriteString(fd.key)
		b.WriteString("=")
		b.WriteString(quote(fd.value))
	}
	return b.String()
}

// quote は空白、制御文字、=、" を含む値と空の値を引用する。1 件が 1 行に収まり、key=value の区切りが崩れない。
func quote(v string) string {
	if v == "" || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == '=' || r == '"' || r == 0x7f }) {
		return strconv.Quote(v)
	}
	return v
}

var (
	eventPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)
	keyPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// validEvent は event が出来事の名前の形 (小文字、数字、`.`、`-`) かを返す。
func validEvent(event string) bool {
	return eventPattern.MatchString(event)
}

// parseFields は `key=value` の引数を読む。value は最初の = より後のすべて。
func parseFields(args []string) ([]field, error) {
	fields := make([]field, 0, len(args))
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok || !keyPattern.MatchString(key) {
			return nil, errors.New("key=value の形でない引数: " + strconv.Quote(arg))
		}
		fields = append(fields, field{key: key, value: value})
	}
	return fields, nil
}
