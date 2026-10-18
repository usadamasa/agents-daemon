package adopt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findRepoRoot は path が属する git repo のトップレベルを返す。
// symlink 実体をたどってから git rev-parse するため、~/.claude/skills → dotfiles repo などの
// 場合でも正しい実体 repo が返る。
func findRepoRoot(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve symlink for %q: %w", path, err)
	}
	dir := resolved
	// path がファイルなら dir を parent に変える
	info, err := os.Stat(resolved)
	if err == nil && !info.IsDir() {
		dir = filepath.Dir(resolved)
	}
	// #nosec G204 -- dir は EvalSymlinks で正規化済みのファイルシステムパス。
	// user 直接入力ではなく、Mine 出力の skill_path/memory_path 由来。
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed under %q: %w (%s)", dir, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// branchName は Adopt 用作業ブランチ名を組み立てる。
// 形式: dream/YYYY-MM-DD-<candidate-suffix>
// candidate ID の "dream-candidate-" prefix があれば剥がす。
func branchName(date, candidateID string) string {
	slug := strings.TrimPrefix(candidateID, "dream-candidate-")
	return fmt.Sprintf("dream/%s-%s", date, slug)
}
