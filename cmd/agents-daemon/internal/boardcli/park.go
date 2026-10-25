// Package boardcli はボードに載る記録 (タスク、パーキングロット、セッションをまたぐ辺) を書く・読むコマンド
// (park、edge、ingest-task) と、ボードの経路のログに mod の出来事を書く board-log の組み立てを持つ。
package boardcli

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newIngestTaskCmd(), newParkCmd(), newBoardLogCmd(), newEdgeCmd(), newSpawnCmd()}
}

// newParkCmd は `agents-daemon park add|set|list|discard|adopt` を組み立てる (internal/parking)。
// repo は --cwd (既定は今の cwd) から決め、結果は JSON で stdout に出す。
func newParkCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{Use: "park", Short: "パーキングロット (脇のメモ) を置く・書き換える・一覧する", Annotations: jsonflag.Supported()}
	cmd.PersistentFlags().StringVar(&cwd, "cwd", "", "置いたセッションの cwd。repo をここから決める (既定は今の cwd)")

	add := &cobra.Command{
		Use:   "add",
		Short: "stdin の JSON (kind、title、detail、files、default_taken、session_id) でメモを置き、{created, item} を出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withState(cmd, cwd, func(paths apppath.Paths, cwd string) (any, error) {
				return parkAdd(paths.StateDir(), parking.New(paths.StateDir()), cmd.InOrStdin(), cwd, tasklog.New(paths.StateDir()), time.Now())
			})
		},
	}
	var status, from, session string
	set := &cobra.Command{
		Use:   "set <id>",
		Short: "メモの status (open / handed / discarded) を書き換える",
		Long: "--from を付けると、今の status がそれと違うときは書き換えずにエラーで返す。\n" +
			"ボードの mod はメモを渡す前に `--status handed --from open` で取り、届かなければ `--status open --from handed` で戻す。",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withState(cmd, cwd, func(paths apppath.Paths, _ string) (any, error) {
				return parkSet(paths.StateDir(), parking.New(paths.StateDir()), session, args[0], status, from, time.Now())
			})
		},
	}
	set.Flags().StringVar(&status, "status", "", "open / handed / discarded")
	set.Flags().StringVar(&from, "from", "", "今の status がこれのときだけ書き換える (open / handed / discarded)")
	_ = set.MarkFlagRequired("status")
	var repo, listStatus, task string
	list := &cobra.Command{
		Use:   "list",
		Short: "メモを JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if task != "" && session == "" {
				return errors.New("--task には --session が要る")
			}
			return withState(cmd, cwd, func(paths apppath.Paths, _ string) (any, error) {
				items, err := parking.New(paths.StateDir()).Find(repo, listStatus)
				if err != nil || task == "" {
					return items, err
				}
				return parking.PlacedDuring(items, session, task), nil
			})
		},
	}
	list.Flags().StringVar(&repo, "repo", "", "repo (main worktree のパス) で絞る")
	list.Flags().StringVar(&listStatus, "status", "", "status で絞る")
	list.Flags().StringVar(&task, "task", "", "--session のセッションがこのタスクの途中で置いたメモで絞る")
	var days int
	discard := &cobra.Command{
		Use:   "discard",
		Short: "最後に置かれてから --days 日より経った open のメモを discarded にし、捨てたメモを JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withState(cmd, cwd, func(paths apppath.Paths, _ string) (any, error) {
				return parkDiscard(paths.StateDir(), parking.New(paths.StateDir()), session, days, time.Now())
			})
		},
	}
	discard.Flags().IntVar(&days, "days", 0, "この日数より前に最後に置かれたメモを捨てる (1 以上)")
	_ = discard.MarkFlagRequired("days")
	adopt := &cobra.Command{
		Use:   "adopt <session_id> <task_id>",
		Short: "終了したセッションの未完了のタスクを kind task のメモにし、タスクの記録を消して {created, item} を出す",
		Long: "セッションが終了したことは、動いている daemon がタスクの記録より後に書いた status.json に pane が無いことで確かめる。\n" +
			"daemon が動いていない、status.json が古い、pane がある、のどれかなら移さない。",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withState(cmd, cwd, func(paths apppath.Paths, _ string) (any, error) {
				exited := func(sessionID string, after time.Time) error { return verify.SessionExited(paths, sessionID, after) }
				return parkAdopt(paths.StateDir(), parking.New(paths.StateDir()), tasklog.New(paths.StateDir()), session, args[0], args[1], exited, time.Now())
			})
		},
	}
	for _, c := range []*cobra.Command{set, list, discard, adopt} {
		c.Flags().StringVar(&session, "session", "", "操作したセッションの ID (ログに添える。list では --task のセッション)")
	}
	cmd.AddCommand(add, set, list, discard, adopt)
	return cmd
}

// withState は置き場を環境から決めて fn を呼び、結果を JSON で stdout に書く。
// cwd が空なら今の cwd にする。park、edge、spawn が使う。
func withState(cmd *cobra.Command, cwd string, fn func(apppath.Paths, string) (any, error)) error {
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	v, err := fn(p, cwd)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}
