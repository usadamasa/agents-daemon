package edge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/ref"
)

// Anchor は PR を出したセッション (sessionID、その cwd) から pr への辺を、印 derived を付けて書く (#487)。
// 書くのは、pr がどれかの辺の端点にあり、pr を after に持つ辺が無く、pr がそのセッション (sid: かそのタスク) を
// after に持つ辺 (自分の PR を自分で待つ形。書くと待ちの輪になる) が無く、人がその組を消して「導かない」と
// 記録していない (#576) ときだけ。書いたら ok が true。
// pr の端点の cwd は、pr が出ている辺の端点から写す。手順の性質は models/prlink が持つ。
func (s Store) Anchor(sessionID, cwd, pr string, now time.Time) (Edge, bool, error) {
	before := Endpoint{Ref: ref.SessionID(sessionID), CWD: cwd}
	it, isItem := ref.ParseItem(pr)
	switch {
	case sessionID == "" || !ref.Endpoint(before.Ref):
		return Edge{}, false, fmt.Errorf("セッション %q の参照を組めない", sessionID)
	case !filepath.IsAbs(cwd):
		return Edge{}, false, fmt.Errorf("%s の cwd %q が絶対パスでない", before.Ref, cwd)
	case !isItem || it.Kind != "pr":
		return Edge{}, false, fmt.Errorf("参照 %q は pr:<owner>/<repo>#<番号> でない", pr)
	case s.Dir == "":
		return Edge{}, false, errors.New("edges の置き場所が無い")
	}
	var anchored Edge
	var ok bool
	err := s.locked(func(dir string) error {
		after, found := Endpoint{}, false
		for _, e := range readAll(dir) {
			switch {
			case e.After.Ref == pr, e.Before.Ref == pr && ownedBy(e.After.Ref, sessionID):
				return nil
			case !found && e.Before.Ref == pr:
				after, found = e.Before, true
			}
		}
		if !found {
			return nil
		}
		if skip, err := declined(dir, before.Ref, pr, now); skip || err != nil {
			return err
		}
		before.Repo = gitrepo.RepoKey(cwd)
		anchored = Edge{
			ID:        edgeID(before.Ref, pr),
			Before:    before,
			After:     Endpoint{Ref: pr, CWD: after.CWD, Repo: after.Repo},
			Note:      "PR #" + strconv.Itoa(it.Number),
			Derived:   true,
			CreatedAt: now,
		}
		ok = true
		return write(dir, anchored)
	})
	if err != nil {
		return Edge{}, false, err
	}
	return anchored, ok, nil
}

// Unanchor は導いた辺 (sid:S → pr:P) のうち、P が S を after に持つ辺 (S が自分の PR を待つ形) と輪になるものを
// 消し、消した辺を返す。人が書いた辺は消さない。
func (s Store) Unanchor() ([]Edge, error) {
	if s.Dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.Dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var removed []Edge
	err := s.locked(func(dir string) error {
		edges := readAll(dir)
		waits := map[[2]string]bool{}
		for _, e := range edges {
			waits[[2]string{e.Before.Ref, owner(e.After.Ref)}] = true
		}
		var errs []error
		for _, e := range edges {
			sessionID, isSID := ref.ParseSessionID(e.Before.Ref)
			if !e.Derived || !isSID || !waits[[2]string{e.After.Ref, sessionID}] {
				continue
			}
			if err := os.Remove(filepath.Join(dir, e.ID+".json")); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			removed = append(removed, e)
		}
		return errors.Join(errs...)
	})
	return removed, err
}

// owner は参照がセッション (sid:S) かそのタスク (task:S/x) なら S を返す。ほかは空。
func owner(r string) string {
	if id, ok := ref.ParseSessionID(r); ok {
		return id
	}
	if id, _, ok := ref.ParseTask(r); ok {
		return id
	}
	return ""
}

// ownedBy は参照がセッション sessionID かそのタスクか。
func ownedBy(r, sessionID string) bool {
	return owner(r) == sessionID
}
