// Package quotaresume は quota-resume/<session_id>.json (Notification hook が受けた、Claude Code 自身の上限待ちの
// 最後の結末 quota_auto_resume_*) の書式を読み書き両方で持つ。書くのは `agents-daemon ingest-quota-resume`、読むのは daemon。
//
// 待機明けに Enter だけを送るかは画面の通知欄で選ぶ (detect.ClassifyScreen)。通知欄は走査範囲の外へ出たり描き方が
// 変わったりして読めないことがあるので、stale の通知を Enter の補助の根拠にする (#529)。hook が走らないセッションでは
// 記録が無く、選択は画面だけで成り立つ。
package quotaresume

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// notification_type の値。
// cc-premise: 2.1.330 hooks Notification の quota_auto_resume_fired / _stale / _disabled (2.1.234 以降。docs の matcher の表)
const (
	// TypeFired は本体が上限の解除で作業を続けた。
	TypeFired = "quota_auto_resume_fired"
	// TypeStale は解除がスリープ中に過ぎ、本体が続けずに Enter を待っている。
	TypeStale = "quota_auto_resume_stale"
	// TypeDisabled は本体が続けずに待機を終えた (設定の off、解除が 24 時間より先へ移った、続けても上限に当たり続けた)。
	TypeDisabled = "quota_auto_resume_disabled"
)

var knownTypes = []string{TypeFired, TypeStale, TypeDisabled}

// Retention は記録を残す期間。読むのは pane が上限で待っている間で、StaleFor が今の上限の entry より古い記録を
// 使わないので、これが消すのは閉じたセッションの残りだけ。turnfailure.Retention と揃える。
const Retention = 24 * time.Hour

// Store は quota-resume/ の置き場所を持つ。ゼロ値は書かず、読みは「無い」、消さない。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "quota-resume")}
}

// Record はセッションの最後の quota_auto_resume_* の通知。欄の名前は hook の入力に揃える。
type Record struct {
	SessionID        string `json:"session_id"`
	NotificationType string `json:"notification_type"`
	// Message は通知の文。判断には使わず、出来事の記録で読むためだけに残す (docs に文面の例が無い)。
	Message    string    `json:"message,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

// hookInput は Notification hook の stdin のうち、記録に要る部分。
// cc-premise: 2.1.330 hooks Notification の入力の欄 (message、title、notification_type)。解除時刻の欄は無い
type hookInput struct {
	SessionID        string `json:"session_id"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
}

// Ingest は Notification hook の入力からセッションの記録を上書きする。3 種のどれも最後の 1 件だけを残し、stale の後に
// fired か disabled が来たら Enter の根拠は消える。ほかの通知と event は hooks.json の matcher が絞る前提なので error に
// する。session_id が無ければ何もしない。
func (s Store) Ingest(input []byte, now time.Time) error {
	var in hookInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("hook の入力のパースに失敗: %w", err)
	}
	if in.HookEventName != "Notification" || !slices.Contains(knownTypes, in.NotificationType) {
		return fmt.Errorf("扱わない hook の入力 (%q、%q)", in.HookEventName, in.NotificationType)
	}
	path, err := s.file(in.SessionID)
	if err != nil || path == "" {
		return err
	}
	return statefile.WriteJSONAtomic(path, ".quota-resume.*", Record{
		SessionID:        in.SessionID,
		NotificationType: in.NotificationType,
		Message:          in.Message,
		ObservedAt:       now,
	})
}

// Load は sessionID の記録を読む。sessionID が空か、記録が無ければ (nil, nil)。
func (s Store) Load(sessionID string) (*Record, error) {
	path, err := s.file(sessionID)
	if err != nil || path == "" {
		return nil, err
	}
	var rec Record
	ok, err := statefile.ReadJSON(path, &rec)
	if err != nil || !ok {
		return nil, err
	}
	return &rec, nil
}

// Prune は mtime が Retention より古い記録を消し、消した数を返す。ディレクトリが無ければ何もしない。
func (s Store) Prune(now time.Time) (int, error) {
	return statefile.PruneDir(s.Dir, now.Add(-Retention))
}

// StaleFor は、記録が entry (今の上限の entry) の後に届いた stale の通知かを返す。前の上限の待機で届いた stale は
// 今の待機の根拠にしない。nil の記録と entry、時刻の読めない entry は false。
func (r *Record) StaleFor(entry *transcript.Entry) bool {
	if r == nil || entry == nil || entry.Timestamp.IsZero() {
		return false
	}
	return r.NotificationType == TypeStale && !r.ObservedAt.Before(entry.Timestamp)
}

// file は sessionID の記録のパス。ゼロ値の Store と空の sessionID は ("", nil) で、呼び出し元は何もしない。
func (s Store) file(sessionID string) (string, error) {
	if s.Dir == "" {
		return "", nil
	}
	return statefile.SessionPath(s.Dir, sessionID, ".json")
}
