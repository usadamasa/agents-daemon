// Package refpoll は daemon が辺の端点の外部の参照 (issue / PR) の状態を gh で引き直し、写し (refstate) へ書く
// (#416)。daemon の tick ごとに Refresh を 1 回呼ぶ。手順の性質 (gh の失敗で完了に倒さない、済んだ参照はいずれ
// 完了になる) は models/refstate が持つ。
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
// 効く理由は参照の数だけ 1 回ずつ)。nil の Poller は何もしない。
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
	if len(due) == 0 {
		return
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := cached[due[i]].CheckedAt, cached[due[j]].CheckedAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return due[i] < due[j]
	})
	p.refresh(ctx, due[0], cached[due[0]], now)
}

func (p *Poller) refresh(ctx context.Context, key string, prev refstate.Record, now time.Time) {
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
		return
	}
	if _, failed := p.failures[key]; failed {
		delete(p.failures, key)
		p.log.Logf("外部の参照の状態を引けるようになりました: %s", key)
	}
	if err := p.refs.Put(refstate.Record{Ref: key, State: state, CheckedAt: now}); err != nil {
		p.log.Logf("外部の参照の写しの書き込みに失敗: %v", err)
		return
	}
	if refstate.Done(state) && prev.State != state {
		p.log.Logf("%s は %s (依存図で完了にします)", key, state)
	}
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
