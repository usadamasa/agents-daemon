package monitor

import (
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// daemon 自身が打ち込む prompt は､cache が失効した後に送れば文脈全体を書き直す｡
// 利用上限からの再開は 5 時間待った後なので必ず失効している｡このコストは避けられない
// (再開しなければ意味が無い) が､送る側がそれを知らないと､ログにも残らず､TTL guard hook が
// daemon の prompt を止めて再開が「pane が固まった」ように見える｡
//
// だで送る前に cache の状態を読み､非スラッシュの prompt を失効後に送るときは
// ack マーカー cache-ack/<session_id> に直近の応答の開始時刻の文字列をそのまま写す
// (「書き直しのコストを承知で送る」の意思表示｡guard はこれを見て通す)｡状態が無ければ
// sentinel を新しい mtime で書く｡書かないと guard が再開を止め､daemon は pane の停止と見て
// nudge を打ち､それも止められて堂々巡りになる｡
//
// 送る・送らないの判断は変えない｡足すのは ack と､daemon がログに残すための報告だけ｡

// SendReport は daemon が pane へ prompt を送ったときの cache の状態｡監視の判断には
// 使わず､daemon がログへ書くためだけに PaneState に残す｡
type SendReport struct {
	// At は送った時刻｡ゼロ値は「まだ何も送っていない」｡
	At time.Time
	// Slash は prompt がスラッシュコマンドだったか｡guard が素通しするので ack は書かない｡
	Slash bool
	// Cache は送信時点の cache の状態｡nil は状態無し (または session が無い)｡
	Cache *sessionstate.Cache
	// Ack は書いた ack の値｡空は書いていない (warm / スラッシュ / session 無し / 配線無し)｡
	Ack string
	// AckErr は ack の書き込みの失敗｡送信自体は行っている｡
	AckErr error
}

// State は cache の状態を識別子で返す (warm / expired / unknown)｡
func (r SendReport) State() string {
	switch {
	case r.Cache == nil:
		return "unknown"
	case r.Cache.Expired(r.At):
		return "expired"
	default:
		return "warm"
	}
}

// ackBeforeSend は text を pane へ送る直前に呼び､cache の状態を ps.LastSend に残し､
// 必要なら ack を書く｡失敗しても送信は止めない (報告に残すだけ)｡
//
// スラッシュの判定は config のキーではなく送る文面で行う｡どの文面も設定で書き換えられる｡
func ackBeforeSend(deps Deps, pane herdrcli.Pane, ps *PaneState, text string, now time.Time) {
	report := SendReport{At: now, Slash: strings.HasPrefix(strings.TrimSpace(text), "/")}
	defer func() { ps.LastSend = report }()

	sessionID := pane.SessionID()
	if sessionID == "" || deps.CacheState == nil {
		return
	}
	report.Cache = deps.CacheState(sessionID)
	if report.Slash || deps.CacheAck == nil {
		return
	}

	// 値は transcript の文字列をそのまま写す｡time.Parse / Format を通すとミリ秒が落ちたり
	// Z が +00:00 になったりして､guard の文字列比較と合わなくなる｡
	value := sessionstate.CacheAckUnknown
	if c := report.Cache; c != nil {
		if !c.Expired(now) {
			return
		}
		value = c.LastRequestRaw
	}
	report.Ack = value
	report.AckErr = deps.CacheAck(sessionID, value)
}
