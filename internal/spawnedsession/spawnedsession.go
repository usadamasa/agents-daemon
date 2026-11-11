// Package spawnedsession は spawn と peer-sessions skill が起こした多重化ツールの session の印を XDG state 配下に
// 置く (#557)。書くのは起こした session の中の包みの sh (`agents-daemon spawned-session mark`) で、daemon の reaper が
// 印を回り、block が無くなった session を閉じて印を消す。多重化ツール (rex) の session には印を付ける欄が無く、手で
// 作った空の session と見分けるのに使う。
//
//	spawned-sessions/<id>.json   印 1 つ (多重化ツールの session ID、書いた時刻)
//
// id は session ID から決める (rex の session ID は : を含む)。印は古さで消さない。daemon が回らないまま日が経っても
// 生きている session の印を残すためで、消すのは reaper が session を閉じたときか、session がもう無いと分かったとき
// だけ。印は生きている起こした session の数しか溜まらない。
package spawnedsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Record は印 1 つ。
type Record struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Store は spawned-sessions/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "spawned-sessions")}
}

// Put は session の印を書く。同じ session の印は書き直す。
func (s Store) Put(sessionID string) error {
	if s.Dir == "" {
		return errors.New("spawned-sessions の置き場所が無い")
	}
	if sessionID == "" {
		return errors.New("session ID が空なので印を書かない")
	}
	return statefile.WriteJSONAtomic(s.path(sessionID), ".spawned.*", Record{SessionID: sessionID, CreatedAt: time.Now()})
}

// Get は session の印を返す。無いか読めなければ false。
func (s Store) Get(sessionID string) (Record, bool) {
	if s.Dir == "" {
		return Record{}, false
	}
	return read(s.path(sessionID))
}

// List は印を返す。置き場が無ければ空。壊れたファイルと書きかけの一時ファイルは読み飛ばす。
func (s Store) List() ([]Record, error) {
	if s.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name[0] == '.' || filepath.Ext(name) != ".json" {
			continue
		}
		if rec, ok := read(filepath.Join(s.Dir, name)); ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// Remove は印を消す。無ければ何もしない。
func (s Store) Remove(sessionID string) error {
	if err := os.Remove(s.path(sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s Store) path(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(s.Dir, hex.EncodeToString(sum[:8])+".json")
}

func read(path string) (Record, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- spawned-sessions/ 配下の走査で得たパス
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil || rec.SessionID == "" {
		return Record{}, false
	}
	return rec, true
}
