// Package daemon は agents-daemon の監視デーモンの実体 (ポーリングループ、
// 起動の排他 (lock) と PID ファイル、status snapshot、dry-run 経路) を提供する。daemoncli の
// daemon / status / stop はこのパッケージを薄く呼び出すだけの cobra 配線に徹する。
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/refstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/runtime/claudesessions"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/dryrun"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/reaper"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/refpoll"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/transcript"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// maxConsecutiveTickFailures は PaneList の取得にこの回数だけ連続で
// 失敗したら daemon が自己終了する閾値。多重化ツールの server が落ちたまま無限に回り続けて
// ログを埋め尽くすのを防ぐ。
const maxConsecutiveTickFailures = 10

// paneEntry は daemon が pane 1 枚ぶんに保持する監視状態。
type paneEntry struct {
	state      *monitor.PaneState
	lastSeenAt time.Time
	lastLogKey string // 直前にログへ書いた内容の識別子 (同じ状態が続く間の重複を抑える)
	// loggedUnknownReason は最後にログへ書いた Pane.UnknownReason。同じ理由が続く間は書かない。
	loggedUnknownReason string
	// loggedMismatch は最後にログへ書いた「StopFailure の記録と transcript の判定の食い違い」の鍵。同じ間は書かない。
	loggedMismatch string
}

// multiplexerClient は daemon が多重化ツールに対して使う操作。monitor へ渡す 3 つ (読む・送る) に、tick の pane の列挙、
// 上限の出来事の記録に添える端末の大きさ、起動時の到達性を足したもの (#301)。PaneGet は daemon の process からは
// 呼ばないので持たない。backend.Connect が返す multiplexer.Client (全部入り) をそのまま渡し、dry-run の包み
// (dryrun.Client) はこの 6 つだけを実装する。購読 (multiplexer.Watcher) は持つ実装だけが満たす別の口で、startWatch が型で確かめる。
type multiplexerClient interface {
	monitor.Client
	// PaneList は現在の全 pane を返す。
	PaneList(ctx context.Context) ([]multiplexer.Pane, error)
	// PaneGeometry は pane の端末の大きさと画面を返す。上限の出来事の記録だけが呼ぶ。
	PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error)
	// Reachable は多重化ツールの server に到達できるかを確認する。起動時に 1 回だけ呼ぶ。
	Reachable(ctx context.Context) error
}

// daemonRuntime は 1 回の --foreground 実行に閉じたループの可変状態をまとめる。
// pane ごとの状態機械は PaneID (rex の block ID) をキーにする。
type daemonRuntime struct {
	client multiplexerClient
	// backend は client の多重化ツールの種類。status.json に書くだけで判定には使わない。
	backend string
	log     *applog.Logger
	// statusPath と store はプロセスの生存期間で固定なのでここに置く。
	statusPath string
	store      sessionstate.Store
	// tasks は hook が書くタスクの記録 (tasklog)。daemon は housekeeping で消すだけ。
	// ゼロ値は何も消さない。
	tasks tasklog.Store
	// works は hook が書くセッションの作業の記録 (worklog)。daemon は tick で生きているセッションの mtime を進め、
	// housekeeping で消す。ゼロ値は何もしない。
	works worklog.Store
	// edges は `edge add` が書くセッションをまたぐ辺 (edge)。daemon は tick ごとに session:<名前> の
	// 端点を台帳のセッションに結びつけ、housekeeping で消す。ゼロ値は何もしない。
	edges edge.Store
	// sessions はセッション台帳 (ledger)。daemon だけが tick ごとに書き、housekeeping で消す。
	// ゼロ値は書かない。
	sessions ledger.Store
	// claudeSessions は Claude Code のセッションの登録簿 (claudesessions)。台帳に書く名前 (ListAgents の名前) と、
	// statusline の記録から session の引けない pane の session_id (前景の pid から) をここから引く。ゼロ値はどちらも引かない。
	claudeSessions claudesessions.Store
	// transcripts は Claude Code の transcript の置き場 (transcript)。上限の判定はこの末尾の entry で行う。
	// ゼロ値は全 pane が「transcript が読めない」になる。
	transcripts transcript.Store
	// parking は `park add` が書くパーキングのメモ (parking)。daemon は housekeeping で消すだけ。
	// ゼロ値は何も消さない。
	parking parking.Store
	// eventLog は CLI と mod が書く出来事のログ (eventlog)。daemon は housekeeping で rotate 済みの
	// ものを消すだけ。ゼロ値は何も消さない。
	eventLog string
	// limits は上限の出来事の記録 (daemon/limitlog)。daemon が書き、housekeeping で消す。ゼロ値は書かない。
	limits limitlog.Store
	// turnFailures は hook が書く turn の失敗の記録 (turnfailure)。daemon は上限の出来事に添えて読み、
	// housekeeping で消す。ゼロ値は「無い」を返し、何も消さない。
	turnFailures turnfailure.Store
	// refs は辺の端点の issue / PR の状態の写し (refstate)。refPoll が tick ごとに引いて書き、housekeeping で消す。
	// ゼロ値 (refPoll が nil) は引かない。
	refs    refstate.Store
	refPoll *refpoll.Poller
	// spawned は spawn と peer-sessions skill が起こした session の印 (spawnedsession)。reaper が tick ごとに回り、
	// housekeeping で消す。reaper が nil (多重化ツールが後片付けの操作を持たない) なら回らない。
	spawned             spawnedsession.Store
	reaper              *reaper.Reaper
	sleep               func(time.Duration)
	startedAt           time.Time
	panes               map[string]*paneEntry
	consecutiveFailures int
	idleSince           time.Time // ゼロ値は「非アイドル (Claude pane が存在する)」
	lastHousekeepAt     time.Time // ゼロ値は「まだ 1 度も掃除していない」
	// liveSessions は最後の tick で pane が見えたセッション。housekeeping が終了したセッションを見分けるのに使う。
	liveSessions []string
	// livePlaces は最後の tick で pane が見えたセッションとその cwd。refPoll が PR を出したセッションを引くのに使う。
	livePlaces []refpoll.Session
}

// liveRefSessions は最後の tick で pane が見えたセッションとその今の cwd。pane の cwd は起動したディレクトリのままで、
// セッションの途中で入った worktree を映さないので、作業の記録の cwd (hook が渡す今の cwd) があればそちらを使う。
// cc-premise: 2.1.323 hooks EnterWorktree の後の hook の入力の cwd は worktree (rex の pane の cwd は起動したディレクトリのまま)
func (r *daemonRuntime) liveRefSessions() []refpoll.Session {
	sessions := make([]refpoll.Session, 0, len(r.livePlaces))
	for _, s := range r.livePlaces {
		if rec, ok := r.works.Get(s.ID); ok && rec.CWD != "" {
			s.CWD = rec.CWD
		}
		sessions = append(sessions, s)
	}
	return sessions
}

func (r *daemonRuntime) prIssuers() map[string][]refpoll.Session {
	return nil
}

func newDaemonRuntime(client multiplexerClient, log *applog.Logger, statusPath string, store sessionstate.Store, sleep func(time.Duration), startedAt time.Time) *daemonRuntime {
	return &daemonRuntime{
		client:     client,
		store:      store,
		log:        log,
		statusPath: statusPath,
		sleep:      sleep,
		startedAt:  startedAt,
		panes:      map[string]*paneEntry{},
	}
}

// tick は 1 回分のポーリングを処理する。設定を毎回読み直すことで、daemon を
// 再起動せずに設定変更 (閾値・パターン等) を反映できる (参考実装からの踏襲)。
// 返り値の cfg は呼び出し元がポーリング間隔の更新に使う。shutdown=true は
// 呼び出し元がループを終了すべきことを表す。
func (r *daemonRuntime) tick(ctx context.Context, cfgPath string, now time.Time) (shutdown bool, cfg config.Config, err error) {
	cfg, cfgErr := config.Load(cfgPath)
	if cfgErr != nil {
		r.log.Logf("設定の読み込みでエラー、デフォルト値で継続します: %v", cfgErr)
	}

	panes, err := r.client.PaneList(ctx)
	if err != nil {
		r.consecutiveFailures++
		if r.consecutiveFailures >= maxConsecutiveTickFailures {
			r.log.Logf("pane list の取得に %d 回連続で失敗したため daemon を終了します: %v", r.consecutiveFailures, err)
			return true, cfg, err
		}
		return false, cfg, fmt.Errorf("pane list の取得に失敗 (%d 回連続): %w", r.consecutiveFailures, err)
	}
	r.consecutiveFailures = 0
	r.resolveSessionIDs(panes)

	deps := monitor.Deps{Client: r.client, Config: cfg, Sleep: r.sleep, Turns: r.transcripts.Tail}

	seen := make(map[string]bool, len(panes))
	claudeCount := 0
	statusPanes := make([]daemonstate.PaneStatus, 0, len(panes))

	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		claudeCount++
		seen[pane.PaneID] = true
		statusPanes = append(statusPanes, r.tickPane(ctx, deps, pane, cfg, now))
	}

	r.forgetUnseen(seen)

	if err := daemonstate.WriteStatusSnapshot(r.statusPath, os.Getpid(), r.backend, r.startedAt, now, statusPanes); err != nil {
		r.log.Logf("status snapshot の書き込みに失敗: %v", err)
	}
	r.observeSessions(statusPanes, now)
	r.refPoll.Refresh(ctx, now)
	r.reaper.Reap(ctx, now)

	if claudeCount == 0 {
		if r.idleSince.IsZero() {
			r.idleSince = now
		} else if now.Sub(r.idleSince) >= time.Duration(cfg.IdleShutdownMinutes)*time.Minute {
			r.log.Logf("Claude pane が %d 分間 0 件のため daemon を終了します", cfg.IdleShutdownMinutes)
			return true, cfg, nil
		}
	} else {
		r.idleSince = time.Time{}
	}

	return false, cfg, nil
}

// tickPane は Claude の pane 1 枚の tick。状態機械を回し、出来事をログと limit-events に残し、status.json の 1 行を返す。
// Tick が判定に使った根拠 (Observe) は pane ごとに受け、読まなかった tick では nil のまま。
func (r *daemonRuntime) tickPane(ctx context.Context, deps monitor.Deps, pane multiplexer.Pane, cfg config.Config, now time.Time) daemonstate.PaneStatus {
	var observed *monitor.Evidence
	deps.Observe = func(_ multiplexer.Pane, ev monitor.Evidence) { observed = &ev }
	entry, ok := r.panes[pane.PaneID]
	if !ok {
		entry = &paneEntry{state: &monitor.PaneState{}}
		r.panes[pane.PaneID] = entry
	}
	entry.lastSeenAt = now
	r.logUnknownReason(pane, entry)

	outcome, tickErr := monitor.Tick(ctx, deps, pane, entry.state, now)
	failure, failureErr := r.turnFailures.Load(pane.SessionID)
	if failureErr != nil {
		r.log.Logf("pane %s: turn の失敗の記録の読み込みに失敗、無しとして扱います: %v", pane.PaneID, failureErr)
	}
	if observed != nil {
		r.logMismatch(pane, entry, *observed, failure)
	}
	if r.logOutcome(pane, outcome, tickErr) && observed != nil && isLimitEvent(outcome) {
		r.recordLimitEvent(ctx, pane, entry, outcome, *observed, failure, cfg, now)
	}
	return paneStatus(pane, entry, outcome, observed, now)
}

// resolveSessionIDs は statusline の記録 (context/) から session の引けなかった Claude の pane の SessionID を、
// 前景の pid で Claude Code の登録簿から補う。statusline を配線していないセッションでも transcript を引けるようにする。
func (r *daemonRuntime) resolveSessionIDs(panes []multiplexer.Pane) {
	if err := r.claudeSessions.Fill(panes); err != nil {
		r.log.Logf("Claude Code の登録簿の読み込みに失敗、pid から session_id を補いません: %v", err)
	}
}

// logMismatch は StopFailure hook の記録 (最後の turn が rate_limit で終わった) があるのに transcript の末尾が
// 上限でないとき、理由が変わったときだけログに残す。transcript の形が変わって読めなくなった疑いを、daemon が
// 黙って何もしない形で終わらせないため。hook が走らないセッションでは記録が無く、ここには来ない。
func (r *daemonRuntime) logMismatch(pane multiplexer.Pane, entry *paneEntry, ev monitor.Evidence, failure *turnfailure.Record) {
	key := ""
	if failure != nil && failure.Error == "rate_limit" && !ev.Kind.Limited() && ev.Kind != detect.KindSpendLimit {
		key = failure.ObservedAt.Format(time.RFC3339) + "|" + ev.Turn.Reason
	}
	if key == entry.loggedMismatch {
		return
	}
	entry.loggedMismatch = key
	if key != "" {
		r.log.Logf("pane %s: StopFailure の記録は rate_limit (%s) なのに transcript では上限と読めません。transcript の形が変わった疑い: %s",
			pane.PaneID, failure.ObservedAt.Format(time.RFC3339), transcriptDetail(ev.Turn))
	}
}

// transcriptDetail は食い違いのログに添える、読んだ transcript の末尾の要約。
func transcriptDetail(turn monitor.Turn) string {
	if turn.Entry == nil {
		return turn.Reason
	}
	return fmt.Sprintf("末尾の entry は type=%s error=%q (%s)", turn.Entry.Type, turn.Entry.Error, turn.Path)
}

// logUnknownReason は record があるのに使えず unknown になった理由を、理由が変わったときだけログに残す。
// 理由が無いと「静かに止まった」ように見える。
func (r *daemonRuntime) logUnknownReason(pane multiplexer.Pane, entry *paneEntry) {
	if pane.UnknownReason == entry.loggedUnknownReason {
		return
	}
	entry.loggedUnknownReason = pane.UnknownReason
	if pane.UnknownReason != "" {
		r.log.Logf("pane %s: agent_status を判定できません: %s", pane.PaneID, pane.UnknownReason)
	}
}

// forgetUnseen は消えた pane のエントリを捨てる。
// 捨てずに放置すると pane の入れ替わりのたびに無限に育ってしまう。
func (r *daemonRuntime) forgetUnseen(seen map[string]bool) {
	for paneID := range r.panes {
		if !seen[paneID] {
			delete(r.panes, paneID)
		}
	}
}

// paneStatus は status.json に書く pane 1 枚ぶんのスナップショットを組む。observed は Tick が判定に使った根拠で、
// 判定できなかった理由 (session_id が引けない、transcript が読めない) をそこから写す。読まなかった tick では nil。
func paneStatus(pane multiplexer.Pane, entry *paneEntry, outcome monitor.Outcome, observed *monitor.Evidence, now time.Time) daemonstate.PaneStatus {
	st := daemonstate.PaneStatus{
		PaneID:        pane.PaneID,
		SessionID:     pane.SessionID,
		CWD:           pane.CWD,
		AgentStatus:   string(pane.AgentStatus),
		AgentKind:     pane.AgentKind,
		AgentMessage:  pane.AgentMessage,
		MonitorStatus: monitorStatusString(entry.state.Status),
		WaitUntil:     zeroableTime(entry.state.WaitUntil),
		Attempts:      entry.state.Attempts,
		LastOutcome:   outcome.String(),
		LastTickAt:    now,
	}
	if observed != nil && observed.Turn.Entry == nil {
		st.DetectReason = observed.Turn.Reason
	}
	return st
}

// monitorStatusString は monitor.Status を status.json 向けの識別子文字列にする。
func monitorStatusString(s monitor.Status) string {
	if s == monitor.StatusWaiting {
		return "waiting"
	}
	return "monitoring"
}

// zeroableTime はゼロ値の time.Time を nil に変換する。PaneState.WaitUntil は
// 監視中 (未待機) の間ゼロ値のままなので、JSON へは省略して出す。
func zeroableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// logOutcome は tick 1 回の結果をログへ書く。「静かな tick」(監視中で異常無し、
// 対象外、待機継続中) はログしない。次の朝に読んで意味が分かる粒度に絞る:
// 上限検知と待ち時間、送信の実施、ユーザーの自己復帰、ゲート抑制、エラー。
// 返り値は「状態が切り替わった (同じ出来事の繰り返しではない)」で、上限の出来事の記録も同じ遷移で書く。
func (r *daemonRuntime) logOutcome(pane multiplexer.Pane, outcome monitor.Outcome, err error) bool {
	// 同じ状態が続く間は書かない。tick は既定 5 秒ごとなので、待機や抑制が
	// 続くだけで日に数千行に膨れ、後から読めるログでなくなる。状態が切り替わった
	// ときだけ書き、静かな結果も key は更新して「戻ってきた」を検出できるようにする。
	entry := r.panes[pane.PaneID]
	key := logKey(outcome, err, entry)
	if entry != nil {
		repeated := entry.lastLogKey == key
		entry.lastLogKey = key
		if repeated {
			return false
		}
	}

	if err != nil {
		r.log.Logf("pane %s (%s): %s でエラー: %v", pane.PaneID, pane.CWD, outcome, err)
		return true
	}
	if outcome == monitor.OutcomeMonitoring || outcome == monitor.OutcomeStillWaiting {
		return false
	}
	msg, ok := outcomeMessages[outcome]
	switch {
	case !ok:
		msg = outcome.String()
	case outcome == monitor.OutcomeRetried:
		msg = fmt.Sprintf(msg, entry.state.Attempts)
	case outcome == monitor.OutcomeTranscriptUnavailable:
		msg = fmt.Sprintf(msg, pane.SessionID)
	}
	// 行には Claude Code の自己申告 (record) を添える。logKey には混ぜない (msg が変わるだけで同じ出来事を書き直さない)。
	// 判定の根拠 (transcript の entry、画面) は limit-events/ にある。
	r.log.Logf("pane %s (%s): %s (%s)", pane.PaneID, pane.CWD, msg, multiplexer.RecordLabel(pane))
	return true
}

// outcomeMessages は Outcome ごとのログの文。retried は試行回数、transcript-unavailable は session_id を %s / %d で受ける。
var outcomeMessages = map[monitor.Outcome]string{
	monitor.OutcomeNoSession:             "session_id を引けないため上限を判定しません (statusline の記録も登録簿の pid も無い)",
	monitor.OutcomeTranscriptUnavailable: "transcript を読めないため上限を判定しません (session %s)",
	monitor.OutcomeWorkingSuppressed:     "transcript の末尾は上限ですが record が working のため手を出しません",
	monitor.OutcomeStaleTurn:             "transcript の末尾は上限ですが解除 (か entry) から 24 時間より経った (か時刻が無い) ため待機に入りません",
	monitor.OutcomeWaiting:               "利用上限/一時エラーを検知し待機に入りました",
	monitor.OutcomeRetried:               "再開メッセージを送信しました (%d 回目)",
	monitor.OutcomeRecoveredByUser:       "セッションが上限から戻ったため監視状態へ戻します",
	monitor.OutcomeMaxRetries:            "再試行上限に達したためクールダウンに入ります",
	monitor.OutcomeNotClaudeAgent:        "Claude agent でなくなったため待機を延長します",
	monitor.OutcomeReadError:             "待機明けに画面を読めないため送らず待機を続けます",
	monitor.OutcomeNativeAutoContinue:    "Claude Code 自身が解除を待っているため手を引きます",
	monitor.OutcomeLowerPriority:         "lower-priority mode で作業中のため手を引きます",
	monitor.OutcomeResumeEnter:           "上限が解除済みで Enter 待ちだったため Enter を送りました",
	monitor.OutcomeHardCap:               "待っても解除されない上限 (spend limit / usage credit / Fable) のため待機に入りません",
	monitor.OutcomeWeeklyCap:             "週の上限で解除が 24 時間より先のため待機に入りません (近づいたら待機に入ります)",
	monitor.OutcomeBlockedByPrompt:       "Claude Code が利用者の答えを待つ prompt を開いているため送信を見送り待機を延長します",
}

// isLimitEvent は outcome が上限の出来事 (後から検証する材料を残す価値があるもの) かを返す。
// 静かな結果、Claude でなくなった pane は対象外。判定できなかった理由と、上限なのに working の食い違いは残す。
func isLimitEvent(outcome monitor.Outcome) bool {
	switch outcome {
	case monitor.OutcomeMonitoring, monitor.OutcomeStillWaiting, monitor.OutcomeNotClaudeAgent:
		return false
	default:
		return true
	}
}

// recordLimitEvent は上限の出来事 1 件を limit-events/ へ残す。block の高さと screen はこのときだけ打つ
// (毎 tick に 2 fork 足さない)。取れなくても出来事は残す。書けなくても tick は止めない。
func (r *daemonRuntime) recordLimitEvent(ctx context.Context, pane multiplexer.Pane, entry *paneEntry, outcome monitor.Outcome,
	observed monitor.Evidence, failure *turnfailure.Record, cfg config.Config, now time.Time) {
	ev := limitlog.Build(limitlog.Input{
		At: now, Pane: pane, Outcome: outcome, State: *entry.state, Evidence: observed, TurnFailure: failure, Config: cfg,
	})
	if geom, err := r.client.PaneGeometry(ctx, pane.PaneID); err != nil {
		ev.GeometryError = err.Error()
	} else {
		ev.Geometry = &geom
	}
	if err := r.limits.Append(ev); err != nil {
		r.log.Logf("pane %s: 上限の出来事の記録に失敗: %v", pane.PaneID, err)
	}
}

// logKey は logOutcome の重複除去のキー。同じ Outcome でも別の出来事として残すものは値を混ぜる。
func logKey(outcome monitor.Outcome, err error, entry *paneEntry) string {
	key := outcome.String()
	if err != nil {
		key += "|" + err.Error()
	}
	if entry != nil && outcome == monitor.OutcomeRetried {
		// 送信は 1 回ずつが独立した出来事なので、試行回数を混ぜて毎回残す。
		return fmt.Sprintf("%s|%d", key, entry.state.Attempts)
	}
	return key
}

// EnsureAction は `daemon --ensure` が何をしたか。
type EnsureAction string

const (
	// EnsureStarted は daemon を detach 起動したことを表す。
	EnsureStarted EnsureAction = "started"
	// EnsureAlreadyRunning は生きている daemon が既に居たので何もしなかったことを表す。
	EnsureAlreadyRunning EnsureAction = "already_running"
	// EnsureOutsideRex は rex の外 (監視する block が無い) なので何もしなかったことを表す。
	EnsureOutsideRex EnsureAction = "outside_rex"
)

// EnsureReport は `daemon --ensure --json` が出す形。
type EnsureReport struct {
	Action EnsureAction `json:"action"`
}

// EnsureDaemon は生存している daemon が無ければこのバイナリを detach 起動する。
// SessionStart hook から呼ばれる想定なので、常に高速に戻り、hook を絶対に
// ブロック/失敗させない設計にする。
func EnsureDaemon(p apppath.Paths, dryRun bool) (EnsureAction, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("実行ファイルパスの取得に失敗: %w", err)
	}
	return ensureDaemon(p, func() error { return spawnDetached(exe, dryRun, p) })
}

// ensureDaemon は lock を試し、取れなければ既に動いているとして何もしない。取れたら離してから spawn を呼ぶ。
// 子は RunForeground で lock を取り直す。親が握ったまま起こすと子は ErrAlreadyRunning で終わり、daemon が 0 になる
// (os.File の fd は CLOEXEC なので子へは渡らない)。離してから子が取るまでの間に別の --ensure が入っても、
// lock を取れた 1 つだけが daemon になる。
func ensureDaemon(p apppath.Paths, spawn func() error) (EnsureAction, error) {
	lock, err := daemonstate.AcquireLock(daemonstate.LockFile(p))
	if err != nil {
		if errors.Is(err, daemonstate.ErrAlreadyRunning) {
			return EnsureAlreadyRunning, nil
		}
		return "", err
	}
	if err := lock.Close(); err != nil {
		return "", fmt.Errorf("lock を離せません: %w", err)
	}
	if err := spawn(); err != nil {
		return "", err
	}
	return EnsureStarted, nil
}

// spawnDetached は exe を `daemon --foreground` として detach 起動する。
// 呼び出し元 (hook 経由の EnsureDaemon、実行ファイル更新時の入れ替え) を
// ブロックしないよう、Wait せずに切り離す。
func spawnDetached(exe string, dryRun bool, p apppath.Paths) error {
	if err := os.MkdirAll(filepath.Dir(p.LogFile()), 0o700); err != nil {
		return fmt.Errorf("ログディレクトリの作成に失敗: %w", err)
	}
	// 子の stdout / stderr は現行ログへ向ける (panic の痕跡を残すため)。子が日付境界で
	// rotate しても、この fd は rename 後のファイルを指し続けるので追記先は失われない。
	logFile, err := os.OpenFile(p.LogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- 固定パターンから組み立てたパス
	if err != nil {
		return fmt.Errorf("ログファイルを開けません: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	args := []string{"daemon", "--foreground"}
	if dryRun {
		// detach 起動でも dry-run を引き継ぐ。引き継がないと --ensure --dry-run が
		// 実際には送信する daemon を生む、という最も危険な取り違えになる。
		args = append(args, "--dry-run")
	}
	cmd := exec.Command(exe, args...) // #nosec G204 -- 自分自身の実行ファイルパスのみを起動する
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon の起動に失敗: %w", err)
	}
	// Wait しない (detach させるため)。Release は Wait を呼ばない場合の後始末。
	return cmd.Process.Release()
}

// watchStopWait は購読を止めてから後始末 (events の子の刈り取り) を待つ上限。子は kill から 100ms で刈り取られる。
const watchStopWait = 2 * time.Second

// startWatch は client が多重化ツールの出来事を購読できれば始める。tick の pane の一覧が fork せずに取れるようになり、
// 購読できない間は tick が全列挙する。返す stop は購読を止めて後始末を待つ。daemon は終わる前に呼び、
// 入れ替わりのたびに events の子が多重化ツールに繋がったまま残らないようにする。
func startWatch(ctx context.Context, client multiplexerClient, log *applog.Logger) (stop func()) {
	w, ok := client.(multiplexer.Watcher)
	if !ok {
		return func() {}
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := w.Watch(watchCtx, log.Logf)
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(watchStopWait):
			log.Logf("購読の後始末が %s で終わりませんでした", watchStopWait)
		}
	}
}

// reachTimeout は起動時に多重化ツールの server への到達を待つ上限。
const reachTimeout = 10 * time.Second

// connect は多重化ツールに繋ぎ、server に到達できることを確かめてから、daemon が使う形 (multiplexerClient) で返す。
// 到達できなければ起動しない。
func connect(ctx context.Context, store sessionstate.Store) (kind string, client multiplexerClient, err error) {
	kind, execClient, err := backend.Connect(os.Getenv, store.Context)
	if err != nil {
		return "", nil, err
	}
	reachCtx, cancel := context.WithTimeout(ctx, reachTimeout)
	defer cancel()
	if err := execClient.Reachable(reachCtx); err != nil {
		return "", nil, fmt.Errorf("%s に到達できないため起動しません: %w", kind, err)
	}
	return kind, execClient, nil
}

// assembleRuntime は daemon のループが回す runtime を組み立て、state の置き場と Claude Code の設定ディレクトリを
// 配線する。dry-run なら送信を握りつぶす包みを被せる。起こした session の後片付けは包む前の client で行い、dry-run なら
// reaper が閉じない。
func assembleRuntime(raw multiplexerClient, kind string, p apppath.Paths, store sessionstate.Store, log *applog.Logger, dryRun bool, now time.Time) *daemonRuntime {
	client := raw
	if dryRun {
		client = dryrun.New(raw, log)
		log.Logf("dry-run モード: 検知と判定は通常どおり行うが、pane への送信とラベル更新と、起こした session の後片付けはしない")
	}
	runtime := newDaemonRuntime(client, log, p.StatusFile(), store, time.Sleep, now)
	runtime.backend = kind
	runtime.attachStateDir(p.StateDir())
	runtime.refPoll = refpoll.New(runtime.edges, runtime.refs, refpoll.GH, runtime.liveRefSessions, nil, log)
	if c, ok := raw.(reaper.Client); ok {
		runtime.reaper = reaper.New(runtime.spawned, c, log, dryRun)
	}
	if claudeConfig, err := apppath.ClaudeConfigDir(); err != nil {
		log.Logf("Claude Code の設定ディレクトリを決められないため、transcript を読めず上限を判定できません。台帳にセッション名も書きません: %v", err)
	} else {
		runtime.attachClaudeConfigDir(claudeConfig)
	}
	return runtime
}

// RunForeground は --foreground の本体。SessionStart から detach 起動された
// 子プロセスも最終的にここへ辿り着く。
func RunForeground(ctx context.Context, p apppath.Paths, dryRun bool) error {
	// lock は設定や rex を見る前に取る。後に回すと、その間に起きた別の process と 2 つ進む。
	// 以降のどこで戻っても、process の終了で OS が lock を解放する。
	lock, err := daemonstate.AcquireLock(daemonstate.LockFile(p))
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return fmt.Errorf("設定の読み込みに失敗: %w", err)
	}
	if !cfg.Enabled {
		return fmt.Errorf("%s の enabled が false のため起動しません", p.ConfigFile())
	}

	store := sessionstate.New(p.StateDir())
	kind, client, err := connect(ctx, store)
	if err != nil {
		return err
	}

	if err := daemonstate.WritePIDFile(p.PIDFile(), os.Getpid()); err != nil {
		return err
	}

	log := applog.NewLogger(p.LogFile(), time.Now)
	defer func() { _ = log.Close() }()
	log.Logf("daemon 起動 (pid %d, backend=%s, pollIntervalSeconds=%d)", os.Getpid(), kind, cfg.PollIntervalSeconds)

	now := time.Now()
	runtime := assembleRuntime(client, kind, p, store, log, dryRun, now)

	// 実行ファイルの差し替えに気づくための基準。取得できなければ更新検知だけを
	// 諦める (path が空の stamp は changed() が常に false を返す)。
	stamp, stampErr := currentBinaryStamp()
	if stampErr != nil {
		log.Logf("実行ファイルの情報を取得できないため、更新の自動反映を無効にします: %v", stampErr)
	}

	runtime.housekeep(p.LogFile(), now)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	loopCtx, stop := context.WithCancel(ctx)
	defer stop()

	defer startWatch(loopCtx, runtime.client, log)()

	cleanup := func(reason string) {
		if err := daemonstate.RemoveOwnPIDFile(p.PIDFile(), os.Getpid()); err != nil {
			log.Logf("PID ファイルの削除に失敗: %v", err)
		}
		log.Logf("daemon 終了 (pid %d): %s", os.Getpid(), reason)
	}

	pollInterval := time.Duration(cfg.PollIntervalSeconds) * time.Second

	for {
		select {
		case sig := <-sigCh:
			cleanup(fmt.Sprintf("シグナル %v を受信", sig))
			return nil
		case <-loopCtx.Done():
			cleanup("context がキャンセルされた")
			return nil
		case <-time.After(pollInterval):
			// 実行ファイルが差し替わっていたら、後始末をしてから新しい版へ
			// 入れ替わる (理由は selfupdate.go を参照)。
			if stamp.changed() {
				cleanup("実行ファイルが更新されたため入れ替わります")
				replaceSelf(log, lock, stamp, dryRun, p)
				return nil
			}

			tickNow := time.Now()
			if runtime.housekeepDue(tickNow) {
				runtime.housekeep(p.LogFile(), tickNow)
			}
			shutdown, tickCfg, tickErr := runtime.tick(loopCtx, p.ConfigFile(), tickNow)
			if tickErr != nil {
				log.Logf("tick でエラー: %v", tickErr)
			}
			pollInterval = time.Duration(tickCfg.PollIntervalSeconds) * time.Second
			if shutdown {
				cleanup("自己終了条件を満たした")
				return nil
			}
		}
	}
}
