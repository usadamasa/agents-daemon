// Package doctor は `doctor` サブコマンド (daemon が動作するための前提条件の診断) の実装を提供する。
// 多重化ツールへの到達性、pane の見え方、rex CLI の前提、statusline の state、設定、domain、daemon の稼働、
// peer messaging、解決済みパスを Report に集めてから書く。文字の出力 (write) と `--json` の出力が同じ判定を
// 見るためで、JSON の形は Report の json tag が決める。他の読むだけの診断 (inspect / status / logs) は internal/verify。
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/daemon/state"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
)

// Input は Run が診断に使う環境。
type Input struct {
	Client multiplexer.Client
	Kind   string // Client の多重化ツール (backend.Detect の結果)
	Paths  apppath.Paths
	Now    time.Time
	Getenv func(string) string // peer messaging と rex CLI の前提 (REX_BLOCK / REX_SESSION) に使う環境 (通常は os.Getenv)
	// Premises は rex CLI の前提の点検。Client がその口を持たなければ nil。
	Premises multiplexer.PremiseChecker
}

// Report は doctor の診断の結果。backend に到達できなければ Backend だけが埋まる。
type Report struct {
	Backend       BackendReport        `json:"backend"`
	Panes         *PanesReport         `json:"panes,omitempty"`
	Premises      *PremisesReport      `json:"premises,omitempty"`
	RateLimits    *RateLimitsReport    `json:"rate_limits,omitempty"`
	Config        *ConfigReport        `json:"config,omitempty"`
	Domain        *DomainReport        `json:"domain,omitempty"`
	Daemon        *DaemonReport        `json:"daemon,omitempty"`
	PeerMessaging *PeerMessagingReport `json:"peer_messaging,omitempty"`
	Paths         *PathsReport         `json:"paths,omitempty"`
}

// BackendReport は多重化ツールの server への到達性。
type BackendReport struct {
	Kind      string `json:"kind"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// PanesReport は pane 一覧のうち Claude agent と判定されたもの。Total は Claude 以外も数えた pane の総数。
type PanesReport struct {
	Claude []PaneReport `json:"claude"`
	Total  int          `json:"total"`
	// Error は server には到達できるのに一覧を取れなかったとき (診断として機能しない状態)。
	Error string `json:"error,omitempty"`
}

// PaneReport は Claude agent の pane 1 つ。
type PaneReport struct {
	PaneID       string                  `json:"pane_id"`
	CWD          string                  `json:"cwd"`
	AgentStatus  multiplexer.AgentStatus `json:"agent_status"`
	AgentKind    string                  `json:"agent_kind,omitempty"`
	AgentMessage string                  `json:"agent_message,omitempty"`
	SessionID    string                  `json:"session_id,omitempty"`
	// UnknownReason は record があるのに使えず agent_status が unknown になった理由。
	UnknownReason string `json:"unknown_reason,omitempty"`
	// Geometry は block の高さと screen (size と modes)。取れなければ GeometryError に理由。
	Geometry      *multiplexer.Geometry `json:"geometry,omitempty"`
	GeometryError string                `json:"geometry_error,omitempty"`
}

// tooShort は block の高さが上限の画面を検知できる下限に満たないかを返す。
func (p PaneReport) tooShort() bool {
	return p.Geometry != nil && p.Geometry.Rows < detect.MinDetectableRows
}

// PremisesReport は rex CLI の前提の点検。点検しなかったときは Skipped に理由が入り Findings は空。
type PremisesReport struct {
	Skipped  string                       `json:"skipped,omitempty"`
	Findings []multiplexer.PremiseFinding `json:"findings"`
}

// RateLimitsReport は Claude agent の pane ごとの利用上限 state (statusline が書く rate-limits/)。
type RateLimitsReport struct {
	// ConfigError は設定を読めず既定の鮮度閾値で判定したときの理由。
	ConfigError string                   `json:"config_error,omitempty"`
	Sessions    []SessionRateLimitReport `json:"sessions"`
}

// SessionRateLimitReport は 1 pane (1 セッション) ぶんの state。State が nil ならファイルがまだ無い。
type SessionRateLimitReport struct {
	PaneID    string                `json:"pane_id"`
	SessionID string                `json:"session_id,omitempty"`
	Error     string                `json:"error,omitempty"`
	State     *RateLimitStateReport `json:"state"`
}

// RateLimitStateReport は state ファイルの中身と、鮮度・閾値の判定。
type RateLimitStateReport struct {
	ObservedAt       time.Time `json:"observed_at"`
	AgeSeconds       int64     `json:"age_seconds"`
	Fresh            bool      `json:"fresh"`
	MaxAgeSeconds    int       `json:"max_age_seconds"`
	FiveHourUsed     float64   `json:"five_hour_used_percentage"`
	Threshold        float64   `json:"used_percentage_threshold"`
	Exhausted        bool      `json:"exhausted"`
	FiveHourResetsAt time.Time `json:"five_hour_resets_at"`
	// SessionIDMismatch はファイル名と中身の session_id が食い違うときの中身の値。
	SessionIDMismatch string `json:"session_id_mismatch,omitempty"`
}

// ConfigReport は設定ファイル (config.json) の有無と実効値。Fields はファイルがあるときだけ埋まる。
type ConfigReport struct {
	Path      string              `json:"path"`
	Exists    bool                `json:"exists"`
	ReadError string              `json:"read_error,omitempty"`
	ValidJSON bool                `json:"valid_json"`
	LoadError string              `json:"load_error,omitempty"`
	Fields    []ConfigFieldReport `json:"fields,omitempty"`
}

// ConfigFieldReport は 1 設定項目ぶんの実効値。
type ConfigFieldReport struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
	// Differs はデフォルトと異なる値 (ファイルの値が反映されている可能性が高い)。
	Differs bool `json:"differs_from_default"`
}

// DomainReport は私用と業務の境界の判定。
type DomainReport struct {
	ConfigError string        `json:"config_error,omitempty"`
	Fallback    domain.Domain `json:"fallback"`
	HomePaths   []string      `json:"home_paths"`
	// Dropped は homePaths のうち絶対パスでも ~/ 始まりでもないため無視した件数。
	Dropped int                `json:"dropped_home_paths"`
	Panes   []PaneDomainReport `json:"panes"`
}

// PaneDomainReport は Claude agent の pane の cwd の判定。
type PaneDomainReport struct {
	PaneID string        `json:"pane_id"`
	Domain domain.Domain `json:"domain"`
	CWD    string        `json:"cwd"`
}

// DaemonReport は PID ファイルと status.json から見た daemon の稼働状況。
type DaemonReport struct {
	Running       bool                  `json:"running"`
	PID           int                   `json:"pid,omitempty"`
	PIDError      string                `json:"pid_error,omitempty"`
	SnapshotError string                `json:"snapshot_error,omitempty"`
	Snapshot      *DaemonSnapshotReport `json:"snapshot"`
}

// DaemonSnapshotReport は status.json の要約。
type DaemonSnapshotReport struct {
	UpdatedAt      time.Time `json:"updated_at"`
	AgeSeconds     int64     `json:"age_seconds"`
	MonitoredPanes int       `json:"monitored_panes"`
}

// PeerMessagingReport は Claude Code の cross-session messaging の socket。
type PeerMessagingReport struct {
	Socket string `json:"socket,omitempty"`
	Exists bool   `json:"exists"`
	Error  string `json:"error,omitempty"`
	// NestedTmpDir は socket がその下にある CLAUDE_CODE_TMPDIR (入れ子で起動したセッション)。
	NestedTmpDir string `json:"nested_tmpdir,omitempty"`
}

// PathsReport は daemon / CLI が触るランタイムファイルの解決済み絶対パス。
type PathsReport struct {
	Config     string `json:"config"`
	StateDir   string `json:"state_dir"`
	RateLimits string `json:"rate_limits"`
	PIDFile    string `json:"pid_file"`
	StatusFile string `json:"status_file"`
	LogFile    string `json:"log_file"`
}

// Run は各前提条件を順に確認し w へ報告する。
// 多重化ツールの server に到達できない場合だけ error を返す (呼び出し元の
// main が exit 1 にする)。それ以外の欠落や陳腐化は情報として報告するに留め、error にはしない。
func Run(ctx context.Context, w io.Writer, in Input) error {
	rep, err := Collect(ctx, in)
	write(w, rep)
	return err
}

// Collect は診断を集める。error のときも、そこまでに集めた分を返す (JSON でも途中まで出す)。
func Collect(ctx context.Context, in Input) (Report, error) {
	rep := Report{Backend: BackendReport{Kind: in.Kind}}
	if err := in.Client.Reachable(ctx); err != nil {
		rep.Backend.Error = err.Error()
		return rep, fmt.Errorf("%s に到達できないため、これ以降の診断は行えません: %w", in.Kind, err)
	}
	rep.Backend.Reachable = true

	panes, err := in.Client.PaneList(ctx)
	if err != nil {
		// server には到達できているのにここで失敗するのは異常。診断ツールとして
		// 機能しない状態なので broken 扱いにする。
		rep.Panes = &PanesReport{Error: err.Error()}
		return rep, fmt.Errorf("%s の pane 一覧の取得に失敗しました (%s には到達できるのに異常): %w", in.Kind, in.Kind, err)
	}
	rep.Panes = collectPanes(ctx, in.Client, panes)
	rep.Premises = collectPremises(ctx, in.Premises, in.Getenv)
	rep.RateLimits = collectRateLimitStates(in.Paths, panes, in.Now)
	rep.Config = collectConfigState(in.Paths)
	rep.Domain = collectDomain(in.Paths, panes, in.Getenv("HOME"))
	rep.Daemon = collectDaemonState(in.Paths, in.Now)
	rep.PeerMessaging = collectPeerMessaging(in.Getenv)
	rep.Paths = collectPaths(in.Paths)
	return rep, nil
}

// collectPanes は pane 一覧のうち Claude agent と判定されたものを写し、その block の高さと screen を添える
// (小さく分割した block で検知が黙って効かなくなるのを、上限を踏む前に見るため)。
func collectPanes(ctx context.Context, client multiplexer.Client, panes []multiplexer.Pane) *PanesReport {
	rep := &PanesReport{Claude: []PaneReport{}, Total: len(panes)}
	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		p := PaneReport{
			PaneID: pane.PaneID, CWD: pane.CWD, AgentStatus: pane.AgentStatus, AgentKind: pane.AgentKind,
			AgentMessage: pane.AgentMessage, SessionID: pane.SessionID, UnknownReason: pane.UnknownReason,
		}
		if geom, err := client.PaneGeometry(ctx, pane.PaneID); err != nil {
			p.GeometryError = err.Error()
		} else {
			p.Geometry = &geom
		}
		rep.Claude = append(rep.Claude, p)
	}
	return rep
}

// recordLabel は record の自己申告の 1 行 (daemon のログや status と同じ形)。
func (p PaneReport) recordLabel() string {
	return multiplexer.RecordLabel(multiplexer.Pane{AgentStatus: p.AgentStatus, AgentKind: p.AgentKind, AgentMessage: p.AgentMessage})
}

// collectPremises は rex CLI の応答の形が internal/rexcli の読む形のままかを、自分の block (REX_BLOCK) に
// 固定のコマンドを打って集める。ずれは OK=false の finding で、doctor の exit code には響かない
// (rex を上げた直後に全 pane が unknown になる原因を、changelog を読む前に名指しで見るため)。
func collectPremises(ctx context.Context, checker multiplexer.PremiseChecker, getenv func(string) string) *PremisesReport {
	rep := &PremisesReport{Findings: []multiplexer.PremiseFinding{}}
	if checker == nil {
		rep.Skipped = "この backend には点検の口が無いため省きます"
		return rep
	}
	blockID := getenv("REX_BLOCK")
	if blockID == "" {
		rep.Skipped = "REX_BLOCK が無いため点検しません (rex の block の中の Bash ツールから呼ぶと自分の block で点検します)"
		return rep
	}
	rep.Findings = append(rep.Findings, checker.CheckPremises(ctx, blockID, getenv("REX_SESSION"))...)
	return rep
}

// collectRateLimitStates は ingest-statusline が書き出す rate-limits.json の有無、鮮度、
// 5 時間ウィンドウの使用率を集める。ファイルが無いこと自体は異常ではない
// (statusline から ingest-statusline がまだ 1 度も rate_limits を受け取っていない)。
func collectRateLimitStates(p apppath.Paths, panes []multiplexer.Pane, now time.Time) *RateLimitsReport {
	rep := &RateLimitsReport{Sessions: []SessionRateLimitReport{}}
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		rep.ConfigError = cfgErr.Error()
		cfg = config.Default()
	}
	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		rep.Sessions = append(rep.Sessions, collectSessionRateLimitState(cfg, p, pane, now))
	}
	return rep
}

// collectSessionRateLimitState は 1 セッションぶんの state を集める。
func collectSessionRateLimitState(cfg config.Config, p apppath.Paths, pane multiplexer.Pane, now time.Time) SessionRateLimitReport {
	rep := SessionRateLimitReport{PaneID: pane.PaneID, SessionID: pane.SessionID}
	state, err := ratelimit.New(p.StateDir()).Load(pane.SessionID)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	if state == nil {
		return rep
	}
	maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
	rep.State = &RateLimitStateReport{
		ObservedAt:       state.ObservedAt,
		AgeSeconds:       roundSeconds(now.Sub(state.ObservedAt)),
		Fresh:            state.IsFresh(now, maxAge),
		MaxAgeSeconds:    cfg.StateMaxAgeSeconds,
		FiveHourUsed:     state.FiveHourUsed,
		Threshold:        cfg.UsedPercentageThreshold,
		Exhausted:        state.FiveHourExhausted(cfg.UsedPercentageThreshold),
		FiveHourResetsAt: state.FiveHourResetsAt,
	}
	// ファイル名と中身の session_id は必ず一致するはずなので、一致しているときは
	// 出さない。食い違っていたら state の対応付けが壊れているということなので、
	// そのときだけ報告する。
	if state.SessionID != "" && state.SessionID != pane.SessionID {
		rep.State.SessionIDMismatch = state.SessionID
	}
	return rep
}

// roundSeconds は経過時間を秒に丸める (表示の「n 前」と JSON の age_seconds を同じ値にする)。
func roundSeconds(d time.Duration) int64 {
	return int64(d.Round(time.Second) / time.Second)
}

// collectConfigState は設定ファイル (config.json) の有無と実効値を集める。config.Load は
// 個々のキーが不正でも黙ってデフォルトへ落とすため (起動を止めない方針)、ここでは
// 実効値がデフォルトと一致するかどうかをヒントとして添える。一致していても偶然
// 同値のことがあり、逆に不一致でも意図した値のことがあるので、あくまで目安。
func collectConfigState(p apppath.Paths) *ConfigReport {
	rep := &ConfigReport{Path: p.ConfigFile()}
	data, statErr := os.ReadFile(rep.Path) // #nosec G304 -- 固定パス
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			rep.ReadError = statErr.Error()
		}
		return rep
	}
	rep.Exists = true
	rep.ValidJSON = json.Valid(data)
	cfg, err := config.Load(rep.Path)
	if err != nil {
		rep.LoadError = err.Error()
		cfg = config.Default()
	}
	rep.Fields = configFields(cfg, config.Default())
	return rep
}

// configFields は cfg の主要フィールドを名前付きで列挙し、def との差分を添える。
func configFields(cfg, def config.Config) []ConfigFieldReport {
	raw := []struct {
		name     string
		value    any
		defValue any
	}{
		{"enabled", cfg.Enabled, def.Enabled},
		{"pollIntervalSeconds", cfg.PollIntervalSeconds, def.PollIntervalSeconds},
		{"marginSeconds", cfg.MarginSeconds, def.MarginSeconds},
		{"fallbackWaitHours", cfg.FallbackWaitHours, def.FallbackWaitHours},
		{"maxRetries", cfg.MaxRetries, def.MaxRetries},
		{"retryMessage", cfg.RetryMessage, def.RetryMessage},
		{"readLines", cfg.ReadLines, def.ReadLines},
		{"detectionTailLines", cfg.DetectionTailLines, def.DetectionTailLines},
		{"usedPercentageThreshold", cfg.UsedPercentageThreshold, def.UsedPercentageThreshold},
		{"stateMaxAgeSeconds", cfg.StateMaxAgeSeconds, def.StateMaxAgeSeconds},
		{"handleTransient", cfg.HandleTransient, def.HandleTransient},
		{"transientWaitSeconds", cfg.TransientWaitSeconds, def.TransientWaitSeconds},
		{"transientMaxWaitSeconds", cfg.TransientMaxWaitSeconds, def.TransientMaxWaitSeconds},
		{"dismissMenu", cfg.DismissMenu, def.DismissMenu},
		{"menuDismissDelayMs", cfg.MenuDismissDelayMs, def.MenuDismissDelayMs},
		{"submitDelayMs", cfg.SubmitDelayMs, def.SubmitDelayMs},
		{"customPatterns", cfg.CustomPatterns, def.CustomPatterns},
		{"idleShutdownMinutes", cfg.IdleShutdownMinutes, def.IdleShutdownMinutes},
		{"deferToNativeAutoContinue", cfg.DeferToNativeAutoContinue, def.DeferToNativeAutoContinue},
		{"homePaths", cfg.HomePaths, def.HomePaths},
		{"defaultDomain", cfg.DefaultDomain, def.DefaultDomain},
	}
	out := make([]ConfigFieldReport, 0, len(raw))
	for _, r := range raw {
		out = append(out, ConfigFieldReport{
			Name: r.name, Value: r.value, Differs: fmt.Sprintf("%v", r.value) != fmt.Sprintf("%v", r.defValue),
		})
	}
	return out
}

// collectDomain は config の homePaths と defaultDomain から、Claude agent の pane の cwd を
// 私用と業務のどちらに判定するかを集める。判定を誤ると業務のものが私用の側に混ざるので、
// 設定を書いたら pane ごとの結果をここで確かめる。
func collectDomain(p apppath.Paths, panes []multiplexer.Pane, home string) *DomainReport {
	rep := &DomainReport{Panes: []PaneDomainReport{}}
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		rep.ConfigError = err.Error()
	}
	c := domain.New(home, cfg.HomePaths, cfg.DefaultDomain)
	rep.Fallback = c.Fallback()
	rep.HomePaths = c.HomePaths()
	if rep.HomePaths == nil {
		rep.HomePaths = []string{}
	}
	rep.Dropped = len(cfg.HomePaths) - len(c.HomePaths())
	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		rep.Panes = append(rep.Panes, PaneDomainReport{PaneID: pane.PaneID, Domain: c.Classify(pane.CWD), CWD: pane.CWD})
	}
	return rep
}

// collectDaemonState は PID ファイルと status.json から daemon の稼働状況を集める。
func collectDaemonState(p apppath.Paths, now time.Time) *DaemonReport {
	rep := &DaemonReport{}
	pid, err := state.ReadPIDFile(p.PIDFile())
	if err != nil {
		rep.PIDError = err.Error()
		return rep
	}
	if pid == 0 || !state.PIDAlive(pid) {
		return rep
	}
	rep.Running, rep.PID = true, pid
	snap, err := state.ReadStatusSnapshot(p.StatusFile())
	if err != nil {
		rep.SnapshotError = err.Error()
		return rep
	}
	if snap == nil {
		return rep
	}
	rep.Snapshot = &DaemonSnapshotReport{
		UpdatedAt: snap.UpdatedAt, AgeSeconds: roundSeconds(now.Sub(snap.UpdatedAt)), MonitoredPanes: len(snap.Panes),
	}
	return rep
}

// collectPeerMessaging は Claude Code の cross-session messaging がこのプロセスの環境で
// 使える状態かを集める。Claude Code は各セッションで CLAUDE_CODE_MESSAGING_SOCKET を
// 子プロセス (Bash ツール) へ渡すので、doctor をセッション内から呼んだときだけ判定できる。
// 設定側の切り分け (gate を止める env、crossSessionInbound、deny) は setup skill の
// references/peer-messaging.md が持ち、ここは観測できる事実だけを出す。
func collectPeerMessaging(getenv func(string) string) *PeerMessagingReport {
	rep := &PeerMessagingReport{Socket: getenv("CLAUDE_CODE_MESSAGING_SOCKET")}
	if rep.Socket == "" {
		return rep
	}
	if _, err := os.Stat(rep.Socket); err != nil {
		rep.Error = err.Error()
	} else {
		rep.Exists = true
	}
	// sandbox 化した子プロセスとして起動した Claude Code は CLAUDE_CODE_TMPDIR を受け取り、
	// socket 先が /tmp/claude-<uid>/cc-socks/ へ移って seatbelt に bind を拒否される。
	// 変数そのものは親セッションの Bash ツールにも来る (sandbox が渡す) ので、変数の有無ではなく
	// socket のパスがその下に居るかで入れ子を見分ける。
	if tmpdir := getenv("CLAUDE_CODE_TMPDIR"); tmpdir != "" && isUnder(rep.Socket, tmpdir) {
		rep.NestedTmpDir = tmpdir
	}
	return rep
}

// isUnder は path が dir 配下にあるかをパスの区切りで判定する (接頭辞の文字列一致では
// /tmp/claude-501 と /tmp/claude-5010 を区別できない)。
func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// collectPaths は daemon / CLI が触るランタイムファイルの解決済み絶対パスを集める。
func collectPaths(p apppath.Paths) *PathsReport {
	return &PathsReport{
		Config:     p.ConfigFile(),
		StateDir:   p.StateDir(),
		RateLimits: ratelimit.New(p.StateDir()).Dir + "/<session_id>.json",
		PIDFile:    p.PIDFile(),
		StatusFile: p.StatusFile(),
		LogFile:    p.LogFile(),
	}
}
