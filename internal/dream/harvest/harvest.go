package harvest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/harvest/record"
)

const (
	maxUserPromptLen  = 200
	maxUserPrompts    = 10
	maxFeedbackQuote  = 300
	maxFailedReaction = 200
)

// jsonlEntry はセッション JSONL の 1 行 (最小フィールドセット)。
type jsonlEntry struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// userMessage は entry.Message (user 側)。content は string でも array でも来る。
type userMessage struct {
	Content json.RawMessage `json:"content"`
}

// assistantMessage は entry.Message (assistant 側)。
type assistantMessage struct {
	Content []dream.ContentBlock `json:"content"`
}

// skillInput は Skill tool_use の input フィールド。
type skillInput struct {
	Skill string `json:"skill"`
}

// scanSessionFile は 1 つのセッション JSONL を 1 レコードに集約する。
// content ゼロ・parse エラーのみの空セッションは nil を返す。
func scanSessionFile(path string) (*record.Record, error) {
	f, err := os.Open(path) // #nosec G304 -- CLI: パスは WalkDir 由来
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	state := &scanState{
		rec:        &record.Record{},
		skillsSeen: make(map[string]struct{}),
	}

	scanner := dream.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry jsonlEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		state.applyEntry(&entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	rec := state.rec
	if rec.SessionID == "" {
		rec.SessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if rec.NMessages == 0 {
		return nil, nil
	}

	// deterministic ordering (test snapshotability)
	sortAndDedupStrings(&rec.SkillsInvoked)
	if rec.UserPrompts == nil {
		rec.UserPrompts = []string{}
	}
	if rec.SkillsInvoked == nil {
		rec.SkillsInvoked = []string{}
	}
	if rec.FeedbackSignals == nil {
		rec.FeedbackSignals = []record.FeedbackSignal{}
	}
	if rec.FailedInvocations == nil {
		rec.FailedInvocations = []record.FailedInvocation{}
	}
	return rec, nil
}

// scanState は scanSessionFile が 1 セッションを走査する間に持ち回る mutable 状態。
// entry ごとの分岐を state のメソッドに切り出すことで scanSessionFile 本体の複雑度を下げる。
type scanState struct {
	rec              *record.Record
	skillsSeen       map[string]struct{}
	lastInvokedSkill string
}

// applyEntry は 1 行分の entry を state に取り込む。
func (s *scanState) applyEntry(e *jsonlEntry) {
	s.absorbMetadata(e)
	switch e.Type {
	case "user":
		s.handleUser(e.Message)
	case "assistant":
		s.handleAssistant(e.Message)
	}
}

// absorbMetadata は session_id / cwd / timestamp の集約 (先勝ち・min/max) を行う。
func (s *scanState) absorbMetadata(e *jsonlEntry) {
	if s.rec.SessionID == "" && e.SessionID != "" {
		s.rec.SessionID = e.SessionID
	}
	if s.rec.CWD == "" && e.CWD != "" {
		s.rec.CWD = e.CWD
		s.rec.CWDProject = dream.CanonicalizeProjectPath(e.CWD)
	}
	if e.Timestamp == "" {
		return
	}
	if s.rec.TSStart == "" || e.Timestamp < s.rec.TSStart {
		s.rec.TSStart = e.Timestamp
	}
	if e.Timestamp > s.rec.TSEnd {
		s.rec.TSEnd = e.Timestamp
	}
}

// handleUser は user entry を消化する (prompt 追加・feedback 抽出・failed invocation 判定)。
func (s *scanState) handleUser(raw json.RawMessage) {
	text := extractUserText(raw)
	if text == "" || isMetaUserContent(text) {
		return
	}
	s.rec.NMessages++
	addUserPrompt(s.rec, text)

	fs, ok := detectFeedback(text)
	if !ok {
		return
	}
	s.rec.FeedbackSignals = append(s.rec.FeedbackSignals, fs)
	if s.lastInvokedSkill != "" {
		s.rec.FailedInvocations = append(s.rec.FailedInvocations, record.FailedInvocation{
			InvokedSkill: s.lastInvokedSkill,
			UserReaction: truncate(text, maxFailedReaction),
		})
		s.lastInvokedSkill = ""
	}
}

// handleAssistant は assistant entry を消化する (skill invocation の追跡)。
func (s *scanState) handleAssistant(raw json.RawMessage) {
	s.rec.NMessages++
	invoked := extractSkillInvocations(raw)
	for _, name := range invoked {
		if _, ok := s.skillsSeen[name]; !ok {
			s.skillsSeen[name] = struct{}{}
			s.rec.SkillsInvoked = append(s.rec.SkillsInvoked, name)
		}
		s.lastInvokedSkill = name
	}
}

// addUserPrompt は最大 maxUserPrompts 件、それぞれ maxUserPromptLen で truncate + PII 伏せ字化して追加。
func addUserPrompt(rec *record.Record, text string) {
	if len(rec.UserPrompts) >= maxUserPrompts {
		return
	}
	masked := maskPII(text)
	rec.UserPrompts = append(rec.UserPrompts, truncate(masked, maxUserPromptLen))
}

// extractUserText は user entry.Message から本文テキストを取り出す。
// content が string ならそのまま、array なら type=="text" の text を連結する。
func extractUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var um userMessage
	if err := json.Unmarshal(raw, &um); err != nil {
		return ""
	}
	if len(um.Content) == 0 {
		return ""
	}

	// 文字列 content
	var s string
	if err := json.Unmarshal(um.Content, &s); err == nil {
		return s
	}

	// array content: text ブロックのみ連結 (tool_result は除外)
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(um.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// isMetaUserContent は Claude Code が挿入するメタ user prompt (harvest 対象外) を判定する。
//   - XML 風 tag prefix: <command-name>, <system-reminder> など
//   - skill 本体 / slash command Context ブロック: user prompt として展開されるが user 発話ではない
//     これらは長文かつ「却下」「reject」「Do not use」等を含むため feedback_signals 誤検出の主要因。
func isMetaUserContent(text string) bool {
	trimmed := strings.TrimSpace(text)
	prefixes := []string{
		"<local-command",
		"<command-name>",
		"<user-prompt-submit-hook>",
		"<system-reminder>",
		"<bash-input>",
		"<bash-stdout>",
		"<bash-stderr>",
		// slash command 系 Context ブロック (commit-push-pr, commit, etc.)
		"## Context",
		// skill invocation ヘッダ (Skill tool 呼び出し時に user 側に展開される)
		"Base directory for this skill:",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	// 長文かつ Markdown H1 で始まる text は skill 本体の可能性が高い。
	// user が 4000 rune 超の生 prompt を書くことは稀で、その場合も skill body でない旨
	// 明示すべき (前段の prefix でカバーされる) と判定する。
	if strings.HasPrefix(trimmed, "# ") && utf8.RuneCountInString(trimmed) > metaSkillBodyMinRunes {
		return true
	}
	return false
}

// metaSkillBodyMinRunes は「# で始まる長文 = skill 本体」と判定する閾値。
// 実データ (issue #173) では skill 本体は 5KB〜149KB 程度で、この閾値なら誤除外を避けつつ
// commit skill (~2KB Context) 以外の skill body を捕捉できる。
const metaSkillBodyMinRunes = 4000

// extractSkillInvocations は assistant.message.content[] から Skill tool_use を全て取り出す。
func extractSkillInvocations(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var am assistantMessage
	if err := json.Unmarshal(raw, &am); err != nil {
		return nil
	}
	var out []string
	for _, b := range am.Content {
		if b.Type != "tool_use" || b.Name != "Skill" || len(b.Input) == 0 {
			continue
		}
		var in skillInput
		if err := json.Unmarshal(b.Input, &in); err != nil {
			continue
		}
		if in.Skill != "" {
			out = append(out, in.Skill)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// rune-safe truncate
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func sortAndDedupStrings(ss *[]string) {
	seen := make(map[string]struct{}, len(*ss))
	out := (*ss)[:0]
	for _, s := range *ss {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	*ss = out
}
