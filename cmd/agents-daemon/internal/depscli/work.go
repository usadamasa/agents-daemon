package depscli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// newWorkCmd は `agents-daemon work set|get` を組み立てる (worklog、#368)。set は人 (アプリ) が題を直すときと companion
// (mod) が推定の題を書くとき、get は companion が記録 (turns と推定の題の有無) を読むときに使う。
func newWorkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "work", Short: "セッションの作業の記録 (work/) を読み書きする"}
	set := &cobra.Command{
		Use:   "set",
		Short: "stdin の WorkSetRequest で作業の題 (人の直した subject、companion の inferred) を書き、Work を出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("WorkSetRequest", func(p apppath.Paths, req protocol.WorkSetRequest) (any, error) {
				return workSet(p.StateDir(), worklog.New(p.StateDir()), req, time.Now())
			}))
		},
	}
	get := &cobra.Command{
		Use:   "get",
		Short: "stdin の WorkGetRequest で作業の記録を Work で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("WorkGetRequest", func(p apppath.Paths, req protocol.WorkGetRequest) (any, error) {
				rec, ok := worklog.New(p.StateDir()).Get(req.SessionID)
				if !ok {
					return nil, fmt.Errorf("セッション %s の作業の記録が無い", req.SessionID)
				}
				return rec, nil
			}))
		},
	}
	cmd.AddCommand(set, get)
	return cmd
}

// workSet は s.Set を走らせ、何を書いたかを出来事のログに書く (work.set)。題そのものは書かない。
func workSet(stateDir string, s worklog.Store, req protocol.WorkSetRequest, now time.Time) (worklog.Record, error) {
	var change worklog.Change
	fields := []string{}
	if req.Subject != "" {
		change.Subject = &req.Subject
		fields = append(fields, kv("origin", worklog.OriginHuman))
	}
	if req.Inferred != "" {
		change.Inferred = &req.Inferred
		fields = append(fields, kv("inferred", true))
	}
	rec, err := s.Set(req.SessionID, change, now)
	if err != nil {
		_ = eventlog.Record(stateDir, req.SessionID, "work.set.failed", append(fields, kv("reason", err)), now)
		return rec, err
	}
	_ = eventlog.Record(stateDir, req.SessionID, "work.set", fields, now)
	return rec, nil
}
