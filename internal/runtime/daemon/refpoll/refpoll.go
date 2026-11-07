// Package refpoll は daemon が辺の端点の外部の参照 (issue / PR) の状態を gh で引き直して写し (refstate) へ書き
// (#416)、済んだ参照を before に持つ辺を消す (#422)。daemon の tick ごとに Refresh を 1 回呼ぶ。手順の性質
// (gh の失敗で完了に倒さない、open の参照の辺を消さない、済んだ参照はいずれ完了になり、その辺はいずれ消える) は
// models/refstate が持つ。
package refpoll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/refstate"
)

// fetchTimeout は gh の呼び出し 1 回を待つ上限。tick の中で呼ぶので、network が詰まっても pane の監視を
// 長く止めない。
const fetchTimeout = 10 * time.Second

// Remote は引いた issue / PR 1 つ。State は refstate の語。
type Remote struct {
	State string
	Title string
}

// Fetcher は issue / PR の今の状態と題を引く。
type Fetcher func(ctx context.Context, it ref.Item) (Remote, error)

// failure は gh が失敗した参照 1 つの、次に引ける時刻とログに書いた理由。
type failure struct {
	retryAt time.Time
	reason  string
}

// Poller は引き直しの状態 (失敗した参照) を daemon の寿命の間持つ。
type Poller struct {
	edges    edge.Store
	refs     refstate.Store
	fetch    Fetcher
	log      *applog.Logger
	failures map[string]failure
}

// New は edges の端点の参照を fetch で引き、refs へ書く Poller を作る。
func New(edges edge.Store, refs refstate.Store, fetch Fetcher, log *applog.Logger) *Poller {
	return &Poller{edges: edges, refs: refs, fetch: fetch, log: log, failures: map[string]failure{}}
}

// Refresh は辺の端点の issue / PR のうち引き直す時期のもの (refstate.Due) を 1 つ選んで引き、写しへ書く。
// 1 回に 1 つにして、tick (既定 5 秒) の間隔で gh の呼び出しをばらす。選ぶ順は、まだ引いていないもの、前に引いた
// 時刻の古いもの、参照の順。gh が失敗したら写しを書かず (済んでいない参照を完了と見せない)、その参照は
// RefreshInterval のあいだ引かない。失敗の理由は参照ごとに、変わったときだけログに書く (gh が無いような全部に
// 効く理由は参照の数だけ 1 回ずつ)。引いた後に、済んだ参照を before に持つ辺を消す (sweep)。nil の Poller は
// 何もしない。
func (p *Poller) Refresh(ctx context.Context, now time.Time) {
	if p == nil || p.refs.Dir == "" {
		return
	}
	edges, err := p.edges.Find("", "")
	if err != nil {
		p.log.Logf("辺の読み込みに失敗、外部の参照を引きません: %v", err)
		return
	}
	cached, err := p.refs.List()
	if err != nil {
		p.log.Logf("外部の参照の写しの読み込みに失敗、引きません: %v", err)
		return
	}
	if due := p.due(edges, cached, now); len(due) > 0 {
		if rec, ok := p.refresh(ctx, due[0], cached[due[0]], now); ok {
			cached[rec.Ref] = rec
		}
	}
	p.sweep(edges, cached)
}

// due は辺の端点の issue / PR のうち、引き直す時期のものを引く順に返す。辺から消えた参照の失敗の記録は捨てる。
func (p *Poller) due(edges []edge.Edge, cached map[string]refstate.Record, now time.Time) []string {
	referenced := map[string]bool{}
	var due []string
	for _, e := range edges {
		for _, ep := range []string{e.Before.Ref, e.After.Ref} {
			if _, ok := ref.ParseItem(ep); !ok || referenced[ep] {
				continue
			}
			referenced[ep] = true
			rec, found := cached[ep]
			if refstate.Due(rec, found, now) && !now.Before(p.failures[ep].retryAt) {
				due = append(due, ep)
			}
		}
	}
	for k := range p.failures {
		if !referenced[k] {
			delete(p.failures, k)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := cached[due[i]].CheckedAt, cached[due[j]].CheckedAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return due[i] < due[j]
	})
	return due
}

// sweep は写しが merged / closed の参照を before に持つ辺を消す (#422)。待っていたものが済んだ辺は、依存図で畳む
// だけでなく記録からも消し、書いた側が `edge rm` を覚えていなくても溜まらないようにする。済んだ参照が after に
// あるだけの辺と、取りやめの参照の辺 (待っていたものは届いていないので、人が付け替えるか `edge rm` する) は残す。写しが済んだ状態に変わったときだけでなく毎回掃くので、済んだ PR へ後から書いた辺も消える
// (閉じた参照は引き直さないため、変わったときだけだと残り続ける。models/refstate の EdgeRemoved)。
func (p *Poller) sweep(edges []edge.Edge, cached map[string]refstate.Record) {
	for _, e := range edges {
		state := cached[e.Before.Ref].State
		if !refstate.Done(state) {
			continue
		}
		removed, err := p.edges.Remove(e.Before, e.After)
		if err != nil {
			p.log.Logf("辺 %s を消せません: %v", e.ID, err)
			continue
		}
		if removed.Removed {
			p.log.Logf("辺 %s を消しました: %s → %s (%s は %s)", e.ID, e.Before.Ref, e.After.Ref, e.Before.Ref, state)
		}
	}
}

// refresh は参照 1 つを引いて写しへ書き、書いた写しを返す。書かなかったら ok が false。
func (p *Poller) refresh(ctx context.Context, key string, prev refstate.Record, now time.Time) (refstate.Record, bool) {
	it, _ := ref.ParseItem(key)
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	remote, err := p.fetch(fetchCtx, it)
	if err != nil {
		reason := err.Error()
		if reason != p.failures[key].reason {
			p.log.Logf("外部の参照の状態を引けません (外部のまま残します): %s: %s", key, reason)
		}
		p.failures[key] = failure{retryAt: now.Add(refstate.RefreshInterval), reason: reason}
		return refstate.Record{}, false
	}
	if _, failed := p.failures[key]; failed {
		delete(p.failures, key)
		p.log.Logf("外部の参照の状態を引けるようになりました: %s", key)
	}
	state := remote.State
	rec := refstate.Record{Ref: key, State: state, Title: remote.Title, CheckedAt: now}
	if err := p.refs.Put(rec); err != nil {
		p.log.Logf("外部の参照の写しの書き込みに失敗: %v", err)
		return refstate.Record{}, false
	}
	switch {
	case prev.State == state:
	case refstate.Done(state):
		p.log.Logf("%s は %s (依存図で完了にします)", key, state)
	case state == refstate.StateDropped:
		p.log.Logf("%s は取りやめ (依存図で止まっているノードにし、辺は残します)", key)
	}
	return rec, true
}

// GH は gh で issue / PR の状態と題を引く。daemon は Claude Code の sandbox の外で動き、gh の認証 (~/.config/gh) と
// network をそのまま使う。gh は SessionStart の hook から継いだ PATH で探す。
func GH(ctx context.Context, it ref.Item) (Remote, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return Remote{}, fmt.Errorf("gh が PATH に無い: %w", err)
	}
	sub, fields := "issue", "state,stateReason,title"
	if it.Kind == "pr" {
		sub, fields = "pr", "state,title"
	}
	cmd := exec.CommandContext(ctx, bin, sub, "view", strconv.Itoa(it.Number), "--repo", it.Repo, "--json", fields) // #nosec G204 -- 固定の gh のサブコマンドと、ref.ParseItem で検証した repo と番号
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			if line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); line != "" {
				return Remote{}, fmt.Errorf("gh %s view が失敗: %s", sub, line)
			}
		}
		return Remote{}, fmt.Errorf("gh %s view が失敗: %w", sub, err)
	}
	return decodeView(sub, out)
}

// decodeView は gh の view の JSON (sub は issue か pr) を状態と題にする。
func decodeView(sub string, out []byte) (Remote, error) {
	var v struct {
		State       string `json:"state"`
		StateReason string `json:"stateReason"`
		Title       string `json:"title"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return Remote{}, fmt.Errorf("gh %s view の出力を読めない: %w", sub, err)
	}
	state, err := ghState(sub, v.State, v.StateReason)
	if err != nil {
		return Remote{}, err
	}
	return Remote{State: state, Title: v.Title}, nil
}

// ghState は gh の state (issue は stateReason も) を写しの状態にする。閉じた PR はマージしたかで、閉じた issue は
// 閉じた理由で、済んだか取りやめかを分ける。理由の無い閉じた issue (理由ができる前に閉じたもの) は GitHub の既定の
// 理由 (completed) とみなす。
func ghState(kind, state, reason string) (string, error) {
	switch {
	case state == "OPEN":
		return refstate.StateOpen, nil
	case kind == "pr" && state == "MERGED":
		return refstate.StateMerged, nil
	case kind == "pr" && state == "CLOSED":
		return refstate.StateDropped, nil
	case kind == "issue" && state == "CLOSED" && (reason == "NOT_PLANNED" || reason == "DUPLICATE"):
		return refstate.StateDropped, nil
	case kind == "issue" && state == "CLOSED":
		return refstate.StateClosed, nil
	}
	return "", fmt.Errorf("gh %s view の状態 %q (理由 %q) を知らない", kind, state, reason)
}
