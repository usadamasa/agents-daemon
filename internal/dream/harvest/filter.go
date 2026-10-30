package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// walkFilter は walkJSONLFiles のコールバックで各ファイルを採用するか判定する。
type walkFilter struct {
	projectGlob string // canonical project name の substring 一致
	incremental bool
	lastRun     string // RFC3339
}

func (f *walkFilter) accept(path, projectsDir string) bool {
	if f.projectGlob != "" {
		project := extractProjectFromPath(path, projectsDir)
		if !strings.Contains(project, f.projectGlob) {
			return false
		}
	}

	if f.incremental && f.lastRun != "" {
		last, err := time.Parse(time.RFC3339, f.lastRun)
		if err != nil {
			return true // state 破損時は fail-open (全走査)
		}
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if info.ModTime().Before(last) {
			return false
		}
	}

	return true
}

// extractProjectFromPath は ~/.claude/projects/-Users-masaru-uchida-workspace/<sid>.jsonl から
// -Users-masaru-uchida-workspace を返す。
func extractProjectFromPath(path, projectsDir string) string {
	rel, err := filepath.Rel(projectsDir, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
