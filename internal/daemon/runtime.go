// Package daemon は agents-daemon の監視デーモンの実体 (ポーリングループ､
// PID ファイル､status snapshot､dry-run 経路) を提供する｡cmd_daemon.go /
// cmd_status.go / cmd_stop.go はこのパッケージを薄く呼び出すだけの cobra 配線に徹する｡
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// maxConsecutiveTickFailures は herdr pane list の取得にこの回数だけ連続で
// 失敗したら daemon が自己終了する閾値｡herdr が落ちたまま無限に回り続けて
// ログを埋め尽くすのを防ぐ｡
const maxConsecutiveTickFailures = 10

// metadataSource は herdr pane report-metadata --source に渡す固定値｡
// monitor パッケージ内の同名定数 (非公開) と揃えてある｡
const metadataSource = "agents-daemon"

// paneEntry は daemon が pane 1 枚ぶんに保持する監視状態｡
type paneEntry struct {
	state      *monitor.PaneState
	pane       herdrcli.Pane // 直近の tick で観測した pane 情報 (シャットダウン時のラベル解除に使う)
	lastSeenAt time.Time
	lastLogKey string // 直前にログへ書いた内容の識別子 (同じ状態が続く間の重複を抑える)
}

// daemonRuntime は 1 回の --foreground 実行に閉じたループの可変状態をまとめる｡
// pane ごとの状態機械は terminal_id をキーにする (pane_id は herdr 再起動で
// 変わりうるが､terminal_id はその herdr 実行内で安定するため)｡
type daemonRuntime struct {
	client herdrcli.Client
	log    *applog.Logger
	// statusPath と store はプロセスの生存期間で固定なのでここに置く｡
	statusPath string
	store      sessionstate.Store
	cache      *sessionstate.CacheLoader
	// dryRun は pane への送信をしない稼働｡client 側 (dryRunClient) が握りつぶすが､
	// cache の ack マーカーは client を通らないのでここでも見る｡書くと利用者自身の
	// 次の prompt が TTL guard を素通りする｡
	dryRun              bool
	sleep               func(time.Duration)
	startedAt           time.Time
	panes               map[string]*paneEntry
	consecutiveFailures int
	idleSince           time.Time // ゼロ値は「非アイドル (Claude pane が存在する)」
	lastHousekeepAt     time.Time // ゼロ値は「まだ 1 度も掃除していない」
}

func newDaemonRuntime(client herdrcli.Client, log *applog.Logger, statusPath string, store sessionstate.Store, dryRun bool, sleep func(time.Duration), startedAt time.Time) *daemonRuntime {
	return &daemonRuntime{
		client:     client,
		store:      store,
		cache:      sessionstate.NewCacheLoader(store),
		log:        log,
		statusPath: statusPath,
		dryRun:     dryRun,
		sleep:      sleep,
		startedAt:  startedAt,
		panes:      map[string]*paneEntry{},
	}
}

// tick は 1 回分のポーリングを処理する｡設定を毎回読み直すことで､daemon を
// 再起動せずに設定変更 (閾値・パターン等) を反映できる (参考実装からの踏襲)｡
// 返り値の cfg は呼び出し元がポーリング間隔の更新に使う｡shutdown=true は
// 呼び出し元がループを終了すべきことを表す｡
func (r *daemonRuntime) tick(ctx context.Context, cfgPath string, now time.Time) (shutdown bool, cfg config.Config, err error) {
	cfg, cfgErr := config.Load(cfgPath)
	if cfgErr != nil {
		r.log.Logf("設定の読み込みでエラー､デフォルト値で継続します: %v", cfgErr)
	}

	classifier, err := detect.NewClassifier(cfg.CustomPatterns, cfg.CustomTransientPatterns)
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

	// アカウント全体で最新のウィンドウは tick ごとに 1 回だけ読み､全 pane で共有する｡
	// 自分の state を持たない pane (上限中に開いた新規セッション) の起床時刻はここから引く｡
	account, accountErr := r.store.LatestWindow(now)
	if accountErr != nil {
		r.log.Logf("アカウント全体の利用上限 state の走査に失敗､無しとして継続します: %v", accountErr)
		account = nil
	}

	deps := monitor.Deps{
		Client:     r.client,
		Classifier: classifier,
		Config:     cfg,
		Sleep:      r.sleep,
		Account:    account,
		// compact 関連の state は監視状態の最後まで到達した pane ぶんだけ読む｡
		// 読み込みエラーは「state 無し」として継続する (statusline や hook の
		// 書きかけを踏んだだけで監視全体を止めない)｡
		CompactState: func(sessionID string) *sessionstate.Compact {
			state, err := r.store.LoadCompact(sessionID)
			if err != nil {
				r.log.Logf("session %s の compact state の読み込みに失敗､state 無しとして継続します: %v", sessionID, err)
				return nil
			}
			return state
		},
		// cache の状態は idle compact の判定と prompt を送る直前に読む｡
		// 読み込みエラーは「cache の状態が無い」(送らない側) として継続する｡
		CacheState:        r.loadCache,
		CacheAck:          r.writeCacheAck,
		MarkIdleCompacted: r.markIdleCompacted,
	}

	seen := make(map[string]bool, len(panes))
	sessions := make(map[string]bool, len(panes))
	claudeCount := 0
	statusPanes := make([]PaneStatus, 0, len(panes))

	for _, pane := range panes {
		if !herdrcli.IsClaudeAgent(pane) {
			continue
		}
		claudeCount++
		seen[pane.TerminalID] = true
		if sid := pane.SessionID(); sid != "" {
			sessions[sid] = true
		}

		entry, ok := r.panes[pane.TerminalID]
		if !ok {
			entry = &paneEntry{state: &monitor.PaneState{}}
			r.panes[pane.TerminalID] = entry
		}
		entry.pane = pane
		entry.lastSeenAt = now

		// state はセッションごとに別ファイル｡pane に対応するものだけを読む｡
		rateState, stateErr := r.store.LoadRateLimit(pane.SessionID())
		if stateErr != nil {
			r.log.Logf("pane %s の利用上限 state の読み込みに失敗､state 無しとして継続します: %v", pane.PaneID, stateErr)
			rateState = nil
		}

		outcome, tickErr := monitor.Tick(ctx, deps, pane, entry.state, rateState, now)
		r.logOutcome(pane, outcome, tickErr)
		statusPanes = append(statusPanes, r.paneStatus(pane, entry, outcome, now))
	}

	r.forgetUnseen(seen, sessions)

	if err := writeStatusSnapshot(r.statusPath, os.Getpid(), r.startedAt, now, statusPanes); err != nil {
		r.log.Logf("status snapshot の書き込みに失敗: %v", err)
	}

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

// forgetUnseen は消えた pane のエントリと､消えた session の cache の memo を捨てる｡
// 捨てずに放置するとどちらも pane の入れ替わりのたびに無限に育ってしまう｡
func (r *daemonRuntime) forgetUnseen(terminals, sessions map[string]bool) {
	for terminalID := range r.panes {
		if !terminals[terminalID] {
			delete(r.panes, terminalID)
		}
	}
	r.cache.Retain(sessions)
}

// paneStatus は status.json に書く pane 1 枚ぶんのスナップショットを組む｡
func (r *daemonRuntime) paneStatus(pane herdrcli.Pane, entry *paneEntry, outcome monitor.Outcome, now time.Time) PaneStatus {
	return PaneStatus{
		PaneID:        pane.PaneID,
		TerminalID:    pane.TerminalID,
		CWD:           pane.CWD,
		AgentStatus:   string(pane.AgentStatus),
		MonitorStatus: monitorStatusString(entry.state.Status),
		WaitUntil:     zeroableTime(entry.state.WaitUntil),
		Attempts:      entry.state.Attempts,
		LastOutcome:   outcome.String(),
		LastTickAt:    now,
		Cache:         cacheStatus(r.statusCache(pane), now),
	}
}

// statusCache は status に載せる cache の状態｡working な pane の transcript は応答のたびに
// 伸びるので読み直さず､前回読んだ値を出す｡
func (r *daemonRuntime) statusCache(pane herdrcli.Pane) *sessionstate.Cache {
	if pane.AgentStatus == herdrcli.AgentStatusWorking {
		return r.cache.Cached(pane.SessionID())
	}
	return r.loadCache(pane.SessionID())
}

// loadCache は pane のセッションの prompt cache の状態を transcript から求める｡無ければ nil｡
// 読み込みエラーはログに残して nil を返す (idle compact は送らず､ack は sentinel になる)｡
func (r *daemonRuntime) loadCache(sessionID string) *sessionstate.Cache {
	c, err := r.cache.Load(sessionID)
	if err != nil {
		r.log.Logf("session %s の cache の状態の読み込みに失敗､状態無しとして継続します: %v", sessionID, err)
		return nil
	}
	return c
}

// writeCacheAck は cache の ack マーカーを書く｡dry-run では書かない (理由は dryRun を参照)｡
func (r *daemonRuntime) writeCacheAck(sessionID, value string) error {
	if r.dryRun {
		r.log.Logf("dry-run: session %s の cache ack (%s) は書きません", sessionID, value)
		return nil
	}
	return r.store.WriteCacheAck(sessionID, value)
}

// markIdleCompacted は idle compact の marker を書く｡dry-run では /compact も送らないので書かない｡
func (r *daemonRuntime) markIdleCompacted(sessionID string) error {
	if r.dryRun {
		r.log.Logf("dry-run: session %s の idle compact marker は書きません", sessionID)
		return nil
	}
	return r.store.MarkIdleCompacted(sessionID)
}

// logOutcome は tick 1 回の結果をログへ書く｡「静かな tick」(監視中で異常無し､
// 対象外､待機継続中) はログしない｡次の朝に読んで意味が分かる粒度に絞る:
// 上限検知と待ち時間､送信の実施､ユーザーの自己復帰､ゲート抑制､エラー｡
//
// prompt を送った出来事には cache の状態を添える (cacheSuffix)｡失効後の送信は文脈全体を
// 書き直すので､どれだけ書き直したかを後から追えるようにする｡
func (r *daemonRuntime) logOutcome(pane herdrcli.Pane, outcome monitor.Outcome, err error) {
	// 同じ状態が続く間は書かない｡tick は既定 5 秒ごとなので､待機や抑制が
	// 続くだけで日に数千行に膨れ､後から読めるログでなくなる｡状態が切り替わった
	// ときだけ書き､静かな結果も key は更新して「戻ってきた」を検出できるようにする｡
	entry := r.panes[pane.TerminalID]
	key := outcome.String()
	if err != nil {
		key += "|" + err.Error()
	}
	if outcome == monitor.OutcomeRetried && entry != nil {
		// 送信は 1 回ずつが独立した出来事なので､試行回数を key に混ぜて毎回残す｡
		key = fmt.Sprintf("%s|%d", key, entry.state.Attempts)
	}
	if entry != nil {
		repeated := entry.lastLogKey == key
		entry.lastLogKey = key
		if repeated {
			return
		}
	}

	if err != nil {
		r.log.Logf("pane %s (%s): %s でエラー: %v", pane.PaneID, pane.CWD, outcome, err)
		return
	}
	switch outcome {
	case monitor.OutcomeMonitoring, monitor.OutcomeStillWaiting:
		return
	case monitor.OutcomeWaiting:
		r.log.Logf("pane %s (%s): 利用上限/一時エラーを検知し待機に入りました", pane.PaneID, pane.CWD)
	case monitor.OutcomeRetried:
		r.log.Logf("pane %s (%s): 再開メッセージを送信しました (%d 回目)%s", pane.PaneID, pane.CWD, entry.state.Attempts, r.cacheSuffix(pane, entry))
	case monitor.OutcomeRecoveredByUser:
		r.log.Logf("pane %s (%s): ユーザー自身が復帰したため監視状態へ戻します", pane.PaneID, pane.CWD)
	case monitor.OutcomeMaxRetries:
		r.log.Logf("pane %s (%s): 再試行上限に達したためクールダウンに入ります", pane.PaneID, pane.CWD)
	case monitor.OutcomeGateSuppressed:
		r.log.Logf("pane %s (%s): 画面は上限に見えましたが statusline の使用率が閾値未満のため抑制しました", pane.PaneID, pane.CWD)
	case monitor.OutcomeNotClaudeAgent:
		r.log.Logf("pane %s (%s): Claude agent でなくなったため待機を延長します", pane.PaneID, pane.CWD)
	case monitor.OutcomeNativeAutoContinue:
		r.log.Logf("pane %s (%s): Claude Code 自身の auto-continue が解除を待っているため手を引きます", pane.PaneID, pane.CWD)
	case monitor.OutcomeResumeEnter:
		r.log.Logf("pane %s (%s): 上限が解除済みで Enter 待ちだったため Enter を送りました", pane.PaneID, pane.CWD)
	case monitor.OutcomeHardCap:
		r.log.Logf("pane %s (%s): 待っても解除されない上限 (spend limit / usage credit) のため待機に入りません", pane.PaneID, pane.CWD)
	default:
		// compact 周りは別関数に分けてある (1 つの switch に並べると
		// 循環複雑度が lint の閾値を超える)｡
		if msg, sent := compactOutcomeMessage(outcome); msg != "" {
			suffix := ""
			if sent {
				suffix = r.cacheSuffix(pane, entry)
			}
			r.log.Logf("pane %s (%s): %s%s", pane.PaneID, pane.CWD, msg, suffix)
			return
		}
		r.log.Logf("pane %s (%s): %s", pane.PaneID, pane.CWD, outcome)
	}
}

// compactOutcomeMessage は compact 周りの Outcome に対応するログ本文と､それが prompt の
// 送信を伴う出来事かを返す｡該当しなければ空文字列｡
func compactOutcomeMessage(outcome monitor.Outcome) (msg string, sent bool) {
	switch outcome {
	case monitor.OutcomeCompactNudged:
		return "compact 直後に止まっていたため継続を促しました (画面判定)", true
	case monitor.OutcomeCompactNudgeCapped:
		return "compact 直後の停止へ継続を促した回数が上限に達したため送りません (詰まっている可能性があります)", false
	case monitor.OutcomeCompactPrepSent:
		return "context 使用率が閾値を超えたため compact-prep を投入しました", true
	case monitor.OutcomeCompactSent:
		return "compact-prep の state file を確認したため /compact を投入しました", true
	case monitor.OutcomeCompactPrepTimeout:
		return "compact-prep の state file が現れないまま時間切れになりました", false
	case monitor.OutcomeCompactResumed:
		return "圧縮完了 marker を検知したため作業の再開を促しました", true
	case monitor.OutcomeIdleCompactPrepSent:
		return "idle compact: cache の失効が近いため compact-prep を投入しました", true
	case monitor.OutcomeIdleCompactSent:
		return "idle compact: marker を書いて /compact を投入しました (利用者が戻るまで再開は送りません)", true
	case monitor.OutcomeIdleCompactShortTTL:
		return "idle compact: cache の TTL が短く失効前に compact を終えられないためスキップしました", false
	default:
		return "", false
	}
}

// cacheSuffix は送信ログに添える cache の状態｡形は次のいずれか｡
//
//	 / cache=warm (経過 3 分､TTL 60 分)
//	 / cache=expired (経過 312 分､TTL 60 分､context 使用率 63%､ack 済み)
//	 / cache=unknown (sidecar 無し､ack=daemon-unknown)
//
// 失効後の送信は文脈全体を書き直すので context 使用率を併記し､どれだけ書き直したかを
// 後から追えるようにする｡スラッシュの prompt は guard を素通りするので ack は無い｡
func (r *daemonRuntime) cacheSuffix(pane herdrcli.Pane, entry *paneEntry) string {
	if entry == nil {
		return ""
	}
	send := entry.state.LastSend
	var b strings.Builder
	fmt.Fprintf(&b, " / cache=%s (", send.State())
	switch c := send.Cache; {
	case c == nil && pane.SessionID() == "":
		b.WriteString("session 未紐づけ")
	case c == nil:
		b.WriteString("sidecar 無し")
	default:
		fmt.Fprintf(&b, "経過 %d 分､TTL %d 分", int(send.At.Sub(c.LastRequestAt)/time.Minute), int(c.TTL/time.Minute))
		if c.Expired(send.At) {
			if cs, err := r.store.LoadCompact(pane.SessionID()); err == nil && cs != nil && cs.HasContext {
				fmt.Fprintf(&b, "､context 使用率 %.0f%%", cs.UsedPercentage)
			}
		}
	}
	switch {
	case send.AckErr != nil:
		fmt.Fprintf(&b, "､ack の書き込みに失敗: %v", send.AckErr)
	case send.Ack != "" && r.dryRun:
		b.WriteString("､ack は dry-run で省略")
	case send.Ack == sessionstate.CacheAckUnknown:
		fmt.Fprintf(&b, "､ack=%s", send.Ack)
	case send.Ack != "":
		b.WriteString("､ack 済み")
	case send.Slash:
		b.WriteString("､スラッシュなので ack なし")
	}
	b.WriteString(")")
	return b.String()
}

// clearAllLabels は待機ラベルを表示している可能性のある全 pane へ
// ClearStateLabels を送る｡シャットダウン時に「ラベルが残ったまま」に
// ならないようにするためのベストエフォート処理で､失敗してもログするだけで
// シャットダウン自体は止めない｡
func (r *daemonRuntime) clearAllLabels(ctx context.Context) {
	for _, entry := range r.panes {
		if entry.state.Status != monitor.StatusWaiting {
			continue
		}
		if err := r.client.ReportMetadata(ctx, entry.pane.PaneID, herdrcli.ReportMetadataOptions{
			Source:           metadataSource,
			ClearStateLabels: true,
		}); err != nil {
			r.log.Logf("pane %s のラベル解除に失敗: %v", entry.pane.PaneID, err)
		}
	}
}

// EnsureDaemon は生存している daemon が無ければこのバイナリを detach 起動する｡
// SessionStart hook から呼ばれる想定なので､常に高速に戻り､hook を絶対に
// ブロック/失敗させない設計にする｡
func EnsureDaemon(p apppath.Paths, dryRun bool) error {
	if err := checkRunning(p.PIDFile()); err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			return nil // 既に動いている｡何もしない｡
		}
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("実行ファイルパスの取得に失敗: %w", err)
	}
	return spawnDetached(exe, dryRun, p)
}

// spawnDetached は exe を `daemon --foreground` として detach 起動する｡
// 呼び出し元 (hook 経由の EnsureDaemon､実行ファイル更新時の入れ替え) を
// ブロックしないよう､Wait せずに切り離す｡
func spawnDetached(exe string, dryRun bool, p apppath.Paths) error {
	if err := os.MkdirAll(filepath.Dir(p.LogFile()), 0o700); err != nil {
		return fmt.Errorf("ログディレクトリの作成に失敗: %w", err)
	}
	// 子の stdout / stderr は現行ログへ向ける (panic の痕跡を残すため)｡子が日付境界で
	// rotate しても､この fd は rename 後のファイルを指し続けるので追記先は失われない｡
	logFile, err := os.OpenFile(p.LogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- 固定パターンから組み立てたパス
	if err != nil {
		return fmt.Errorf("ログファイルを開けません: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	args := []string{"daemon", "--foreground"}
	if dryRun {
		// detach 起動でも dry-run を引き継ぐ｡引き継がないと --ensure --dry-run が
		// 実際には送信する daemon を生む､という最も危険な取り違えになる｡
		args = append(args, "--dry-run")
	}
	cmd := exec.Command(exe, args...) // #nosec G204 -- 自分自身の実行ファイルパスのみを起動する
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon の起動に失敗: %w", err)
	}
	// Wait しない (detach させるため)｡Release は Wait を呼ばない場合の後始末｡
	return cmd.Process.Release()
}

// RunForeground は --foreground の本体｡SessionStart から detach 起動された
// 子プロセスも最終的にここへ辿り着く｡
func RunForeground(ctx context.Context, p apppath.Paths, dryRun bool) error {
	if err := checkRunning(p.PIDFile()); err != nil {
		return err
	}

	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return fmt.Errorf("設定の読み込みに失敗: %w", err)
	}
	if !cfg.Enabled {
		return fmt.Errorf("%s の enabled が false のため起動しません", p.ConfigFile())
	}

	client := herdrcli.NewExecClient()
	reachCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	reachErr := client.Reachable(reachCtx)
	cancel()
	if reachErr != nil {
		return fmt.Errorf("herdr に到達できないため起動しません: %w", reachErr)
	}

	if err := writePIDFile(p.PIDFile(), os.Getpid()); err != nil {
		return err
	}

	log := applog.NewLogger(p.LogFile(), time.Now)
	defer func() { _ = log.Close() }()
	log.Logf("daemon 起動 (pid %d, pollIntervalSeconds=%d)", os.Getpid(), cfg.PollIntervalSeconds)

	if dryRun {
		client = newDryRunClient(client, log)
		log.Logf("dry-run モード: 検知と判定は通常どおり行うが､pane への送信とラベル更新はしない")
	}

	// 実行ファイルの差し替えに気づくための基準｡取得できなければ更新検知だけを
	// 諦める (path が空の stamp は changed() が常に false を返す)｡
	stamp, stampErr := currentBinaryStamp()
	if stampErr != nil {
		log.Logf("実行ファイルの情報を取得できないため､更新の自動反映を無効にします: %v", stampErr)
	}

	now := time.Now()
	runtime := newDaemonRuntime(client, log, p.StatusFile(), sessionstate.New(p.StateDir()), dryRun, time.Sleep, now)
	runtime.housekeep(p.LogFile(), now)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	loopCtx, stop := context.WithCancel(ctx)
	defer stop()

	cleanup := func(reason string) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		runtime.clearAllLabels(cleanupCtx)
		cancel()
		if err := RemovePIDFile(p.PIDFile()); err != nil {
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
			// 実行ファイルが差し替わっていたら､後始末をしてから新しい版へ
			// 入れ替わる (理由は selfupdate.go を参照)｡
			if stamp.changed() {
				cleanup("実行ファイルが更新されたため入れ替わります")
				if err := spawnDetached(stamp.path, dryRun, p); err != nil {
					log.Logf("新しい版の起動に失敗しました｡次回の SessionStart で起動されます: %v", err)
				}
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
