// Package selfupdate は plugin の更新と、更新した版のソースからの daemon の建て直しを 1 回で行う
// (`agents-daemon update`)。リリースの後に利用者が打つ 3 つのコマンド (marketplace update、plugin update、
// build-daemon.sh) を順に走らせるだけで、版の比較や daemon の入れ替えは build-daemon.sh と daemon に任せる。
//
// cc-premise: 2.1.310 hooks installed_plugins.json の plugins.<id>[0].installPath と claude plugin update の引数
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pluginName は plugin.json の name。install の id は `<name>@<marketplace>`。
const pluginName = "agents-daemon"

// Runner は外部のコマンドを走らせ、終わるまで待つ。出力は利用者の端末へそのまま流す。
type Runner func(name string, args ...string) error

// Options は Update の入力。ConfigDir は Claude Code の設定ディレクトリ ($CLAUDE_CONFIG_DIR、無ければ ~/.claude)。
type Options struct {
	ConfigDir string
	Run       Runner
}

// Update は marketplace と plugin を更新し、更新後の installPath の hooks/build-daemon.sh で daemon を建て直す。
// 段が失敗したら後の段は走らせない。
func Update(o Options) error {
	id, _, err := installed(o.ConfigDir)
	if err != nil {
		return err
	}
	marketplace := strings.TrimPrefix(id, pluginName+"@")
	steps := [][]string{
		{"claude", "plugin", "marketplace", "update", marketplace},
		{"claude", "plugin", "update", id},
	}
	for _, s := range steps {
		if err := o.Run(s[0], s[1:]...); err != nil {
			return fmt.Errorf("%s が失敗: %w", strings.Join(s, " "), err)
		}
	}
	// plugin update で installPath が新しい版のものに変わるので読み直す。
	_, path, err := installed(o.ConfigDir)
	if err != nil {
		return err
	}
	build := filepath.Join(path, "hooks", "build-daemon.sh")
	if err := o.Run(build); err != nil {
		return fmt.Errorf("%s が失敗: %w", build, err)
	}
	return nil
}

// installed は installed_plugins.json から agents-daemon の install の id と installPath を返す。
func installed(configDir string) (id, installPath string, err error) {
	file := filepath.Join(configDir, "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(file) // #nosec G304 -- Claude Code の設定ディレクトリの固定の名前
	if err != nil {
		return "", "", fmt.Errorf("install の記録を読めない: %w", err)
	}
	var doc struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", "", fmt.Errorf("%s を読めない: %w", file, err)
	}
	for key, entries := range doc.Plugins {
		if !strings.HasPrefix(key, pluginName+"@") || len(entries) == 0 {
			continue
		}
		path := entries[0].InstallPath
		if !filepath.IsAbs(path) {
			return "", "", fmt.Errorf("%s の installPath が絶対パスでない: %q", key, path)
		}
		return key, path, nil
	}
	return "", "", fmt.Errorf("%s に %s の install が無い", file, pluginName)
}
