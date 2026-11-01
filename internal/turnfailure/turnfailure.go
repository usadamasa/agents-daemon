// Package turnfailure は turn-failures/<session_id>.json (StopFailure hook が受けた、turn を終わらせた API error)
// の書式を読み書き両方で持つ。書くのは `agents-daemon ingest-stop-failure`、読むのは daemon の上限の出来事の記録。
//
// 判定にはまだ使わない。上限で止まった pane の OSC 7501 の record と並べて残し、上限検知の前段のゲートに
// どちらが使えるかを比べる材料にする (#77)。
package turnfailure

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention は記録を残す期間。読むのは pane が上限で止まっている間だけで、5 時間ウィンドウの解除を待つ
// 間に消えなければ足りる。ratelimit.Retention と揃える。
const Retention = 24 * time.Hour

// Store は turn-failures/ の置き場所を持つ。ゼロ値は書かず、読みは「無い」、消さない。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "turn-failures")}
}

// Record はセッションの最後の StopFailure。欄の名前は hook の入力に揃える。
type Record struct {
	SessionID string `json:"session_id"`
	// Error は Claude Code が API error を分類した語 (rate_limit、overloaded、server_error など)。
	Error        string `json:"error"`
	ErrorDetails string `json:"error_details,omitempty"`
	// Message は会話に描いたエラーの文 (`API Error: …`、上限なら `You've hit your …`)。
	Message    string    `json:"last_assistant_message,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

// hookInput は StopFailure hook の stdin のうち、記録に要る部分。
// cc-premise: 2.1.316 hooks StopFailure の入力の欄 (error、error_details、last_assistant_message)
type hookInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Error         string `json:"error"`
	ErrorDetails  string `json:"error_details"`
	Message       string `json:"last_assistant_message"`
}

// unknownError は error が無い入力に付ける語。Claude Code 自身も分類できない error をこの語で渡す。
const unknownError = "unknown"

// Ingest は StopFailure hook の入力から、そのセッションの記録を上書きする。session_id が無ければ書かない。
func (s Store) Ingest(input []byte, now time.Time) error {
	var in hookInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("StopFailure の入力のパースに失敗: %w", err)
	}
	if in.HookEventName != "StopFailure" {
		return fmt.Errorf("StopFailure 以外の hook の入力 (%q)", in.HookEventName)
	}
	path, err := s.file(in.SessionID)
	if err != nil || path == "" {
		return err
	}
	if in.Error == "" {
		in.Error = unknownError
	}
	return statefile.WriteJSONAtomic(path, ".turn-failures.*", Record{
		SessionID:    in.SessionID,
		Error:        in.Error,
		ErrorDetails: in.ErrorDetails,
		Message:      in.Message,
		ObservedAt:   now,
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

// file は sessionID の記録のパス。ゼロ値の Store と空の sessionID は ("", nil) で、呼び出し元は何もしない。
func (s Store) file(sessionID string) (string, error) {
	if s.Dir == "" {
		return "", nil
	}
	return statefile.SessionPath(s.Dir, sessionID, ".json")
}
