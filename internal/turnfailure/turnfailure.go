// Package turnfailure は turn-failures/<session_id>.json (StopFailure hook が受けた、turn を終わらせた API error)
// の書式を読み書き両方で持つ。書くのは `agents-daemon ingest-stop-failure`、読むのは daemon の上限の出来事の記録。
package turnfailure

import (
	"errors"
	"path/filepath"
	"time"
)

const Retention = 24 * time.Hour

type Store struct {
	Dir string
}

func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "turn-failures")}
}

type Record struct {
	SessionID    string    `json:"session_id"`
	Error        string    `json:"error"`
	ErrorDetails string    `json:"error_details,omitempty"`
	Message      string    `json:"last_assistant_message,omitempty"`
	ObservedAt   time.Time `json:"observed_at"`
}

func (s Store) Ingest(input []byte, now time.Time) error {
	return errors.New("未実装")
}

func (s Store) Load(sessionID string) (*Record, error) {
	return nil, errors.New("未実装")
}

func (s Store) Prune(now time.Time) (int, error) {
	return 0, errors.New("未実装")
}
