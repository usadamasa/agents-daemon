// Package mention はタスクの題と辺の note に書かれた issue / PR の番号を拾い、owner/repo を解く (#474)。読み手は
// 依存図 (deps) と、題を引く daemon (refpoll) で、どちらも同じ規則で解いて、引いた題と表示の鍵をそろえる。
//
// 拾う形と解き方:
//
//	owner/repo#N、pr:owner/repo#N   書かれた owner/repo
//	repo#N、repo #N (空白 1 つ)     既知の repo 名のときだけその owner/repo。ほかの語 (「PR#392」「PR #392」) は素の #N
//	#N                             題は同じ文の番号の合う参照か cwd の repo、note は models/noterepo の規則
//
// 素の #N の前は ASCII の語の文字・/・#・& でないこと、番号の後は ASCII の語の文字でないこと (gui の links.js と同じ)。
package mention

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// Mention は文面に書かれた issue / PR の番号 1 つ。issue か PR かは文面からは分からない。
type Mention struct {
	Repo   string
	Number int
}

// Key は owner/repo#N を小文字にした鍵。GitHub の owner/repo は大文字小文字を区別しない。
func (m Mention) Key() string {
	return strings.ToLower(m.Repo) + "#" + strconv.Itoa(m.Number)
}

// form は拾った番号の書き方。
type form int

const (
	formExplicit form = iota // owner/repo#N
	formAttached             // repo#N
	formSpaced               // 既知の repo 名 + 空白 1 つ + #N
	formBare                 // #N
)

// token は拾った番号 1 つ。repo は formExplicit なら owner/repo、formAttached と formSpaced なら repo 名。
type token struct {
	form   form
	repo   string
	number int
}

var (
	explicitPrefix = regexp.MustCompile(`^(?:(?:pr|issue):)?([\w-]+/[\w.-]+)$`)
	namePrefix     = regexp.MustCompile(`^[\w.-]+$`)
)

// prefixChar は #N の直前に続けて書く参照の文字 (owner/repo、pr:)。
func prefixChar(c byte) bool {
	return wordChar(c) || c == '.' || c == '-' || c == '/' || c == ':'
}

func wordChar(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func nameChar(c byte) bool {
	return wordChar(c) || c == '.' || c == '-'
}

// scan は text の番号を書かれた順に拾う。
func scan(text string, names map[string][]string) []token {
	var out []token
	for i := 0; i < len(text); i++ {
		if text[i] != '#' {
			continue
		}
		j := i + 1
		for j < len(text) && '0' <= text[j] && text[j] <= '9' {
			j++
		}
		if j == i+1 || text[i+1] == '0' || (j < len(text) && wordChar(text[j])) {
			continue
		}
		number, err := strconv.Atoi(text[i+1 : j])
		if err != nil {
			continue
		}
		if t, ok := classify(text, i, number, names); ok {
			out = append(out, t)
		}
	}
	return out
}

// classify は i の # の前の書き方で番号の形を決める。
func classify(text string, i, number int, names map[string][]string) (token, bool) {
	if i > 0 && (text[i-1] == '#' || text[i-1] == '&') {
		return token{}, false
	}
	k := i
	for k > 0 && prefixChar(text[k-1]) {
		k--
	}
	if prefix := text[k:i]; prefix != "" {
		if m := explicitPrefix.FindStringSubmatch(prefix); m != nil {
			return token{form: formExplicit, repo: m[1], number: number}, true
		}
		if !namePrefix.MatchString(prefix) {
			return token{}, false
		}
		// 既知の repo 名でない語 (「PR#392」) は repo 名と読まず、素の #N にする。引けない参照を引き続けないため。
		if len(names[strings.ToLower(prefix)]) == 1 {
			return token{form: formAttached, repo: prefix, number: number}, true
		}
		return token{form: formBare, number: number}, true
	}
	if i >= 2 && text[i-1] == ' ' {
		w := i - 1
		for w > 0 && nameChar(text[w-1]) {
			w--
		}
		if name := text[w : i-1]; name != "" && (w == 0 || !prefixChar(text[w-1])) && len(names[strings.ToLower(name)]) == 1 {
			return token{form: formSpaced, repo: name, number: number}, true
		}
	}
	return token{form: formBare, number: number}, true
}

// knownNames は repo 名 (小文字) → その名前の owner/repo。
func knownNames(known []string) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, r := range known {
		_, name, ok := strings.Cut(r, "/")
		if !ok || seen[strings.ToLower(r)] {
			continue
		}
		seen[strings.ToLower(r)] = true
		out[strings.ToLower(name)] = append(out[strings.ToLower(name)], r)
	}
	return out
}

// resolver は repo 名の引き方と素の #N の補い方。
type resolver struct {
	names map[string][]string
	// bare は素の #N の owner/repo。named は同じ文の書かれた repo の言及。
	bare func(number int, named []Mention) string
}

func (r resolver) resolve(text string) []Mention {
	tokens := scan(text, r.names)
	named := make([]Mention, 0, len(tokens))
	repoOf := make([]string, len(tokens))
	for i, t := range tokens {
		switch t.form {
		case formExplicit:
			repoOf[i] = t.repo
		case formAttached, formSpaced:
			// classify が既知の repo 名で 1 つに決まる語だけをこの形にする。
			repoOf[i] = r.names[strings.ToLower(t.repo)][0]
		}
		if repoOf[i] != "" {
			named = append(named, Mention{repoOf[i], t.number})
		}
	}
	var out []Mention
	seen := map[string]bool{}
	for i, t := range tokens {
		repo := repoOf[i]
		if t.form == formBare {
			repo = r.bare(t.number, named)
		}
		m := Mention{repo, t.number}
		if repo == "" || seen[m.Key()] {
			continue
		}
		seen[m.Key()] = true
		out = append(out, m)
	}
	return out
}

// sameNumber は番号の合う言及の repo。別の repo が 2 つ以上なら ok は true で repo は空 (決めない)。
func sameNumber(number int, ms []Mention) (repo string, ok bool) {
	repos := map[string]string{}
	for _, m := range ms {
		if m.Number == number {
			repos[strings.ToLower(m.Repo)] = m.Repo
		}
	}
	if len(repos) == 0 {
		return "", false
	}
	if len(repos) > 1 {
		return "", true
	}
	for _, r := range repos {
		return r, true
	}
	return "", false
}

// inText は題 text の言及を書かれた順に重複なく返す。fallback は cwd の owner/repo (分からなければ空)、known は名前で
// 引く owner/repo。素の #N は、同じ文の番号の合う言及の repo、無ければ fallback で補う。
func inText(text, fallback string, known []string) []Mention {
	return resolver{
		names: knownNames(known),
		bare: func(number int, named []Mention) string {
			if repo, ok := sameNumber(number, named); ok {
				return repo
			}
			return fallback
		},
	}.resolve(text)
}

// end は辺の端点 1 つ。Ref は端点の参照、Repo は端点の cwd の owner/repo (分からなければ空)。
type end struct {
	Ref  string
	Repo string
}

// inNote は辺の note の言及を書かれた順に重複なく返す。素の #N は models/noterepo の規則で補う: 番号の合う端点の
// 参照があればその repo (両端が別 repo なら決めない)。無ければ、両端の repo が分かっていて辺が触れる repo が 1 つの
// ときだけそれ。
func inNote(note string, ends [2]end, known []string) []Mention {
	var refs []Mention
	touched := map[string]string{}
	allKnown := true
	for _, e := range ends {
		if it, ok := ref.ParseItem(e.Ref); ok {
			refs = append(refs, Mention{it.Repo, it.Number})
			touched[strings.ToLower(it.Repo)] = it.Repo
		}
		if e.Repo == "" {
			allKnown = false
			continue
		}
		touched[strings.ToLower(e.Repo)] = e.Repo
	}
	return resolver{
		names: knownNames(known),
		bare: func(number int, _ []Mention) string {
			if repo, ok := sameNumber(number, refs); ok {
				return repo
			}
			if !allKnown || len(touched) != 1 {
				return ""
			}
			for _, r := range touched {
				return r
			}
			return ""
		},
	}.resolve(note)
}

// Sources は言及を拾う材料。
type Sources struct {
	// Tasks はタスクの記録。題 (Subject) から拾う。
	Tasks []record.Task
	// Edges は辺の記録。note から拾う。
	Edges []edge.Edge
	// Works はセッションの作業の記録。題からは拾わず、cwd の owner/repo を repo 名で引く owner/repo に足す。
	Works []worklog.Record
	// Refs は daemon が状態を引いた参照 (refstate)。repo 名で引く owner/repo に足す。
	Refs []string
	// GitHub は cwd と記録の repo の鍵から owner/repo を決める。分からなければ空。
	GitHub func(cwd, repo string) string
}

// Found は拾った言及。Tasks はタスクの参照 (task:) → 言及、Edges は辺の id → 言及。言及の無いものは載せない。
type Found struct {
	Tasks map[string][]Mention
	Edges map[string][]Mention
}

// Collect はタスクの題と辺の note の言及を拾う。repo 名で引く owner/repo は、refstate の参照、辺の端点の参照、
// タスクと端点と作業の記録の cwd の owner/repo。
func Collect(s Sources) Found {
	github := func(cwd, repo string) string {
		if s.GitHub == nil {
			return ""
		}
		return s.GitHub(cwd, repo)
	}
	var known []string
	addRef := func(r string) {
		if it, ok := ref.ParseItem(r); ok {
			known = append(known, it.Repo)
		}
	}
	for _, r := range s.Refs {
		addRef(r)
	}
	taskRepos := make([]string, len(s.Tasks))
	for i, t := range s.Tasks {
		taskRepos[i] = github(t.CWD, t.Repo)
		known = append(known, taskRepos[i])
	}
	edgeEnds := make([][2]end, len(s.Edges))
	for i, e := range s.Edges {
		for j, ep := range []edge.Endpoint{e.Before, e.After} {
			addRef(ep.Ref)
			edgeEnds[i][j] = end{Ref: ep.Ref, Repo: github(ep.CWD, ep.Repo)}
			known = append(known, edgeEnds[i][j].Repo)
		}
	}
	for _, w := range s.Works {
		known = append(known, github(w.CWD, w.Repo))
	}
	known = nonEmpty(known)
	f := Found{Tasks: map[string][]Mention{}, Edges: map[string][]Mention{}}
	for i, t := range s.Tasks {
		if ms := inText(t.Subject, taskRepos[i], known); len(ms) > 0 {
			f.Tasks[ref.Task(t.SessionID, t.ID)] = ms
		}
	}
	for i, e := range s.Edges {
		if e.Note == "" {
			continue
		}
		if ms := inNote(e.Note, edgeEnds[i], known); len(ms) > 0 {
			f.Edges[e.ID] = ms
		}
	}
	return f
}

func nonEmpty(ss []string) []string {
	out := ss[:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// All は拾った言及を鍵の順に重複なく返す。
func (f Found) All() []Mention {
	byKey := map[string]Mention{}
	for _, group := range []map[string][]Mention{f.Tasks, f.Edges} {
		for _, ms := range group {
			for _, m := range ms {
				if _, ok := byKey[m.Key()]; !ok {
					byKey[m.Key()] = m
				}
			}
		}
	}
	out := make([]Mention, 0, len(byKey))
	for _, m := range byKey {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
