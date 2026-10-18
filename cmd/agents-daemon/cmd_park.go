package main

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// newParkCmd は `agents-daemon park add|set|list` を組み立てる (internal/parking)。
// domain と repo は --cwd (既定は今の cwd) から決め、結果は JSON で stdout に出す。
func newParkCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{Use: "park", Short: "パーキングロット (脇の件) を置く・書き換える・一覧する"}
	cmd.PersistentFlags().StringVar(&cwd, "cwd", "", "置いた (開いた) セッションの cwd。domain と repo をここから決める (既定は今の cwd)")

	add := &cobra.Command{
		Use:   "add",
		Short: "stdin の JSON (kind、title、detail、files、default_taken、session_id) で件を置き、{created, item} を出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPark(cmd, cwd, func(s parking.Store, p parking.Placer, cwd string) (any, error) {
				req, err := parking.ReadRequest(cmd.InOrStdin())
				if err != nil {
					return nil, err
				}
				return s.Park(req, cwd, p, time.Now())
			})
		},
	}
	var status, session string
	set := &cobra.Command{
		Use:   "set <id>",
		Short: "件の status (open / handed / discarded) を書き換える",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPark(cmd, cwd, func(s parking.Store, p parking.Placer, cwd string) (any, error) {
				return s.Set(p.Domain(session, cwd), args[0], status)
			})
		},
	}
	set.Flags().StringVar(&status, "status", "", "open / handed / discarded")
	_ = set.MarkFlagRequired("status")
	var repo, listStatus, task string
	var here bool
	list := &cobra.Command{
		Use:   "list",
		Short: "cwd の domain の件を JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if task != "" && session == "" {
				return errors.New("--task には --session が要る")
			}
			return withPark(cmd, cwd, func(s parking.Store, p parking.Placer, cwd string) (any, error) {
				if here {
					repo = gitrepo.RepoKey(cwd)
				}
				items, err := s.Find(p.Domain(session, cwd), repo, listStatus)
				if err != nil || task == "" {
					return items, err
				}
				return parking.PlacedDuring(items, session, task), nil
			})
		},
	}
	list.Flags().StringVar(&repo, "repo", "", "repo (main worktree のパス) で絞る")
	list.Flags().BoolVar(&here, "here", false, "--cwd の repo で絞る (mod は repo の鍵を持たないため)")
	list.MarkFlagsMutuallyExclusive("repo", "here")
	list.Flags().StringVar(&listStatus, "status", "", "status で絞る")
	list.Flags().StringVar(&task, "task", "", "--session のセッションがこのタスクの途中で置いた件で絞る")
	adopt := &cobra.Command{
		Use:   "adopt <session_id> <task_id>",
		Short: "終了したセッションの未完了のタスクを kind task のメモにし、タスクの記録を消して {created, item} を出す",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPark(cmd, cwd, func(s parking.Store, p parking.Placer, cwd string) (any, error) {
				return s.Adopt(session, cwd, args[0], args[1], p, time.Now())
			})
		},
	}
	for _, c := range []*cobra.Command{set, list, adopt} {
		c.Flags().StringVar(&session, "session", "", "開いたセッションの ID。記録が業務の側にあれば業務の件を扱う (status --board と同じ)")
	}
	cmd.AddCommand(add, set, list, adopt)
	return cmd
}

// withPark は置き場と置き先の決め方を環境から組んで fn を呼び、結果を JSON で stdout に書く。
func withPark(cmd *cobra.Command, cwd string, fn func(parking.Store, parking.Placer, string) (any, error)) error {
	return withPlacer(cmd, cwd, func(p apppath.Paths, placer parking.Placer, cwd string) (any, error) {
		return fn(parking.New(p.StateDir()), placer, cwd)
	})
}

// withPlacer は domain の決め方 (config とタスクの記録) を環境から組んで fn を呼び、結果を JSON で stdout に書く。
// cwd が空なら今の cwd にする。park と edge が使う。
func withPlacer(cmd *cobra.Command, cwd string, fn func(apppath.Paths, parking.Placer, string) (any, error)) error {
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	// 設定が読めなくても Load は既定値 (すべて業務) を返す。業務のものが私用の側へ出ない向きに倒れる。
	cfg, _ := config.Load(p.ConfigFile())
	placer := parking.Placer{Tasks: tasklog.New(p.StateDir()), Classify: domain.New(os.Getenv("HOME"), cfg.HomePaths, cfg.DefaultDomain).Classify}
	v, err := fn(p, placer, cwd)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}
