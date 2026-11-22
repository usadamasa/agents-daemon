// Package record は、検証の記録を tree の hash を鍵に置き、ref の tree が通っているかを判定する。
package record

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/command"
)

// humanPaths は、人の確認が要るかを決めるパス。画面と mod は機械の点検で見ていない。
var humanPaths = []string{"gui", "types", ":(glob)hooks/*.ts", ":(glob)hooks/*.tsx"}

// notCovered は、この検証が見ていない操作。Status に載せ、pass をそこまで確かめたことと読ませない。
var notCovered = []string{"spawn", "tell", "park hand", "dream adopt", "dream forget", "見た目と IME (人の確認)"}

var (
	// ErrWrite は、記録を書けなかったことを表す。書けなければ、検証が通ったことにしない。
	ErrWrite = errors.New("記録を書けない")
	// ErrNoMachinePass は、機械の点検が通った記録が無いことを表す。
	ErrNoMachinePass = errors.New("機械の点検が通った記録が無い (先に check を通す)")
)

// Target は、記録を付ける先。記録は Dir/<tree>.json に置く。
type Target struct {
	Dir    string
	Repo   string
	Commit string
	Tree   string
}

// Check は、機械の点検の 1 項目の結果。
type Check struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

// note は tree に付ける検証の記録。
type note struct {
	Schema  int     `json:"schema"`
	Tree    string  `json:"tree"`
	Commit  string  `json:"commit"`
	Machine machine `json:"machine"`
	Human   *human  `json:"human"`
}

type machine struct {
	Result string  `json:"result"`
	At     string  `json:"at"`
	Checks []Check `json:"checks"`
}

type human struct {
	ApprovedAt string `json:"approved_at"`
}

// Now は記録に書く時刻。
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// WriteMachinePass は、機械の点検が通ったことを記録する。
func (t Target) WriteMachinePass(checks []Check) error {
	return t.write(note{
		Schema: 1, Tree: t.Tree, Commit: t.Commit, Machine: machine{Result: "pass", At: Now(), Checks: checks},
	})
}

// Approve は、人の確認を記録に書く。
func (t Target) Approve() error {
	var n note
	if raw := t.read(); raw == nil || json.Unmarshal(raw, &n) != nil || n.Machine.Result != "pass" {
		return ErrNoMachinePass
	}
	n.Human = &human{ApprovedAt: Now()}
	return t.write(n)
}

func (t Target) write(n note) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return fmt.Errorf("%w: %w", ErrWrite, err)
	}
	// 書きかけの記録を読ませないよう、別名で書いてから置き換える。
	tmp := t.path() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("%w: %w", ErrWrite, err)
	}
	if err := os.Rename(tmp, t.path()); err != nil {
		return fmt.Errorf("%w: %w", ErrWrite, err)
	}
	t.prune()
	return nil
}

// retention は記録の保持期間。記録が要るのはリリース PR をマージするまでで、リリースの間隔より十分長く取る。
const retention = 90 * 24 * time.Hour

// prune は保持期間を過ぎた記録を消す。
func (t Target) prune() {
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < retention {
			continue
		}
		_ = os.Remove(filepath.Join(t.Dir, entry.Name()))
	}
}

func (t Target) path() string {
	return filepath.Join(t.Dir, t.Tree+".json")
}

// read は tree の記録を返す。無ければ nil。
func (t Target) read() json.RawMessage {
	// #nosec G304 -- 読むのは、この道具が置き場に書いた記録。tree は git が解決した hash
	data, err := os.ReadFile(t.path())
	if err != nil || !json.Valid(data) {
		return nil
	}
	return json.RawMessage(data)
}

// Status は status が stdout に出す 1 行の JSON。Result は pass / human_required / unverified。
type Status struct {
	Schema        int             `json:"schema"`
	Result        string          `json:"result"`
	Tree          string          `json:"tree"`
	Commit        string          `json:"commit"`
	PreviousTag   string          `json:"previous_tag"`
	HumanRequired bool            `json:"human_required"`
	Record        json.RawMessage `json:"record"`
	NotCovered    []string        `json:"not_covered"`
}

// Status は、tree に通った記録があるかを判定する。
func (t Target) Status(ctx context.Context) Status {
	raw := t.read()
	var n note
	if raw == nil {
		raw = json.RawMessage("null")
	} else {
		// 形の合わない記録は、記録が無いものとして扱う。
		_ = json.Unmarshal(raw, &n)
		// status の stdout を 1 行に保つ。
		raw, _ = json.Marshal(n)
	}
	tag, humanRequired := t.humanRequired(ctx)
	return Status{
		Schema: 1, Result: result(n, humanRequired), Tree: t.Tree, Commit: t.Commit, PreviousTag: tag,
		HumanRequired: humanRequired, Record: raw, NotCovered: notCovered,
	}
}

// humanRequired は、前のリリースの tag から画面と mod に差分があるかを返す。tag が無ければ、人の確認を求める。
func (t Target) humanRequired(ctx context.Context) (tag string, required bool) {
	tag, err := command.GitOutput(ctx, t.Repo, "describe", "--tags", "--abbrev=0", t.Commit)
	if err != nil {
		return "", true
	}
	args := append([]string{"-C", t.Repo, "diff", "--quiet", tag, t.Commit, "--"}, humanPaths...)
	return tag, command.RunHost(ctx, "git", args...) != nil
}

// result は記録と、人の確認が要るかから Result を決める。
func result(n note, humanRequired bool) string {
	switch {
	case n.Machine.Result != "pass":
		return "unverified"
	case humanRequired && (n.Human == nil || n.Human.ApprovedAt == ""):
		return "human_required"
	}
	return "pass"
}
