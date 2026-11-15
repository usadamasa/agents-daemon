// Package policy は dream が利用者から受け取る規約 (PII の規則、skill 名の接頭辞、走査する skill の置き場、
// 記憶の行き先の規則) を読む。dream は利用者固有の値を持たず、config.json の dream.policy が指す
// 規約のファイルから受ける。ファイルの形は README の「利用者の設定」が宣言する。
//
// キーが無ければ空の規約で動く。キーがあるのに読めない・壊れている・未知のフィールドがあるときはエラーにする。
// 読めた分だけで動くと、仕事用の規約の PII の規則が黙って外れたまま harvest が書き出すため。
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Version は受け付ける規約のファイルの version。
const Version = 1

// PIIRule は harvest が伏せる規則。Replacement は literal として置き換える ($1 などを展開しない)。
type PIIRule struct {
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// Delegate は dream の対象外の話題と、それを担う skill。
type Delegate struct {
	Topic string `json:"topic"`
	Skill string `json:"skill"`
}

// SkillGuide は skill を書くときに従う規範の skill を、対象の範囲ごとに指す。
type SkillGuide struct {
	Scope string `json:"scope"`
	Skill string `json:"skill"`
}

// MemoryHome は memory の行き先の規則。dream skill は references の既定の表より先に当てる。
type MemoryHome struct {
	When string `json:"when"`
	Home string `json:"home"`
}

// Policy は規約のファイルを合わせた結果。配列は Sources の順に連結する。SkillDirs は絶対パスに解決済み。
type Policy struct {
	Sources           []string     `json:"sources,omitempty"`
	PII               []PIIRule    `json:"pii,omitempty"`
	SkillNamePrefixes []string     `json:"skillNamePrefixes,omitempty"`
	SkillDirs         []string     `json:"skillDirs,omitempty"`
	Delegates         []Delegate   `json:"delegates,omitempty"`
	SkillGuides       []SkillGuide `json:"skillGuides,omitempty"`
	MemoryHomes       []MemoryHome `json:"memoryHomes,omitempty"`
}

// file は規約のファイル 1 つの形。
type file struct {
	Version           *int         `json:"version"`
	PII               []PIIRule    `json:"pii"`
	SkillNamePrefixes []string     `json:"skillNamePrefixes"`
	SkillDirs         []string     `json:"skillDirs"`
	Delegates         []Delegate   `json:"delegates"`
	SkillGuides       []SkillGuide `json:"skillGuides"`
	MemoryHomes       []MemoryHome `json:"memoryHomes"`
}

// FromConfig は config.json の dream.policy が指す規約を読んで合わせる。キーが無ければ空の Policy。
func FromConfig(configFile, home string) (Policy, error) {
	paths, err := pathsFromConfig(configFile, home)
	if err != nil {
		return Policy{}, err
	}
	return load(paths, home)
}

// pathsFromConfig は config.json の dream.policy を絶対パスの列にして返す。ファイルかキーが無ければ nil。
// `~/` は home、相対パスは config.json のディレクトリを起点にする。
func pathsFromConfig(configFile, home string) ([]string, error) {
	data, err := os.ReadFile(configFile) // #nosec G304 -- 呼び出し元が apppath の固定パスを渡す
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", configFile, err)
	}
	raw, ok := top["dream"]
	if !ok {
		return nil, nil
	}
	var dreamCfg struct {
		Policy []string `json:"policy"`
	}
	if err := decodeStrict(raw, &dreamCfg); err != nil {
		return nil, fmt.Errorf("%s の dream: %w", configFile, err)
	}
	base := filepath.Dir(configFile)
	paths := make([]string, 0, len(dreamCfg.Policy))
	for _, p := range dreamCfg.Policy {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("%s の dream.policy に空のパスがある", configFile)
		}
		paths = append(paths, resolvePath(p, home, base))
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return paths, nil
}

// load は paths の規約のファイルを順に読んで合わせる。1 つでも読めなければエラー。
func load(paths []string, home string) (Policy, error) {
	var p Policy
	for _, path := range paths {
		f, err := readFile(path)
		if err != nil {
			return Policy{}, fmt.Errorf("規約 %s: %w", path, err)
		}
		p.Sources = append(p.Sources, path)
		p.PII = append(p.PII, f.PII...)
		p.SkillNamePrefixes = append(p.SkillNamePrefixes, f.SkillNamePrefixes...)
		for _, d := range f.SkillDirs {
			p.SkillDirs = append(p.SkillDirs, resolvePath(d, home, filepath.Dir(path)))
		}
		p.Delegates = append(p.Delegates, f.Delegates...)
		p.SkillGuides = append(p.SkillGuides, f.SkillGuides...)
		p.MemoryHomes = append(p.MemoryHomes, f.MemoryHomes...)
	}
	return p, nil
}

func readFile(path string) (file, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 利用者が config.json に書いた規約のパス
	if err != nil {
		return file{}, err
	}
	var f file
	if err := decodeStrict(data, &f); err != nil {
		return file{}, err
	}
	if f.Version == nil || *f.Version != Version {
		return file{}, fmt.Errorf("version は %d にする", Version)
	}
	return f, f.validate()
}

func (f file) validate() error {
	for i, r := range f.PII {
		if r.Name == "" || r.Pattern == "" || r.Replacement == "" {
			return fmt.Errorf("pii[%d]: name・pattern・replacement は空にできない", i)
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("pii[%d] (%s): %w", i, r.Name, err)
		}
	}
	if err := nonEmpty("skillNamePrefixes", f.SkillNamePrefixes); err != nil {
		return err
	}
	if err := nonEmpty("skillDirs", f.SkillDirs); err != nil {
		return err
	}
	for i, d := range f.Delegates {
		if d.Topic == "" || d.Skill == "" {
			return fmt.Errorf("delegates[%d]: topic と skill は空にできない", i)
		}
	}
	for i, g := range f.SkillGuides {
		if g.Scope == "" || g.Skill == "" {
			return fmt.Errorf("skillGuides[%d]: scope と skill は空にできない", i)
		}
	}
	for i, h := range f.MemoryHomes {
		if h.When == "" || h.Home == "" {
			return fmt.Errorf("memoryHomes[%d]: when と home は空にできない", i)
		}
	}
	return nil
}

func nonEmpty(field string, vs []string) error {
	for i, v := range vs {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s[%d] は空にできない", field, i)
		}
	}
	return nil
}

// decodeStrict は未知のフィールドと後続のデータを拒む。綴りを誤ったキーが黙って無視されないようにする。
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON の値の後に余分なデータがある")
	}
	return nil
}

// resolvePath は `~/` を home へ、相対パスを base へ解決する。
func resolvePath(p, home, base string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}
