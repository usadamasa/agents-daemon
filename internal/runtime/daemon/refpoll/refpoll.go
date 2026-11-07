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

// Fetcher は issue / PR の今の状態 (refstate の語) を引く。
type Fetcher func(ctx context.Context, it ref.Item) (string, error)

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
// あるだけの辺は残す。写しが済んだ状態に変わったときだけでなく毎回掃くので、済んだ PR へ後から書いた辺も消える
// (merged / closed は引き直さないため、変わったときだけだと残り続ける。models/refstate の EdgeRemoved)。
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
	state, err := p.fetch(fetchCtx, it)
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
	rec := refstate.Record{Ref: key, State: state, CheckedAt: now}
	if err := p.refs.Put(rec); err != nil {
		p.log.Logf("外部の参照の写しの書き込みに失敗: %v", err)
		return refstate.Record{}, false
	}
	if refstate.Done(state) && prev.State != state {
		p.log.Logf("%s は %s (依存図で完了にします)", key, state)
	}
	return rec, true
}

// GH は gh で issue / PR の状態を引く。daemon は Claude Code の sandbox の外で動き、gh の認証 (~/.config/gh) と
// network をそのまま使う。gh は SessionStart の hook から継いだ PATH で探す。
func GH(ctx context.Context, it ref.Item) (string, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return "", fmt.Errorf("gh が PATH に無い: %w", err)
	}
	sub := "issue"
	if it.Kind == "pr" {
		sub = "pr"
	}
	cmd := exec.CommandContext(ctx, bin, sub, "view", strconv.Itoa(it.Number), "--repo", it.Repo, "--json", "state") // #nosec G204 -- 固定の gh のサブコマンドと、ref.ParseItem で検証した repo と番号
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			if line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); line != "" {
				return "", fmt.Errorf("gh %s view が失敗: %s", sub, line)
			}
		}
		return "", fmt.Errorf("gh %s view が失敗: %w", sub, err)
	}
	var v struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("gh %s view の出力を読めない: %w", sub, err)
	}
	state := strings.ToLower(v.State)
	if state != refstate.StateOpen && !refstate.Done(state) {
		return "", fmt.Errorf("gh %s view の状態 %q を知らない", sub, v.State)
	}
	return state, nil
}
