// Package boardcli はボードに載る記録 (タスク、パーキングロット、セッションをまたぐ辺) を書く・読むコマンド
// (park、edge、ingest-task) と、ボードの経路のログに mod の出来事を書く board-log の組み立てを持つ。
package boardcli

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newIngestTaskCmd(), newParkCmd(), newBoardLogCmd(), newEdgeCmd()}
}

// newParkCmd は `agents-daemon park add|set|list` を組み立てる (internal/parking)。
// domain と repo は --cwd (既定は今の cwd) から決め、結果は JSON で stdout に出す。
func newParkCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{Use: "park", Short: "パーキングロット (脇のメモ) を置く・書き換える・一覧する"}
	cmd.PersistentFlags().StringVar(&cwd, "cwd", "", "置いた (開いた) セッションの cwd。domain と repo をここから決める (既定は今の cwd)")

	add := &cobra.Command{
		Use:   "add",
		Short: "stdin の JSON (kind、title、detail、files、default_taken、session_id) でメモを置き、{created, item} を出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPlacer(cmd, cwd, func(paths apppath.Paths, p tasklog.Placer, cwd string) (any, error) {
				return parkAdd(paths.StateDir(), parking.New(paths.StateDir()), cmd.InOrStdin(), cwd, p, time.Now())
			})
		},
	}
	var status, session string
	set := &cobra.Command{
		Use:   "set <id>",
		Short: "メモの status (open / handed / discarded) を書き換える",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPlacer(cmd, cwd, func(paths apppath.Paths, p tasklog.Placer, cwd string) (any, error) {
				return parkSet(paths.StateDir(), parking.New(paths.StateDir()), p, session, cwd, args[0], status, time.Now())
			})
		},
	}
	set.Flags().StringVar(&status, "status", "", "open / handed / discarded")
	_ = set.MarkFlagRequired("status")
	var repo, listStatus, task string
	var here bool
	list := &cobra.Command{
		Use:   "list",
		Short: "cwd の domain のメモを JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if task != "" && session == "" {
				return errors.New("--task には --session が要る")
			}
			return withPark(cmd, cwd, func(s parking.Store, p tasklog.Placer, cwd string) (any, error) {
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
	list.Flags().StringVar(&task, "task", "", "--session のセッションがこのタスクの途中で置いたメモで絞る")
	adopt := &cobra.Command{
		Use:   "adopt <session_id> <task_id>",
		Short: "終了したセッションの未完了のタスクを kind task のメモにし、タスクの記録を消して {created, item} を出す",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPlacer(cmd, cwd, func(paths apppath.Paths, p tasklog.Placer, cwd string) (any, error) {
				return parkAdopt(paths.StateDir(), parking.New(paths.StateDir()), p, session, cwd, args[0], args[1], time.Now())
			})
		},
	}
	for _, c := range []*cobra.Command{set, list, adopt} {
		c.Flags().StringVar(&session, "session", "", "開いたセッションの ID。記録が業務の側にあれば業務のメモを扱う (status --board と同じ)")
	}
	cmd.AddCommand(add, set, list, adopt)
	return cmd
}

// withPark は置き場と domain の決め方を環境から組んで fn を呼び、結果を JSON で stdout に書く。
func withPark(cmd *cobra.Command, cwd string, fn func(parking.Store, tasklog.Placer, string) (any, error)) error {
	return withPlacer(cmd, cwd, func(p apppath.Paths, placer tasklog.Placer, cwd string) (any, error) {
		return fn(parking.New(p.StateDir()), placer, cwd)
	})
}

// withPlacer は domain の決め方を環境から組んで fn を呼び、結果を JSON で stdout に書く。
// cwd が空なら今の cwd にする。park と edge が使う。
func withPlacer(cmd *cobra.Command, cwd string, fn func(apppath.Paths, tasklog.Placer, string) (any, error)) error {
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	v, err := fn(p, tasklog.LoadPlacer(p, os.Getenv("HOME")), cwd)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}
