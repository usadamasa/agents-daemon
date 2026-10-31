// Package parkcli はパーキングロットのコマンド (メモを置く・書き換える・一覧する park と、メモを渡す先の
// セッションを起こす spawn) の組み立てを持つ。
package parkcli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newParkCmd(), newSpawnCmd()}
}

// newParkCmd は `agents-daemon park add|set|list|discard|adopt` を組み立てる (parking)。
// 入力は stdin の JSON で受け、結果は JSON で stdout に出す。
func newParkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "park", Short: "パーキングロット (脇のメモ) を置く・書き換える・一覧する"}
	sub := func(use, short string, fn func(apppath.Paths, io.Reader) (any, error)) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, fn)
		}}
	}
	// add は読めなかった入力も park.failed に書く (model が park の tool に書いた欄の誤りを後から追えるように)。
	add := sub("add", "stdin の ParkAddRequest でメモを置き、ParkAdded を出す", func(p apppath.Paths, stdin io.Reader) (any, error) {
		var req protocol.ParkAddRequest
		if err := protocol.Decode(stdin, "ParkAddRequest", &req); err != nil {
			parkFailed(p.StateDir(), time.Now(), "add", "", err)
			return nil, err
		}
		cwd, err := statecmd.Cwd(req.CWD)
		if err != nil {
			return nil, err
		}
		return parkAdd(p.StateDir(), parking.New(p.StateDir()), req, cwd, tasklog.New(p.StateDir()), time.Now())
	})
	set := sub("set", "stdin の ParkSetRequest でメモの status を書き換え、ParkingItem を出す", statecmd.Decoded("ParkSetRequest", func(p apppath.Paths, req protocol.ParkSetRequest) (any, error) {
		dest := ""
		switch {
		case req.ToSession != "":
			dest = parking.ToSession(req.ToSession)
		case req.ToRepo != "":
			dest = parking.ToRepo(req.ToRepo)
		}
		return parkSet(p.StateDir(), parking.New(p.StateDir()), req.SessionID, req.ID, req.Status, req.From, dest, time.Now())
	}))
	list := sub("list", "stdin の ParkListRequest で絞ったメモを ParkingItemList で出す", statecmd.Decoded("ParkListRequest", func(p apppath.Paths, req protocol.ParkListRequest) (any, error) {
		repo := req.Repo
		if req.Here {
			cwd, err := statecmd.Cwd(req.CWD)
			if err != nil {
				return nil, err
			}
			repo = gitrepo.RepoKey(cwd)
		}
		items, err := parking.New(p.StateDir()).Find(repo, req.Status)
		if err != nil || req.Task == "" {
			return protocol.ParkingItemList(items), err
		}
		return protocol.ParkingItemList(parking.PlacedDuring(items, req.SessionID, req.Task)), nil
	}))
	discard := sub("discard", "stdin の ParkDiscardRequest で古い open のメモを discarded にし、捨てたメモを ParkingItemList で出す", statecmd.Decoded("ParkDiscardRequest", func(p apppath.Paths, req protocol.ParkDiscardRequest) (any, error) {
		items, err := parkDiscard(p.StateDir(), parking.New(p.StateDir()), req.SessionID, req.Days, time.Now())
		return protocol.ParkingItemList(items), err
	}))
	adopt := sub("adopt", "stdin の ParkAdoptRequest で終了したセッションの未完了のタスクをメモにし、ParkAdded を出す", statecmd.Decoded("ParkAdoptRequest", func(p apppath.Paths, req protocol.ParkAdoptRequest) (any, error) {
		sessionID, taskID, ok := ref.ParseTask(req.Ref)
		if !ok {
			return nil, fmt.Errorf("参照 %q は task:<session_id>/<task_id> の形でない", req.Ref)
		}
		exited := func(sessionID string, after time.Time) error {
			return daemonstate.SessionExited(p, sessionID, after)
		}
		return parkAdopt(p.StateDir(), parking.New(p.StateDir()), tasklog.New(p.StateDir()), req.SessionID, sessionID, taskID, exited, time.Now())
	}))
	cmd.AddCommand(add, set, list, discard, adopt)
	return cmd
}
