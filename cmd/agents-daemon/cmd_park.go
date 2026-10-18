package main

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/board"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/domain"
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
		Short: "stdin の JSON (kind、title、detail、files、default_taken、session_id) で件を置く",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPark(cmd, cwd, func(e parkEnv) (any, error) { return e.add(cmd.InOrStdin()) })
		},
	}
	var status, session string
	set := &cobra.Command{
		Use:   "set <id>",
		Short: "件の status (open / handed / discarded) を書き換える",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPark(cmd, cwd, func(e parkEnv) (any, error) {
				return e.store.Set(e.tasks.SessionDomain(session, e.domain), args[0], status)
			})
		},
	}
	set.Flags().StringVar(&status, "status", "", "open / handed / discarded")
	_ = set.MarkFlagRequired("status")
	var repo, listStatus string
	list := &cobra.Command{
		Use:   "list",
		Short: "cwd の domain の件を JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPark(cmd, cwd, func(e parkEnv) (any, error) {
				return e.store.Find(e.tasks.SessionDomain(session, e.domain), repo, listStatus)
			})
		},
	}
	list.Flags().StringVar(&repo, "repo", "", "repo (main worktree のパス) で絞る")
	list.Flags().StringVar(&listStatus, "status", "", "status で絞る")
	for _, c := range []*cobra.Command{set, list} {
		c.Flags().StringVar(&session, "session", "", "開いたセッションの ID。記録が業務の側にあれば業務の件を扱う (status --board と同じ)")
	}
	cmd.AddCommand(add, set, list)
	return cmd
}

// parkEnv は 1 回の park の実行で使う置き場と、cwd から決めた domain。
type parkEnv struct {
	store  parking.Store
	tasks  tasklog.Store
	domain domain.Domain
	cwd    string
}

// withPark は cwd から parkEnv を組んで fn を呼び、結果を JSON で stdout に書く。
func withPark(cmd *cobra.Command, cwd string, fn func(parkEnv) (any, error)) error {
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
	c := cfg.Classifier(os.Getenv("HOME"))
	e := parkEnv{store: parking.New(p.StateDir()), tasks: tasklog.New(p.StateDir()), domain: c.Classify(cwd), cwd: cwd}
	v, err := fn(e)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}

func (e parkEnv) add(stdin io.Reader) (parking.Item, error) {
	in, err := parking.ReadRequest(stdin)
	if err != nil {
		return parking.Item{}, err
	}
	// 業務に倒したセッションの件は、cwd が私用でも業務に置く (ボードと同じ規則)。
	d := e.tasks.SessionDomain(in.SessionID, e.domain)
	repo := board.Locate(e.cwd).Repo
	if repo == "" {
		// git の外はボードの行と同じく cwd を鍵にする。
		repo = e.cwd
	}
	src := parking.Source{SessionID: in.SessionID, TaskID: e.tasks.ActiveTaskID(d, in.SessionID), CWD: e.cwd, At: time.Now()}
	return e.store.Add(d, repo, in.Entry, src)
}
