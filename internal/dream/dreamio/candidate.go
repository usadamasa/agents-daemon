// Package dreamio は dream 7-phase pipeline の Mine 出力 (candidates.jsonl) を
// mine/stage/adopt サブコマンドで共有するための型と I/O を提供する。
//
// candidates.jsonl は 1 行 1 レコードで、各行は Candidate の JSON エンコード。
package dreamio

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Candidate.Kind の値。mine が分類し、stage / adopt が分岐に使う。
const (
	KindSkillBody   = "skill_body"   // memory 内容を skill 本体へ反映する候補
	KindMemoryDedup = "memory_dedup" // skill 本体に反映済みで memory を削除する候補
	KindUnhomed     = "unhomed"      // association 先 skill が無く行き先未定の候補
)

// Candidate.Confidence の値。KindUnhomed は coverage 判定を持たないため ConfidenceUnknown になる。
const (
	ConfidenceHigh    = "high"
	ConfidenceMedium  = "medium"
	ConfidenceLow     = "low"
	ConfidenceUnknown = "unknown"
)

// Candidate は Mine 出力 1 件を表す。mine/stage/adopt の 3 サブコマンドで共有する。
//
// SkillPath のみ omitempty (memory_dedup 候補では skill 実体が無い場合があるため)。
type Candidate struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`             // Kind* 定数のいずれか
	AssociatedSkill  string   `json:"associated_skill"` // 関連 skill 名 (Mine が推定。KindUnhomed では空)
	MemoryPath       string   `json:"memory_path"`      // 対象 memory 実体パス
	SkillPath        string   `json:"skill_path,omitempty"`
	ScopedToProject  string   `json:"scoped_to_project"`
	Diagnosis        string   `json:"diagnosis"`
	EvidenceSessions []string `json:"evidence_sessions"`
	Confidence       string   `json:"confidence"` // Confidence* 定数のいずれか
	CoverageRatio    float64  `json:"coverage_ratio"`
}

// CandidatesFilename は Mine 出力 / Stage 入力 / Adopt 入力で共通の
// "<project-slug>-YYYY-MM-DD.jsonl" 形式ファイル名を返す。
//
// project canonical name の先頭 - を除去した残りを stub として使う。
// 例: project="-Users-x-workspace", date="2026-07-20"
//
//	→ "Users-x-workspace-2026-07-20.jsonl"
func CandidatesFilename(project, date string) string {
	stub := strings.TrimPrefix(project, "-")
	return fmt.Sprintf("%s-%s.jsonl", stub, date)
}

// CandidatesPath は state ディレクトリ / project / date から candidates.jsonl の絶対パスを組み立てる。
// mine が書き stage/adopt が読む共通のパス規約。
func CandidatesPath(stateDir, project, date string) string {
	return filepath.Join(stateDir, "candidates", CandidatesFilename(project, date))
}
