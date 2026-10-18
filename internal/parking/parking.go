package parking

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
)

const (
	KindAside      = "aside"
	KindScope      = "scope"
	KindPreference = "preference"

	StatusOpen      = "open"
	StatusHanded    = "handed"
	StatusDiscarded = "discarded"
)

type Entry struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Detail       string   `json:"detail,omitempty"`
	Files        []string `json:"files,omitempty"`
	DefaultTaken string   `json:"default_taken,omitempty"`
}

type Source struct {
	SessionID string    `json:"session_id"`
	TaskID    string    `json:"task_id,omitempty"`
	CWD       string    `json:"cwd"`
	At        time.Time `json:"at"`
}

type Item struct {
	ID     string        `json:"id"`
	Domain domain.Domain `json:"domain"`
	Repo   string        `json:"repo"`
	Entry
	Sources []Source `json:"sources"`
	Status  string   `json:"status"`
}

type Store struct {
	Dir string
}

func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "parking")}
}

func (s Store) Add(d domain.Domain, repo string, e Entry, src Source) (Item, error) {
	return Item{}, errors.New("未実装")
}

func (s Store) Set(d domain.Domain, id, status string) (Item, error) {
	return Item{}, errors.New("未実装")
}

func (s Store) List(d domain.Domain) ([]Item, error) {
	return nil, errors.New("未実装")
}

func (s Store) Prune(now time.Time) (int, error) {
	return 0, errors.New("未実装")
}
