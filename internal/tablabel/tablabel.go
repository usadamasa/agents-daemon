// Package tablabel は、共有の rex session にタブとして起きたセッションの、タブ (window) の名前を読み、付け直す (#722)。
// 付け直す名前は mod (hooks/tab-label.ts) がセッションの会話から推定し、`agents-daemon tab-label` で渡す。
// mod や hook から多重化ツールを叩かない決めごとに合わせ、rex を叩くのはこのバイナリ。
// 人が付けた名前も付け直す (作業が変わったら名前も追う、という利用者の選択)。session の名前には触れない。
package tablabel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/usadamasa/agents-daemon/internal/rexplace"
)

// Host は block の居る session と window を引き、名前を付ける口。rex の backend が持つ。
type Host interface {
	ResolveBlock(ctx context.Context, blockID string) (string, error)
	WindowOf(ctx context.Context, session, block string) (windowID, label string, err error)
	SetWindowLabel(ctx context.Context, session, windowID, label string) error
}

// Request は読む・付ける対象 (自分の場所) と付ける名前。Label が空なら今の名前を読むだけ。
// session は場所の block から引く。
type Request struct {
	Place rexplace.Place
	Label string
}

// Status は Run がしたこと。
type Status string

const (
	// StatusCurrent は読んだだけ。
	StatusCurrent Status = "current"
	// StatusRenamed は付け直した。
	StatusRenamed Status = "renamed"
	// StatusUnchanged は付ける名前が今と同じで、付けなかった。
	StatusUnchanged Status = "unchanged"
)

// Result は window と、Run の後の名前。
type Result struct {
	WindowID string
	Label    string
	Status   Status
}

// labelLimit は付ける名前の文字数の上限 (rex 自体は 128 文字まで受ける)。mod は 15 字前後を頼み、超えた返事はこの長さで
// 切ってから渡す。ここは mod を通らない呼び出しへの柵。
const labelLimit = 64

// validLabel は名前が 1 行で labelLimit 文字までで、前後の空白と制御文字を持たないか (rex は制御文字を bad_request で拒む)。
func validLabel(label string) bool {
	if strings.TrimSpace(label) != label || utf8.RuneCountInString(label) > labelLimit {
		return false
	}
	return !strings.ContainsFunc(label, unicode.IsControl)
}

// Runner は Host で Request を処理する。
type Runner struct {
	Host Host
}

// Run は block の居る window の名前を読み、Label があれば付け直す。
func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	block := req.Place.Block
	if block == "" {
		return Result{}, errors.New("rex の block の中で呼ぶ")
	}
	if req.Label != "" && !validLabel(req.Label) {
		return Result{}, fmt.Errorf("タブの名前 %q は 1 行で %d 文字まで、前後の空白と制御文字を付けない", req.Label, labelLimit)
	}
	session, err := r.Host.ResolveBlock(ctx, block)
	if err != nil {
		return Result{}, fmt.Errorf("rex の block %s の居る session を引けない: %w", block, err)
	}
	window, current, err := r.Host.WindowOf(ctx, session, block)
	if err != nil {
		return Result{}, err
	}
	switch req.Label {
	case "":
		return Result{WindowID: window, Label: current, Status: StatusCurrent}, nil
	case current:
		return Result{WindowID: window, Label: current, Status: StatusUnchanged}, nil
	}
	if err := r.Host.SetWindowLabel(ctx, session, window, req.Label); err != nil {
		return Result{}, err
	}
	return Result{WindowID: window, Label: req.Label, Status: StatusRenamed}, nil
}
