package deps

import (
	"sort"

	"github.com/usadamasa/agents-daemon/internal/mention"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
)

// mentioned はタスクの題と辺の note の言及と、言及の鍵 (mention.Key) → daemon が引いた参照。
type mentioned struct {
	found  mention.Found
	refs   map[string]string
	titles map[string]string
}

// mentions は言及を拾う (#474)。repo を省いた形はタスクと端点の cwd の repo で補う (worktree が消えていれば記録の
// repo の鍵)。daemon (refpoll) も同じ mention.Collect で拾って題を引くので、鍵がそろう。
func mentions(in input, l locator) mentioned {
	known := make([]string, 0, len(in.RefStates))
	for r := range in.RefStates {
		known = append(known, r)
	}
	sort.Strings(known)
	found := mention.Collect(mention.Sources{
		Tasks: in.Tasks,
		Edges: in.Edges,
		Works: in.Works,
		Refs:  known,
		GitHub: func(cwd, repo string) string {
			key, _ := l.row(cwd)
			if l.place(cwd).Repo == "" && repo != "" {
				key = repo
			}
			return l.github(key)
		},
	})
	refs := map[string]string{}
	for _, r := range known {
		if it, ok := ref.ParseItem(r); ok {
			refs[mention.Mention{Repo: it.Repo, Number: it.Number}.Key()] = r
		}
	}
	return mentioned{found: found, refs: refs, titles: in.RefTitles}
}

func (m mentioned) node(key string) []protocol.RefMention {
	return m.cards(m.found.Tasks[key])
}

func (m mentioned) edge(id string) []protocol.RefMention {
	return m.cards(m.found.Edges[id])
}

// cards は言及を出力の形にし、引いた参照と題を添える。
func (m mentioned) cards(ms []mention.Mention) []protocol.RefMention {
	if len(ms) == 0 {
		return nil
	}
	out := make([]protocol.RefMention, 0, len(ms))
	for _, x := range ms {
		c := protocol.RefMention{Repo: x.Repo, Number: x.Number}
		if r, ok := m.refs[x.Key()]; ok {
			c.Ref, c.Title = r, m.titles[r]
		}
		out = append(out, c)
	}
	return out
}
