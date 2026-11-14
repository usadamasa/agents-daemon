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
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/refstate"
)

// fetchTimeout は gh の呼び出し 1 回を待つ上限。tick の中で呼ぶので、network が詰まっても pane の監視を
// 長く止めない。
const fetchTimeout = 10 * time.Second

// locateTTL は生きているセッションの cwd の repo と branch を使い回す期間。導く候補の PR がある間は tick ごとに
// 見るので、セッションごとに git を tick ごとに起こさない。branch を切り替えてから導くまでの遅れはこの長さで足りる。
// 写しが済んだ状態の候補は使い回さない (anchor)。
const locateTTL = time.Minute

// Remote は引いた issue / PR 1 つ。State は refstate の語。
type Remote struct {
	State string
	Title string
	// PR は open の PR の段。open でない PR と issue はゼロ値。
	PR refstate.PR
	// HeadBranch は PR の head branch。issue は空。
	HeadBranch string
}

// Fetcher は issue / PR の今の状態と題と段を引く。
type Fetcher func(ctx context.Context, it ref.Item) (Remote, error)

// Session は tick で見えた生きているセッション 1 つ。
type Session struct {
	ID  string
	CWD string
}

// failure は gh が失敗した参照 1 つの、次に引ける時刻とログに書いた理由。
type failure struct {
	retryAt time.Time
	reason  string
}

// located は cwd の GitHub の repo (owner/repo) と branch を引いた結果と時刻。
type located struct {
	repo, branch string
	at           time.Time
}

// Poller は引き直しの状態 (失敗した参照、引いた cwd の branch) を daemon の寿命の間持つ。
type Poller struct {
	edges    edge.Store
	refs     refstate.Store
	fetch    Fetcher
	sessions func() []Session
	// locate は cwd の GitHub の repo (owner/repo) と branch を引く (テストが git を起こさずに差し替える継ぎ目)。
	locate   func(cwd string) (repo, branch string)
	log      *applog.Logger
	failures map[string]failure
	places   map[string]located
}

// New は edges の端点の参照を fetch で引き、refs へ書く Poller を作る。sessions は生きているセッションを返す。
func New(edges edge.Store, refs refstate.Store, fetch Fetcher, sessions func() []Session, log *applog.Logger) *Poller {
	return &Poller{
		edges: edges, refs: refs, fetch: fetch, sessions: sessions, locate: gitLocate, log: log,
		failures: map[string]failure{}, places: map[string]located{},
	}
}

// gitLocate は cwd の origin の GitHub の repo と branch を git で引く。git の外や detached HEAD は空。
func gitLocate(cwd string) (string, string) {
	place := gitrepo.Locate(cwd)
	return gitrepo.GitHubRepo(place.Repo), place.Branch
}

// Refresh は辺の端点の issue / PR のうち引き直す時期のもの (refstate.Due) を 1 つ選んで引き、写しへ書く。
// 1 回に 1 つにして、tick (既定 5 秒) の間隔で gh の呼び出しをばらす。選ぶ順は、まだ引いていないもの、前に引いた
// 時刻の古いもの、参照の順。gh が失敗したら写しを書かず (済んでいない参照を完了と見せない)、その参照は
// RefreshInterval のあいだ引かない。失敗の理由は参照ごとに、変わったときだけログに書く (gh が無いような全部に
// 効く理由は参照の数だけ 1 回ずつ)。引いた後に、PR を出したセッションから PR への辺を導き (anchor)、済んだ参照を
// before に持つ辺を消す (sweep)。導くのは掃除より前 (models/prlink の PRKept)。nil の Poller は何もしない。
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
	p.anchor(edges, cached, now)
	p.sweep(edges, cached)
	p.keepOpen(cached, now)
}

// anchor は渡す側が上流を書いていない PR に、その PR の repo と head branch で動いている生きているセッションから
// PR への辺を導き (#487)、自分の PR を待つ形になった導いた辺を消す。合うセッションが 2 つ以上なら導かない。
// 辺の条件は edge の Anchor が edges/.lock の中で確かめ直す。
func (p *Poller) anchor(edges []edge.Edge, cached map[string]refstate.Record, now time.Time) {
	candidates := p.candidates(edges, cached)
	var live []Session
	if len(candidates) > 0 {
		live = p.sessions()
	}
	for _, pr := range candidates {
		it, _ := ref.ParseItem(pr)
		// 済んだ PR の辺はこの tick の sweep で消え、導く機会はここが最後になる。使い回した branch で照合すると
		// 切り替えた直後のセッションを見落とすので引き直す (#577、models/prlink の PRKept)。
		reread := refstate.Done(cached[pr].State)
		var matched []Session
		for _, s := range live {
			repo, branch := p.place(s.CWD, now, reread)
			if strings.EqualFold(repo, it.Repo) && branch == cached[pr].HeadBranch {
				matched = append(matched, s)
			}
		}
		if len(matched) != 1 {
			continue
		}
		e, ok, err := p.edges.Anchor(matched[0].ID, matched[0].CWD, pr, now)
		if err != nil {
			p.log.Logf("PR を出したセッションの辺を導けません: %s: %v", pr, err)
			continue
		}
		if ok {
			p.log.Logf("辺 %s を導きました: %s → %s (branch %s)", e.ID, e.Before.Ref, e.After.Ref, cached[pr].HeadBranch)
		}
	}
	removed, err := p.edges.Unanchor()
	if err != nil {
		p.log.Logf("自分の PR を待つ形になった導いた辺を消せません: %v", err)
	}
	for _, e := range removed {
		p.log.Logf("辺 %s を消しました: %s → %s (%s が %s を待つ辺と輪になるため)", e.ID, e.Before.Ref, e.After.Ref, e.Before.Ref, e.After.Ref)
	}
}

// candidates は導く候補の PR: 写しが head branch を持ち、辺の before にあり、どの辺の after にも無いもの。
func (p *Poller) candidates(edges []edge.Edge, cached map[string]refstate.Record) []string {
	upstream := map[string]bool{}
	for _, e := range edges {
		upstream[e.After.Ref] = true
	}
	seen := map[string]bool{}
	var prs []string
	for _, e := range edges {
		pr := e.Before.Ref
		if seen[pr] || upstream[pr] || cached[pr].HeadBranch == "" {
			continue
		}
		if it, ok := ref.ParseItem(pr); ok && it.Kind == "pr" {
			seen[pr] = true
			prs = append(prs, pr)
		}
	}
	sort.Strings(prs)
	return prs
}

// place は cwd の GitHub の repo と branch。reread でなければ、locateTTL のあいだは前に引いた値を使う。
func (p *Poller) place(cwd string, now time.Time, reread bool) (string, string) {
	if l, ok := p.places[cwd]; ok && !reread && now.Sub(l.at) < locateTTL {
		return l.repo, l.branch
	}
	repo, branch := p.locate(cwd)
	p.places[cwd] = located{repo: repo, branch: branch, at: now}
	return repo, branch
}

// keepOpen は写しが open の参照を before に持つ辺の保持期間を数え直す。マージを待っている PR の辺を、更新が無いだけで消さない。
func (p *Poller) keepOpen(cached map[string]refstate.Record, now time.Time) {
	open := map[string]bool{}
	for key, rec := range cached {
		open[key] = rec.State == refstate.StateOpen
	}
	if err := p.edges.TouchBefore(open, now); err != nil {
		p.log.Logf("open の参照の辺の mtime を進められません: %v", err)
	}
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
	rec := refstate.Record{Ref: key, State: state, Title: remote.Title, PR: remote.PR, HeadBranch: remote.HeadBranch, CheckedAt: now}
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

// GH は gh で issue / PR の状態と題と段を引く。daemon は Claude Code の sandbox の外で動き、gh の認証 (~/.config/gh) と
// network をそのまま使う。gh は SessionStart の hook から継いだ PATH で探す。
func GH(ctx context.Context, it ref.Item) (Remote, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return Remote{}, fmt.Errorf("gh が PATH に無い: %w", err)
	}
	sub, fields := "issue", "state,stateReason,title"
	if it.Kind == "pr" {
		sub, fields = "pr", "state,title,headRefName,isDraft,statusCheckRollup,reviewDecision,mergeStateStatus,mergeable"
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

// view は gh の view の JSON のうち読む欄。PR の欄は issue には無い。
type view struct {
	State             string  `json:"state"`
	StateReason       string  `json:"stateReason"`
	Title             string  `json:"title"`
	HeadRefName       string  `json:"headRefName"`
	IsDraft           bool    `json:"isDraft"`
	StatusCheckRollup []check `json:"statusCheckRollup"`
	ReviewDecision    string  `json:"reviewDecision"`
	MergeStateStatus  string  `json:"mergeStateStatus"`
	Mergeable         string  `json:"mergeable"`
}

// check は statusCheckRollup の 1 行。CheckRun は WorkflowName、Name、Status、Conclusion を、StatusContext は Context と
// State を持つ。
type check struct {
	WorkflowName string    `json:"workflowName"`
	Name         string    `json:"name"`
	Context      string    `json:"context"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	State        string    `json:"state"`
	StartedAt    time.Time `json:"startedAt"`
}

// decodeView は gh の view の JSON (sub は issue か pr) を状態と題と、open の PR なら段にする。
func decodeView(sub string, out []byte) (Remote, error) {
	var v view
	if err := json.Unmarshal(out, &v); err != nil {
		return Remote{}, fmt.Errorf("gh %s view の出力を読めない: %w", sub, err)
	}
	state, err := ghState(sub, v.State, v.StateReason)
	if err != nil {
		return Remote{}, err
	}
	r := Remote{State: state, Title: v.Title, HeadBranch: v.HeadRefName}
	if sub == "pr" && state == refstate.StateOpen {
		r.PR = refstate.PR{Draft: v.IsDraft, CI: ciOf(v.StatusCheckRollup), Review: reviewOf(v.ReviewDecision), Merge: mergeOf(v.Mergeable, v.MergeStateStatus)}
	}
	return r, nil
}

// ciOf は rollup を 1 語に畳む。check が無ければ空。
func ciOf(checks []check) string {
	ci := ""
	for _, c := range latestChecks(checks) {
		switch checkResult(c) {
		case refstate.CIFailing:
			return refstate.CIFailing
		case refstate.CIRunning:
			ci = refstate.CIRunning
		default:
			if ci == "" {
				ci = refstate.CIPassing
			}
		}
	}
	return ci
}

// latestChecks は同じ check の最新の 1 行だけを残す。rollup は同じ commit の再実行の行も持ち、concurrency で
// 取り消された先の run の CANCELLED が、後の run の成功と並ぶ。開始前の行は startedAt がゼロで、どの行より新しい。
func latestChecks(checks []check) map[[3]string]check {
	latest := map[[3]string]check{}
	for _, c := range checks {
		key := [3]string{c.WorkflowName, c.Name, c.Context}
		if prev, ok := latest[key]; !ok || newerCheck(c, prev) {
			latest[key] = c
		}
	}
	return latest
}

func newerCheck(a, b check) bool {
	return !b.StartedAt.IsZero() && (a.StartedAt.IsZero() || a.StartedAt.After(b.StartedAt))
}

// checkResult は check 1 行を語にする。分類は `gh pr checks` に合わせ、知らない語は通過にしない。
func checkResult(c check) string {
	result := c.State // StatusContext
	if result == "" {
		result = c.Status // CheckRun の実行中
		if c.Status == "COMPLETED" {
			result = c.Conclusion
		}
	}
	switch result {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return refstate.CIPassing
	case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return refstate.CIFailing
	}
	return refstate.CIRunning
}

// reviewOf は reviewDecision を語にする。レビューを必須にしていない repo の PR は空。
func reviewOf(decision string) string {
	switch decision {
	case "REVIEW_REQUIRED":
		return refstate.ReviewRequired
	case "CHANGES_REQUESTED":
		return refstate.ReviewChangesRequested
	case "APPROVED":
		return refstate.ReviewApproved
	}
	return ""
}

// mergeOf はマージの段を語にする。衝突は Draft でも分かる mergeable で見る。CLEAN のほか (計算中の UNKNOWN、BLOCKED、
// UNSTABLE) はマージ可と言わない。
func mergeOf(mergeable, status string) string {
	switch {
	case mergeable == "CONFLICTING":
		return refstate.MergeConflicting
	case status == "BEHIND":
		return refstate.MergeBehind
	case status == "CLEAN":
		return refstate.MergeClean
	}
	return ""
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
