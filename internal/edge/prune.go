package edge

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ref"
)

// TouchSessions は sessionIDs のセッションを端点 (sid: か task:) に持つ辺の mtime を now に進める。
func (s Store) TouchSessions(sessionIDs []string, now time.Time) error {
	return s.touch(func(e Edge) bool {
		return onSession(e.Before.Ref, sessionIDs) || onSession(e.After.Ref, sessionIDs)
	}, now)
}

// TouchBefore は before の参照が refs にある辺の mtime を now に進める。
func (s Store) TouchBefore(refs map[string]bool, now time.Time) error {
	return s.touch(func(e Edge) bool { return refs[e.Before.Ref] }, now)
}

// onSession は端点の参照が sessionIDs のセッションのものか。
func onSession(r string, sessionIDs []string) bool {
	id, ok := ref.ParseSessionID(r)
	if !ok {
		id, _, ok = ref.ParseTask(r)
	}
	return ok && slices.Contains(sessionIDs, id)
}

// touch は keep が true を返す辺の mtime を now に進める。Prune と同じロックを取り、edges/ が無ければ作らない。
func (s Store) touch(keep func(Edge) bool, now time.Time) error {
	if s.Dir == "" {
		return nil
	}
	if _, err := os.Stat(s.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return s.locked(func(dir string) error {
		var errs []error
		for _, e := range readAll(dir) {
			if !keep(e) {
				continue
			}
			if err := os.Chtimes(filepath.Join(dir, e.ID+".json"), now, now); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
}

// Prune は Retention のあいだ更新の無いファイル (辺、「導かない」の記録、消し損ねた一時ファイル) を mtime で消し、
// 消した数を返す。
// ロックのファイルは残す (消すと、開いたままの別のプロセスと別のロックを取り合うことになる)。Add と同じロックを
// 取り、Add が読んでから書くまでの間には消さない。
func (s Store) Prune(now time.Time) (int, error) {
	if s.Dir == "" {
		return 0, nil
	}
	if _, err := os.Stat(s.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	err := s.locked(func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var errs []error
		for _, e := range entries {
			if e.IsDir() || e.Name() == lockFile {
				continue
			}
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) < Retention {
				continue
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			removed++
		}
		return errors.Join(errs...)
	})
	return removed, err
}
