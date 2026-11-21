package refpoll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/mention"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/refstate"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// KindFetcher は番号が issue か PR か (ref の種類の語 issue / pr) を引く。
type KindFetcher func(ctx context.Context, repo string, number int) (string, error)

// Mentions はタスクの題と辺の note に書かれた番号 (言及) の題も引くようにする (#474)。tasks はタスクの記録を、works は
// 作業の記録 (repo 名で引く repo を足す) を返し、kind は言及の種類を引く。言及は文面から issue か PR か分からないので、
// 種類を決めてから Fetcher で引き、その種類の写しに書く。nil の Poller は何もしない。
func (p *Poller) Mentions(tasks func() ([]record.Task, error), works func() ([]worklog.Record, error), kind KindFetcher) {
	if p == nil {
		return
	}
	p.tasks, p.works, p.kind = tasks, works, kind
}

// listMentions は言及を鍵の順に返す。拾い方は依存図 (deps) と同じ mention.Collect で、引いた題と表示の鍵をそろえる。
// cwd の owner/repo は記録の repo の鍵 (worktree が消えても残る main worktree のパス) を先に使い、パスごとに 1 回だけ
// 引く (remote は daemon の寿命の間変わらないとみなす)。鍵が無ければ cwd を place で引く。Mentions を渡していないか、
// タスクの記録を読めなければ空。
func (p *Poller) listMentions(edges []edge.Edge, cached map[string]refstate.Record, now time.Time) []mention.Mention {
	if p.tasks == nil || p.works == nil || p.kind == nil {
		return nil
	}
	tasks, err := p.tasks()
	if err != nil {
		p.log.Logf("タスクの記録の読み込みに失敗、題の番号を引きません: %v", err)
		return nil
	}
	works, err := p.works()
	if err != nil {
		p.log.Logf("作業の記録の読み込みに失敗、題の番号を引きません: %v", err)
		return nil
	}
	refs := make([]string, 0, len(cached))
	for r := range cached {
		refs = append(refs, r)
	}
	return mention.Collect(mention.Sources{
		Tasks: tasks,
		Edges: edges,
		Works: works,
		Refs:  refs,
		GitHub: func(cwd, repo string) string {
			if repo == "" {
				r, _ := p.place(cwd, now, false)
				return r
			}
			if r, ok := p.repos[repo]; ok {
				return r
			}
			// 引けなかった repo は使い回さない (remote を足した後に引き直す)。
			r := p.gitHub(repo)
			if r != "" {
				p.repos[repo] = r
			}
			return r
		},
	}).All()
}

// dueMention は引く言及を 1 つ選ぶ。写しのある番号 (種類と大文字小文字を問わない) と、失敗して間隔の過ぎていない
// 言及は選ばない。言及だけの参照は題のために引くので、写しがあれば open でも引き直さない。写しは refstate の
// Retention で消え、まだ言及があればそのとき引き直す。
func (p *Poller) dueMention(mentions []mention.Mention, cached map[string]refstate.Record, now time.Time) (mention.Mention, bool) {
	have := map[string]bool{}
	for r := range cached {
		if it, ok := ref.ParseItem(r); ok {
			have[mention.Mention{Repo: it.Repo, Number: it.Number}.Key()] = true
		}
	}
	for _, m := range mentions {
		if !have[m.Key()] && !now.Before(p.failures[m.Key()].retryAt) {
			return m, true
		}
	}
	return mention.Mention{}, false
}

// refreshMention は言及 1 つの種類を引き、その種類の参照として引いて写しへ書く。どちらかが失敗したら言及の鍵で
// 失敗を残し、RefreshInterval のあいだ引かない。
func (p *Poller) refreshMention(ctx context.Context, m mention.Mention, now time.Time) (refstate.Record, bool) {
	kindCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	kind, err := p.kind(kindCtx, m.Repo, m.Number)
	cancel()
	key := m.Key()
	if err == nil {
		r := kind + ":" + m.Repo + "#" + strconv.Itoa(m.Number)
		if prev, failed := p.failures[key]; failed {
			// 同じ理由の失敗をログに書き直さないよう、言及の鍵の失敗を参照の鍵へ写してから引く。
			p.failures[r] = prev
		}
		if _, ok := ref.ParseItem(r); !ok {
			err = fmt.Errorf("%s の種類 %q を知らない", key, kind)
		} else if rec, ok := p.refresh(ctx, r, refstate.Record{}, false, now); ok {
			delete(p.failures, key)
			return rec, true
		} else {
			p.failures[key] = failure{retryAt: now.Add(refstate.RefreshInterval), reason: p.failures[r].reason}
			return refstate.Record{}, false
		}
	}
	reason := err.Error()
	if reason != p.failures[key].reason {
		p.log.Logf("言及の番号の種類を引けません: %s: %s", key, reason)
	}
	p.failures[key] = failure{retryAt: now.Add(refstate.RefreshInterval), reason: reason}
	return refstate.Record{}, false
}

// GHKind は gh issue view の url で番号が issue か PR かを引く。gh issue view は PR の番号でも PR を返す。
func GHKind(ctx context.Context, repo string, number int) (string, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return "", fmt.Errorf("gh が PATH に無い: %w", err)
	}
	if _, ok := ref.ParseItem("issue:" + repo + "#" + strconv.Itoa(number)); !ok {
		return "", fmt.Errorf("%s#%d は issue / PR の番号でない", repo, number)
	}
	cmd := exec.CommandContext(ctx, bin, "issue", "view", strconv.Itoa(number), "--repo", repo, "--json", "url") // #nosec G204 -- 固定の gh のサブコマンドと、ref.ParseItem で検証した repo と番号
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			if line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); line != "" {
				return "", fmt.Errorf("gh issue view が失敗: %s", line)
			}
		}
		return "", fmt.Errorf("gh issue view が失敗: %w", err)
	}
	var v struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("gh issue view の出力を読めない: %w", err)
	}
	return kindOfURL(v.URL), nil
}

// kindOfURL は issue / PR の URL の種類。PR は /pull/ を含む。
func kindOfURL(url string) string {
	if strings.Contains(url, "/pull/") {
		return "pr"
	}
	return "issue"
}
