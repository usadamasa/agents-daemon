package mine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skill は SKILL.md + references/*.md を連結した「skill 本体全体のテキスト」を持つ。
// grep coverage check は Body に対して substring match で行う。
type skill struct {
	Name     string
	RealPath string // symlink 実体パス (Adopt phase で commit する repo を判定する用)
	Body     string // SKILL.md + references/*.md を join したもの
}

// loadSkill は skillsDir/<name>/ 配下の SKILL.md と references/*.md を読み込む。
// symlink 実体を辿って referenced files も同じ実体側から読み込む。
func loadSkill(skillsDir, name string) (skill, error) {
	skillPath := filepath.Join(skillsDir, name)
	realPath, err := filepath.EvalSymlinks(skillPath)
	if err != nil {
		return skill{}, fmt.Errorf("skill %q not found: %w", name, err)
	}
	skillMd := filepath.Join(realPath, "SKILL.md")
	info, err := os.Stat(skillMd)
	if err != nil {
		return skill{}, fmt.Errorf("SKILL.md not found under %q: %w", realPath, err)
	}
	if info.IsDir() {
		return skill{}, fmt.Errorf("SKILL.md is a directory: %q", skillMd)
	}
	body, err := os.ReadFile(skillMd) // #nosec G304 -- CLI: EvalSymlinks 済みパスの SKILL.md
	if err != nil {
		return skill{}, err
	}

	refs, err := readReferencesDir(filepath.Join(realPath, "references"))
	if err != nil {
		return skill{}, err
	}

	var b strings.Builder
	b.Write(body)
	for _, r := range refs {
		b.WriteString("\n\n")
		b.WriteString(r)
	}

	return skill{
		Name:     name,
		RealPath: realPath,
		Body:     b.String(),
	}, nil
}

// readReferencesDir は references/*.md の中身を deterministic order で返す。
// ディレクトリ未存在は許容 (nil, nil)。
func readReferencesDir(refDir string) ([]string, error) {
	entries, err := os.ReadDir(refDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(refDir, n)) // #nosec G304 -- CLI: 検証済み参照パス
		if err != nil {
			return nil, err
		}
		out = append(out, string(data))
	}
	return out, nil
}
