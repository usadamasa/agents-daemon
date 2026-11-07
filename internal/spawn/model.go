package spawn

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SubagentModelKey は新しいセッションのモデルを持つ settings の env のキー。別セッションへ渡す作業は subagent 用の
// モデルに揃える (peer-sessions skill)。
const SubagentModelKey = "CLAUDE_CODE_SUBAGENT_MODEL"

// SubagentModel は Claude Code の設定ディレクトリの settings.json から env.CLAUDE_CODE_SUBAGENT_MODEL を読む。
// 環境変数ではなくファイルから読むのは、依存図のアプリが launchd の環境で起き、settings の env が子プロセスへ
// 届かないため。読むのはこのキーだけで、ファイルかキーが無ければ空を返す。
func SubagentModel(configDir string) (string, error) {
	path := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(path) // #nosec G304 -- Claude Code の設定ディレクトリの固定のファイル名
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s を読めない: %w", path, err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return "", fmt.Errorf("%s を読めない: %w", path, err)
	}
	return strings.TrimSpace(settings.Env[SubagentModelKey]), nil
}
