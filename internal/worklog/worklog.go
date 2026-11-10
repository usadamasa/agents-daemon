// Package worklog はセッション 1 つの作業の記録 (work/<session_id>.json) を、hook の入力と人・companion の書き換えから
// XDG state 配下に書く (#368)。model が Task 系ツールを打つかどうかに関わらず、prompt を打ったセッションの作業が依存図に
// 作業の単位として載り、辺の端点 (sid:<session_id>) にもなれるようにする。読み手は依存図 (deps)。
//
//	work/<session_id>.json   セッション 1 つの作業 (subject と出どころ、companion の推定の題、最後のターンの要約、turns、
//	                         最後の prompt と Stop の時刻、始まりと終わり)
//	work/.lock               hook、`work set`、prune を直列にするロック
//
// 書くのは hook (SessionStart / UserPromptSubmit / Stop / SessionEnd が `agents-daemon ingest-work` に渡す)、人とアプリと
// companion の `work set`、daemon の housekeeping (Prune)。どれも lock の中で読んでから自分の欄だけを書き換える。
// lock が無いと、ターンの途中に入った peer の delivery の UserPromptSubmit と Stop が同じ写しを書いて turns を失い、遅れた
// hook が人の直した subject や SessionEnd の ended_at を戻す (models/work の WorkRace*.cfg の反例)。
//
// 記録を作るのは UserPromptSubmit と Stop だけで、SessionStart と SessionEnd は記録があるときだけ書き換える。prompt を
// 打たずに終わったセッション (起動して /exit、/clear の直後) の記録を残さないため。
//
// 題の出どころ (Origin) は prompt (最初の prompt の 1 行目) か human (人が `work set` で直した)。companion の推定の題は
// 別の欄 (Inferred) に置き、subject を上書きしない。見せる題 (Title) は human の subject、推定の題、prompt の subject の順。
package worklog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention はセッションの終了後に記録を残す期間。daemon が pane の見えているセッションの mtime を tick ごとに進める
// (Touch) ので、mtime からの経過は pane が見えなくなってからの経過になる。
// 終了したセッションの作業を未完了のまま残す (パーキングへ移すか外すかを決める) 期間で、終了したセッションの未完了の
// タスク (tasklog.Retention) と揃える。計測 (#368) では 7 日に 117 のセッションがタスク無しで終わるので、同じ数の
// 終了したノードが依存図に残る。多すぎればここを縮める (アプリの「終了したセッションの作業を外す」でもまとめて消せる)。
const Retention = 7 * 24 * time.Hour

// 題の出どころ。
const (
	OriginPrompt = "prompt" // 最初の prompt の 1 行目
	OriginHuman  = "human"  // 人が `work set` で直した
)

// subjectLimit は prompt から取る題の長さ (rune)。超えたら … を付けて切る。
const subjectLimit = 120

// lockFile は work/ で書き込みを直列にするロック。
const lockFile = ".lock"

// Record はセッション 1 つの作業の記録。ファイルの形は protocol の schema (Work) が持ち、`work get` / `work set` の出力でもある。
// Subject は題 (事実か人の手) で Origin がその出どころ、Inferred は companion の推定の題 (subject とは別の欄)、Summary は
// 最後のターンの応答の 1 行目、Turns は UserPromptSubmit の数 (peer の delivery も数える)、PromptedAt と StoppedAt は
// 最後の UserPromptSubmit と Stop の時刻、EndedAt は SessionEnd の時刻 (生きている間は無い)。
type Record = protocol.Work

// Title は見せる題。人が直した subject、companion の推定の題、prompt の subject の順。無ければ空。
func Title(r Record) string {
	if r.Origin == OriginHuman {
		return r.Subject
	}
	if r.Inferred != "" {
		return r.Inferred
	}
	return r.Subject
}

// Ended はセッションが終了した (SessionEnd の後、resume の前) か。
func Ended(r Record) bool {
	return r.EndedAt != nil
}

// Dismiss は終了したセッションの作業の記録を消す (依存図の Remove)。exited はセッションが記録の時刻より後に
// 終了したと確かめる (daemonstate.SessionExited)。確かめられなければ消さない (生きているセッションの記録を消すと、
// 次の hook まで依存図から落ちる)。
func (s Store) Dismiss(sessionID string, exited func(sessionID string, after time.Time) error) error {
	rec, ok := s.Get(sessionID)
	if !ok {
		return fmt.Errorf("セッション %s の作業の記録が無い", sessionID)
	}
	if err := exited(sessionID, rec.UpdatedAt); err != nil {
		return fmt.Errorf("セッション %s の作業は消さない: %w", sessionID, err)
	}
	return s.Remove(sessionID)
}

// Change は `work set` で書き換える欄。nil の欄は触らない。
type Change struct {
	// Subject は人が直した題。出どころは human になる。空白だけはエラー。
	Subject *string
	// Inferred は companion の推定の題。
	Inferred *string
}

// Result は 1 回の Ingest で何をしたか。出来事のログに出す。
type Result struct {
	Event     string
	SessionID string
	// Created は記録を新しく作ったか、Written は何かを書いたか、Ended は SessionEnd で ended_at を書いたか。
	Created, Written, Ended bool
}

// hookInput は hook の stdin のうち、記録に使う欄。
// cc-premise: 2.1.319 hooks SessionStart の source、UserPromptSubmit の prompt、Stop の last_assistant_message、SessionEnd の reason
type hookInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	Source        string `json:"source"`
	Prompt        string `json:"prompt"`
	Message       string `json:"last_assistant_message"`
	Reason        string `json:"reason"`
}

// Store は work/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "work")}
}

// Ingest は hook の stdin を読み、event に応じて記録を書く。
func (s Store) Ingest(input []byte, now time.Time) (Result, error) {
	var in hookInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{}, fmt.Errorf("hook の入力のパースに失敗: %w", err)
	}
	res := Result{Event: in.HookEventName, SessionID: in.SessionID}
	switch in.HookEventName {
	case "SessionStart", "UserPromptSubmit", "Stop", "SessionEnd":
	default:
		return res, fmt.Errorf("扱わない hook の入力 (%q)", in.HookEventName)
	}
	err := s.modify(in.SessionID, func(rec *Record, found bool) bool {
		switch in.HookEventName {
		case "SessionStart":
			// cc-premise: 2.1.319 hooks resume の SessionStart は同じ session_id で source が resume
			if !found || in.Source != "resume" {
				return false
			}
			rec.EndedAt, rec.EndReason = nil, ""
		case "UserPromptSubmit":
			res.Created = !found
			rec.Turns++
			at := now
			rec.PromptedAt = &at
			if rec.Subject == "" {
				if subject := subjectOf(in.Prompt); subject != "" {
					rec.Subject, rec.Origin = subject, OriginPrompt
				}
			}
		case "Stop":
			res.Created = !found
			rec.Summary = firstLine(in.Message, subjectLimit)
			at := now
			rec.StoppedAt = &at
		case "SessionEnd":
			if !found {
				return false
			}
			at := now
			rec.EndedAt, rec.EndReason = &at, in.Reason
			res.Ended = true
		}
		if in.CWD != "" {
			rec.CWD = in.CWD
		}
		return true
	}, now)
	res.Written = err == nil && (res.Created || res.Ended || in.HookEventName == "UserPromptSubmit" || in.HookEventName == "Stop" || resumed(in))
	return res, err
}

// resumed は Written の判定のうち、SessionStart が記録を書き換えた形 (resume) か。
func resumed(in hookInput) bool {
	return in.HookEventName == "SessionStart" && in.Source == "resume"
}

// Set は人かアプリか companion の書き換え。記録が無ければエラー。
func (s Store) Set(sessionID string, change Change, now time.Time) (Record, error) {
	if change.Subject == nil && change.Inferred == nil {
		return Record{}, errors.New("書き換える欄が無い")
	}
	if change.Subject != nil && strings.TrimSpace(*change.Subject) == "" {
		return Record{}, errors.New("subject が空")
	}
	var out Record
	err := s.modify(sessionID, func(rec *Record, found bool) bool {
		if !found {
			return false
		}
		if change.Subject != nil {
			rec.Subject, rec.Origin = strings.TrimSpace(*change.Subject), OriginHuman
		}
		if change.Inferred != nil {
			rec.Inferred = strings.TrimSpace(*change.Inferred)
		}
		out = *rec
		return true
	}, now)
	if err != nil {
		return Record{}, err
	}
	if out.SessionID == "" {
		return Record{}, fmt.Errorf("セッション %s の作業の記録が無い", sessionID)
	}
	return out, nil
}

// modify は lock の中で記録を読み、fn が true を返したら書く。fn には記録 (無ければ now で始まる空の記録) と、あったかを渡す。
func (s Store) modify(sessionID string, fn func(rec *Record, found bool) bool, now time.Time) error {
	if s.Dir == "" {
		return errors.New("work の置き場所が無い")
	}
	path, err := statefile.SessionPath(s.Dir, sessionID, ".json")
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("session_id が無い")
	}
	return s.locked(func() error {
		rec := Record{SessionID: sessionID, StartedAt: now}
		found, err := statefile.ReadJSON(path, &rec)
		if err != nil {
			// 壊れた記録は作り直す (読み手は読み飛ばすので、残しても依存図には出ない)。
			rec, found = Record{SessionID: sessionID, StartedAt: now}, false
		}
		if !fn(&rec, found) {
			return nil
		}
		rec.SessionID = sessionID
		rec.UpdatedAt = now
		return statefile.WriteJSONAtomic(path, ".work.*", rec)
	})
}

// Get はセッションの記録を返す。無い、壊れている、ゼロ値の Store なら false。
func (s Store) Get(sessionID string) (Record, bool) {
	if s.Dir == "" {
		return Record{}, false
	}
	path, err := statefile.SessionPath(s.Dir, sessionID, ".json")
	if err != nil || path == "" {
		return Record{}, false
	}
	return read(path)
}

// List は全セッションの記録を session ID の順に返す。壊れたファイルと書きかけの一時ファイル、lock は読み飛ばす。
func (s Store) List() ([]Record, error) {
	if s.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("work の走査に失敗: %w", err)
	}
	var out []Record
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name[0] == '.' || filepath.Ext(name) != ".json" {
			continue
		}
		if rec, ok := read(filepath.Join(s.Dir, name)); ok {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

// Remove はセッションの記録を消す。無ければ何もしない。
func (s Store) Remove(sessionID string) error {
	if s.Dir == "" {
		return errors.New("work の置き場所が無い")
	}
	path, err := statefile.SessionPath(s.Dir, sessionID, ".json")
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("session_id が無い")
	}
	return s.locked(func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("作業の記録を消せない: %w", err)
		}
		return nil
	})
}

// Touch は生きているセッションの記録の mtime を now に進める。中身は変えず (依存図は UpdatedAt で並べる)、無い記録は
// 作らない。ファイル名に使えない ID は飛ばして残りを進め、エラーにまとめて返す。
func (s Store) Touch(sessionIDs []string, now time.Time) error {
	if s.Dir == "" {
		return nil
	}
	var errs []error
	for _, id := range sessionIDs {
		path, err := statefile.SessionPath(s.Dir, id, ".json")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if path == "" {
			continue
		}
		if err := os.Chtimes(path, now, now); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Prune は Retention のあいだ更新されなかった記録 (と壊れたファイル、消し損ねた一時ファイル) を mtime で消し、消した数を
// 返す。lock は消さない。
func (s Store) Prune(now time.Time) (int, error) {
	if s.Dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("work の走査に失敗: %w", err)
	}
	cutoff := now.Add(-Retention)
	removed := 0
	var errs []error
	for _, e := range entries {
		if e.IsDir() || e.Name() == lockFile {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir, e.Name())); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// locked は work/ を作り、ロックを取って fn を呼ぶ。
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return fmt.Errorf("work の作成に失敗: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- work/ 配下の固定のパス
	if err != nil {
		return fmt.Errorf("ロックを開けない: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("ロックを取れない: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func read(path string) (Record, bool) {
	var rec Record
	found, err := statefile.ReadJSON(path, &rec)
	if err != nil || !found || rec.SessionID == "" {
		return Record{}, false
	}
	return rec, true
}

// wrapperPattern は peer の delivery の prompt を包む cross-session-message のタグ。本文はタグの間の行。
// cc-premise: 2.1.319 peer-messaging delivery の prompt は <cross-session-message …>\n本文\n</cross-session-message> で、
// transcript にある「Another Claude session sent a message:」の前置きは付かない
var wrapperPattern = regexp.MustCompile(`(?s)^\s*<cross-session-message[^>]*>\s*(.*?)\s*</cross-session-message>\s*$`)

// subjectOf は prompt から題を取る: 包みを剥き、最初の空でない行を空白を詰めて subjectLimit で切る。無ければ空。
func subjectOf(prompt string) string {
	if m := wrapperPattern.FindStringSubmatch(prompt); m != nil {
		prompt = m[1]
	}
	return firstLine(prompt, subjectLimit)
}

// firstLine は text の最初の空でない行を、空白を 1 つに詰めて limit (rune) で切ったもの。超えたら … を付ける。
func firstLine(text string, limit int) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > limit {
			return string(r[:limit]) + "…"
		}
		return line
	}
	return ""
}
