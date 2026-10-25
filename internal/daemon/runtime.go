// Package daemon は agents-daemon の監視デーモンの実体 (ポーリングループ、
// 起動の排他 (lock) と PID ファイル、status snapshot、dry-run 経路) を提供する。cmd/agents-daemon/internal/daemoncli の
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
	"github.com/usadamasa/agents-daemon/internal/backend"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/daemon/ledger"
	"github.com/usadamasa/agents-daemon/internal/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/daemon/state"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
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
}

// daemonRuntime は 1 回の --foreground 実行に閉じたループの可変状態をまとめる。
// pane ごとの状態機械は PaneID (rex の block ID) をキーにする。
type daemonRuntime struct {
	client multiplexer.Client
	// backend は client の多重化ツールの種類。status.json に書くだけで判定には使わない。
	backend string
	log     *applog.Logger
	// statusPath と store はプロセスの生存期間で固定なのでここに置く。
	statusPath string
	store      sessionstate.Store
	// tasks は hook が書くタスクの記録 (internal/tasklog)。daemon は housekeeping で消すだけ。
	// ゼロ値は何も消さない。
	tasks tasklog.Store
	// edges は `edge add` が書くセッションをまたぐ辺 (internal/edge)。daemon は tick ごとに session:<名前> の
	// 端点を台帳のセッションに結びつけ、housekeeping で消す。ゼロ値は何もしない。
	edges edge.Store
	// sessions はセッション台帳 (internal/daemon/ledger)。daemon だけが tick ごとに書き、housekeeping で消す。
	// ゼロ値は書かない。
	sessions ledger.Store
	// parking は `park add` が書くパーキングのメモ (internal/parking)。daemon は housekeeping で消すだけ。
	// ゼロ値は何も消さない。
	parking parking.Store
	// eventLog は CLI と mod が書く出来事のログ (internal/eventlog)。daemon は housekeeping で rotate 済みの
	// ものを消すだけ。ゼロ値は何も消さない。
	eventLog string
	// limits は上限の出来事の記録 (internal/daemon/limitlog)。daemon が書き、housekeeping で消す。ゼロ値は書かない。
	limits              limitlog.Store
	sleep               func(time.Duration)
	startedAt           time.Time
	panes               map[string]*paneEntry
	consecutiveFailures int
	idleSince           time.Time // ゼロ値は「非アイドル (Claude pane が存在する)」
	lastHousekeepAt     time.Time // ゼロ値は「まだ 1 度も掃除していない」
}

func newDaemonRuntime(client multiplexer.Client, log *applog.Logger, statusPath string, store sessionstate.Store, sleep func(time.Duration), startedAt time.Time) *daemonRuntime {
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

	classifier, err := detect.NewClassifier(cfg.CustomPatterns)
	if err != nil {
		return false, cfg, fmt.Errorf("分類器の構築に失敗: %w", err)
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

	// アカウント全体で最新のウィンドウは tick ごとに 1 回だけ読み、全 pane で共有する。
	// 自分の state を持たない pane (上限中に開いた新規セッション) の起床時刻はここから引く。
	account, accountErr := r.store.RateLimits.LatestWindow(now)
	if accountErr != nil {
		r.log.Logf("アカウント全体の利用上限 state の走査に失敗、無しとして継続します: %v", accountErr)
		account = nil
	}

	// Tick が分類した画面は pane ごとに 1 つ。pane は順に処理するので、直前の pane の観測を持ち越さないよう
	// pane ごとに捨てる。
	var observed *observation
	deps := monitor.Deps{
		Client:     r.client,
		Classifier: classifier,
		Config:     cfg,
		Sleep:      r.sleep,
		Account:    account,
		Observe: func(_ multiplexer.Pane, kind detect.Kind, screen string) {
			observed = &observation{kind: kind, screen: screen}
		},
	}

	seen := make(map[string]bool, len(panes))
	claudeCount := 0
	statusPanes := make([]state.PaneStatus, 0, len(panes))

	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		claudeCount++
		seen[pane.PaneID] = true

		entry, ok := r.panes[pane.PaneID]
		if !ok {
			entry = &paneEntry{state: &monitor.PaneState{}}
			r.panes[pane.PaneID] = entry
		}
		entry.lastSeenAt = now
		r.logUnknownReason(pane, entry)

		// state はセッションごとに別ファイル。pane に対応するものだけを読む。
		rateState, stateErr := r.store.RateLimits.Load(pane.SessionID)
		if stateErr != nil {
			r.log.Logf("pane %s の利用上限 state の読み込みに失敗、state 無しとして継続します: %v", pane.PaneID, stateErr)
			rateState = nil
		}

		observed = nil
		outcome, tickErr := monitor.Tick(ctx, deps, pane, entry.state, rateState, now)
		if r.logOutcome(pane, outcome, tickErr) && observed != nil && isLimitEvent(outcome) {
			r.recordLimitEvent(ctx, pane, entry, outcome, observed, rateState, account, cfg, now)
		}
		statusPanes = append(statusPanes, paneStatus(pane, entry, outcome, now))
	}

	r.forgetUnseen(seen)

	if err := state.WriteStatusSnapshot(r.statusPath, os.Getpid(), r.backend, r.startedAt, now, statusPanes); err != nil {
		r.log.Logf("status snapshot の書き込みに失敗: %v", err)
	}
	r.recordSessions(statusPanes, now)

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

// paneStatus は status.json に書く pane 1 枚ぶんのスナップショットを組む。
func paneStatus(pane multiplexer.Pane, entry *paneEntry, outcome monitor.Outcome, now time.Time) state.PaneStatus {
	return state.PaneStatus{
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
	// 上限の検知と送信の行には Claude Code の自己申告 (record) を添える。上限到達時に record が何になるかは
	// 未実測で (#77)、ここと limit-events/ が実測の記録になる。logKey には混ぜない (msg が変わるだけで同じ出来事を
	// 書き直さない)。
	record := multiplexer.RecordLabel(pane)
	switch outcome {
	case monitor.OutcomeMonitoring, monitor.OutcomeStillWaiting:
		return false
	case monitor.OutcomeWaiting:
		r.log.Logf("pane %s (%s): 利用上限/一時エラーを検知し待機に入りました (%s)", pane.PaneID, pane.CWD, record)
	case monitor.OutcomeRetried:
		r.log.Logf("pane %s (%s): 再開メッセージを送信しました (%d 回目) (%s)", pane.PaneID, pane.CWD, entry.state.Attempts, record)
	case monitor.OutcomeRecoveredByUser:
		r.log.Logf("pane %s (%s): ユーザー自身が復帰したため監視状態へ戻します", pane.PaneID, pane.CWD)
	case monitor.OutcomeMaxRetries:
		r.log.Logf("pane %s (%s): 再試行上限に達したためクールダウンに入ります", pane.PaneID, pane.CWD)
	case monitor.OutcomeGateSuppressed:
		r.log.Logf("pane %s (%s): 画面は上限に見えましたが statusline の使用率が閾値未満のため抑制しました (%s)", pane.PaneID, pane.CWD, record)
	case monitor.OutcomeNotClaudeAgent:
		r.log.Logf("pane %s (%s): Claude agent でなくなったため待機を延長します", pane.PaneID, pane.CWD)
	case monitor.OutcomeNativeAutoContinue:
		r.log.Logf("pane %s (%s): Claude Code 自身の auto-continue が解除を待っているため手を引きます (%s)", pane.PaneID, pane.CWD, record)
	case monitor.OutcomeResumeEnter:
		r.log.Logf("pane %s (%s): 上限が解除済みで Enter 待ちだったため Enter を送りました (%s)", pane.PaneID, pane.CWD, record)
	case monitor.OutcomeHardCap:
		r.log.Logf("pane %s (%s): 待っても解除されない上限 (spend limit / usage credit) のため待機に入りません (%s)", pane.PaneID, pane.CWD, record)
	case monitor.OutcomeBlockedByPrompt:
		r.log.Logf("pane %s (%s): Claude Code が利用者の答えを待つ prompt を開いているため送信を見送り待機を延長します (%s)", pane.PaneID, pane.CWD, record)
	default:
		r.log.Logf("pane %s (%s): %s", pane.PaneID, pane.CWD, outcome)
	}
	return true
}

// observation は Tick が分類した画面と kind (monitor.Deps.Observe が渡す)。
type observation struct {
	kind   detect.Kind
	screen string
}

// isLimitEvent は outcome が上限の出来事 (後から検証する材料を残す価値があるもの) かを返す。
// 静かな結果、読めなかった tick、Claude でなくなった pane は対象外。
func isLimitEvent(outcome monitor.Outcome) bool {
	switch outcome {
	case monitor.OutcomeWaiting, monitor.OutcomeRetried, monitor.OutcomeRecoveredByUser, monitor.OutcomeMaxRetries,
		monitor.OutcomeGateSuppressed, monitor.OutcomeNativeAutoContinue, monitor.OutcomeResumeEnter,
		monitor.OutcomeHardCap, monitor.OutcomeBlockedByPrompt, monitor.OutcomeSendError:
		return true
	default:
		return false
	}
}

// recordLimitEvent は上限の出来事 1 件を limit-events/ へ残す。block の高さと screen はこのときだけ打つ
// (毎 tick に 2 fork 足さない)。取れなくても出来事は残す。書けなくても tick は止めない。
func (r *daemonRuntime) recordLimitEvent(ctx context.Context, pane multiplexer.Pane, entry *paneEntry, outcome monitor.Outcome,
	observed *observation, rateState, account *ratelimit.State, cfg config.Config, now time.Time) {
	ev := limitlog.Event{
		At:        now,
		PaneID:    pane.PaneID,
		SessionID: pane.SessionID,
		CWD:       pane.CWD,
		Outcome:   outcome.String(),
		Kind:      observed.kind.String(),
		Attempts:  entry.state.Attempts,
		WaitUntil: zeroableTime(entry.state.WaitUntil),
		Record:    limitlog.Record{AgentStatus: string(pane.AgentStatus), Kind: pane.AgentKind, Msg: pane.AgentMessage},
		RateLimit: limitWindow(rateState),
		Account:   limitWindow(account),
		Settings:  limitlog.Settings{ReadLines: cfg.ReadLines, DetectionTailLines: cfg.DetectionTailLines},
		Screen:    observed.screen,
	}
	if detect.LowerPriorityActive(observed.screen, cfg.DetectionTailLines) {
		ev.LowerPriority = true
		r.log.Logf("pane %s: lower-priority mode の作業中の通知がある画面を %s と判定した (作業中の pane を待機に入れている可能性、#251)",
			pane.PaneID, observed.kind)
	}
	if geom, err := r.client.PaneGeometry(ctx, pane.PaneID); err != nil {
		ev.GeometryError = err.Error()
	} else {
		ev.Geometry = &geom
	}
	if err := r.limits.Append(ev); err != nil {
		r.log.Logf("pane %s: 上限の出来事の記録に失敗: %v", pane.PaneID, err)
	}
}

// limitWindow は利用上限 state を記録の形に写す。無ければ nil (欄ごと出さない)。
func limitWindow(state *ratelimit.State) *limitlog.Window {
	if state == nil {
		return nil
	}
	return &limitlog.Window{
		UsedPercentage: state.FiveHourUsed,
		ResetsAt:       state.FiveHourResetsAt,
		ObservedAt:     state.ObservedAt,
		SessionID:      state.SessionID,
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

// EnsureDaemon は生存している daemon が無ければこのバイナリを detach 起動する。
// SessionStart hook から呼ばれる想定なので、常に高速に戻り、hook を絶対に
// ブロック/失敗させない設計にする。
func EnsureDaemon(p apppath.Paths, dryRun bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("実行ファイルパスの取得に失敗: %w", err)
	}
	return ensureDaemon(p, func() error { return spawnDetached(exe, dryRun, p) })
}

// ensureDaemon は lock を試し、取れなければ既に動いているとして何もしない。取れたら離してから spawn を呼ぶ。
// 子は RunForeground で lock を取り直す。親が握ったまま起こすと子は ErrAlreadyRunning で終わり、daemon が 0 になる
// (os.File の fd は CLOEXEC なので子へは渡らない)。離してから子が取るまでの間に別の --ensure が入っても、
// lock を取れた 1 つだけが daemon になる。
func ensureDaemon(p apppath.Paths, spawn func() error) error {
	lock, err := state.AcquireLock(state.LockFile(p))
	if err != nil {
		if errors.Is(err, state.ErrAlreadyRunning) {
			return nil // 既に動いている。何もしない。
		}
		return err
	}
	if err := lock.Close(); err != nil {
		return fmt.Errorf("lock を離せません: %w", err)
	}
	return spawn()
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

// RunForeground は --foreground の本体。SessionStart から detach 起動された
// 子プロセスも最終的にここへ辿り着く。
func RunForeground(ctx context.Context, p apppath.Paths, dryRun bool) error {
	// lock は設定や rex を見る前に取る。後に回すと、その間に起きた別の process と 2 つ進む。
	// 以降のどこで戻っても、process の終了で OS が lock を解放する。
	lock, err := state.AcquireLock(state.LockFile(p))
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
	kind, client, err := backend.Connect(os.Getenv, store.Context)
	if err != nil {
		return err
	}
	reachCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	reachErr := client.Reachable(reachCtx)
	cancel()
	if reachErr != nil {
		return fmt.Errorf("%s に到達できないため起動しません: %w", kind, reachErr)
	}

	if err := state.WritePIDFile(p.PIDFile(), os.Getpid()); err != nil {
		return err
	}

	log := applog.NewLogger(p.LogFile(), time.Now)
	defer func() { _ = log.Close() }()
	log.Logf("daemon 起動 (pid %d, backend=%s, pollIntervalSeconds=%d)", os.Getpid(), kind, cfg.PollIntervalSeconds)

	if dryRun {
		client = newDryRunClient(client, log)
		log.Logf("dry-run モード: 検知と判定は通常どおり行うが、pane への送信とラベル更新はしない")
	}

	// 実行ファイルの差し替えに気づくための基準。取得できなければ更新検知だけを
	// 諦める (path が空の stamp は changed() が常に false を返す)。
	stamp, stampErr := currentBinaryStamp()
	if stampErr != nil {
		log.Logf("実行ファイルの情報を取得できないため、更新の自動反映を無効にします: %v", stampErr)
	}

	now := time.Now()
	runtime := newDaemonRuntime(client, log, p.StatusFile(), store, time.Sleep, now)
	runtime.backend = kind
	runtime.attachStateDir(p.StateDir())
	runtime.housekeep(p.LogFile(), now)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	loopCtx, stop := context.WithCancel(ctx)
	defer stop()

	cleanup := func(reason string) {
		if err := state.RemoveOwnPIDFile(p.PIDFile(), os.Getpid()); err != nil {
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
