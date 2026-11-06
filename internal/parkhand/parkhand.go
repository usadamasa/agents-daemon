// Package parkhand は、パーキングのメモを生きているセッションか新しく起こすセッションへ渡す手順 (`agents-daemon park hand`) を
// 持つ (#364)。
//
// 手順は 3 歩で、取る (park set の status handed、from open、宛先)、送る (tell か spawn)、届かなければ戻す
// (status open、from handed)。もとは依存図の mod (hooks/parking.ts の handOff) が 3 回のコマンドで行っていたのを、
// 1 つのプロセスが直列に行う形にした。取れるのは 1 つの呼び手だけで、取られたメモはほかの依存図に open と出ても
// 取れない。手順の交錯で破れないことは models/handoff の Handoff.tla (Claim → Send / Spawn → Revert) が持つ。
//
// 渡す文面の雛形もここが持つ。受け手は Claude Code のセッションで、メモの前提が今も成り立つかを先に確かめさせる。
package parkhand

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// 渡した先に頼むこと。
const (
	IntentTask  = "task"
	IntentIssue = "issue"
)

// Request と Result の形は protocol の schema が持つ。
type (
	Request = protocol.ParkHandRequest
	Result  = protocol.ParkHandResult
)

// Runner は渡す手段。cmd が parking の Store、tell、spawn をつなぎ、テストは差し替える。
type Runner struct {
	// Memo はメモ id の記録 (parking.Store.Get)。
	Memo func(id string) (parking.Item, error)
	// Set は park set と同じ書き換え (status、from、宛先)。失敗の理由を返す。ログは呼び手が書く。
	Set func(id, status, from, to string) error
	// Tell は生きているセッションの入力欄に文面を打つ (tell)。
	Tell func(ctx context.Context, sessionID, text string) (protocol.TellResult, error)
	// Spawn は repo で新しいセッションを起こし、文面を最初のターンにする (spawn)。
	Spawn func(ctx context.Context, repo, prompt string) (protocol.SpawnResult, error)
	// SessionNames は session ID から名前への対応 (sessioncontext.Store.ListSessionNames)。nil なら ID のまま書く。
	SessionNames func() map[string]string
}

// Run は req のメモを宛先へ渡す。取る前に落ちる (入力が違う、メモが無い、取れない) ときは error で、何も変えていない。
// 取れた後は届かなくても Result で返し、Status が渡した後のメモの status になる。
func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	if (req.ToSession == "") == (req.ToRepo == "") {
		return Result{}, errors.New("宛先は to_session か to_repo のどちらか 1 つ")
	}
	if req.Intent != IntentTask && req.Intent != IntentIssue {
		return Result{}, fmt.Errorf("intent は task か issue (%q)", req.Intent)
	}
	item, err := r.Memo(req.ID)
	if err != nil {
		return Result{}, err
	}
	var names map[string]string
	if r.SessionNames != nil {
		names = r.SessionNames()
	}
	dest := parking.ToSession(req.ToSession)
	if req.ToRepo != "" {
		dest = parking.ToRepo(req.ToRepo)
	}
	if err := r.Set(req.ID, parking.StatusHanded, parking.StatusOpen, dest); err != nil {
		return Result{}, fmt.Errorf("メモを取れない (ほかが先に渡したか、捨てたかもしれない): %w", err)
	}
	if req.ToRepo != "" {
		return r.spawn(ctx, req, item, names), nil
	}
	return r.tell(ctx, req, item, names), nil
}

// tell は取ったメモを生きているセッションへ打つ。届かなければ戻す。
func (r Runner) tell(ctx context.Context, req Request, item parking.Item, names map[string]string) Result {
	told, err := r.Tell(ctx, req.ToSession, text(item, req.Intent, filepath.Base(item.Repo), req.Note, names))
	if err != nil {
		return r.revert(req.ID, Result{To: req.ToSession, Reason: err.Error()})
	}
	return Result{Delivered: true, Status: parking.StatusHanded, To: req.ToSession, PaneID: told.PaneID, Warning: told.Warning}
}

// spawn は取ったメモを新しく起こすセッションへ渡す。起こせなければ戻す。window はできたが claude が前景になるのを待ち切れた
// ときは claude が起きているかもしれないので戻さない (戻すと押し直しで 2 つ目のセッションが起きる)。
func (r Runner) spawn(ctx context.Context, req Request, item parking.Item, names map[string]string) Result {
	title := filepath.Base(req.ToRepo)
	prompt := fmt.Sprintf("このメモを渡すため、%s (%s) で新しいセッションとして起こしました。\n%s", title, req.ToRepo, text(item, req.Intent, title, req.Note, names))
	res, err := r.Spawn(ctx, req.ToRepo, prompt)
	if err != nil {
		return r.revert(req.ID, Result{Reason: err.Error()})
	}
	if !res.Foreground {
		return Result{Status: parking.StatusHanded, To: res.SessionID, PaneID: res.PaneID, Reason: res.Detail}
	}
	return Result{Delivered: true, Status: parking.StatusHanded, To: res.SessionID, PaneID: res.PaneID}
}

// revert は届かなかったメモを open に戻す。戻せなければ handed のまま残るので、理由に続けて人が戻せるようにする。
func (r Runner) revert(id string, res Result) Result {
	if err := r.Set(id, parking.StatusOpen, parking.StatusHanded, ""); err != nil {
		res.Status = parking.StatusHanded
		res.Reason += "。メモを open に戻せなかった (handed のまま): " + err.Error()
		return res
	}
	res.Status = parking.StatusOpen
	return res
}

// premiseCheck は渡す文面で依頼の前に置く確かめ。メモは置いた時点の前提で書かれていて、渡すまでにレビューや別の変更で
// 前提が消えていることがある。渡したメモは handed になり一覧から消えるので、受け手はメモの status を触らずに終えてよい。
const (
	premiseCheck = "先に、メモの前提 (詳細と関係するファイル) が今のコードと計画で成り立つかを確かめてください。"
	premiseGone  = "前提が成り立たないとき (該当箇所が無くなった、別の変更で直った) は、その理由を 1 行で返して終えてください。"
)

// text は渡す文面。intent が task ならタスクにする依頼 (依存図はタスクの description に書かれた id でメモと照合する)、
// issue なら repoTitle の repo に issue を立てる依頼。note (利用者の補足) は空でなければ末尾に足す。names は補足を書いた
// セッションの名前 (無ければ ID のまま)。
func text(item parking.Item, intent, repoTitle, note string, names map[string]string) string {
	var head string
	if intent == IntentIssue {
		head = fmt.Sprintf("パーキングロットのメモ「%s」を GitHub の issue にしてください。repo: %s。", item.Title, repoTitle) +
			premiseCheck + "成り立つときは起票し、URL を返してください。" + premiseGone
	} else {
		head = fmt.Sprintf("パーキングロットのメモ「%s」(種類: %s) をタスクにしてください。", item.Title, kindLabel(item)) +
			premiseCheck +
			fmt.Sprintf("成り立つときは TaskCreate で作り、タスクの description にメモの id (%s) を入れてください。", item.ID) +
			"既にこのメモを扱うタスクがあれば、新しく作らずにその description に id を書き足すだけで構いません。" +
			"着手の時期は今の計画 (タスクの依存) に従ってください。" + premiseGone
	}
	body := strings.Join(append([]string{head}, contextLines(item, names)...), "\n")
	if said := strings.TrimSpace(note); said != "" {
		body += "\n利用者からの補足: " + said
	}
	return body
}

// kindLabel はメモの種類の語。終了したセッションから移したメモ (origin adopted) は adopted を添える。
func kindLabel(item parking.Item) string {
	if item.Origin == parking.OriginAdopted {
		return item.Kind + "・adopted"
	}
	return item.Kind
}

// contextLines は詳細、置いたセッションの補足、ファイル、置いたときの既定、メモの id。
func contextLines(item parking.Item, names map[string]string) []string {
	var lines []string
	if item.Detail != "" {
		lines = append(lines, "詳細: "+item.Detail)
	}
	for _, src := range item.Sources {
		if src.Detail == "" {
			continue
		}
		name := names[src.SessionID]
		if name == "" {
			name = src.SessionID
		}
		lines = append(lines, fmt.Sprintf("補足 (%s): %s", name, src.Detail))
	}
	if len(item.Files) > 0 {
		lines = append(lines, "関係するファイル: "+strings.Join(item.Files, ", "))
	}
	if item.DefaultTaken != "" {
		lines = append(lines, "置いたときの既定: "+item.DefaultTaken)
	}
	return append(lines, fmt.Sprintf("(メモ %s)", item.ID))
}
