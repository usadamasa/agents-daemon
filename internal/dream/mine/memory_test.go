package mine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMemory は frontmatter + body の memory md を書き出す｡
func writeMemory(t *testing.T, dir, filename, frontmatter, body string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	content := "---\n" + frontmatter + "\n---\n\n" + body
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMemories_ReadsFrontmatterAndBody(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "feedback_a.md",
		"name: feedback-a\ndescription: no PII in standup\nmetadata:\n  type: feedback",
		"# Body\n\n面接候補者の氏名・候補者ID は伏せる｡\n\n**How to apply:** standup で毎回適用｡\n")
	writeMemory(t, dir, "project_b.md",
		"name: project-b\ndescription: Neu role\nmetadata:\n  type: project",
		"内田は Data Layer TL｡\n")
	// MEMORY.md は index なので memory 本体ではない
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# index\n"), 0o644)

	mems, err := loadMemories(dir)
	if err != nil {
		t.Fatalf("loadMemories: %v", err)
	}
	if len(mems) != 2 {
		t.Fatalf("expected 2 memories (MEMORY.md excluded), got %d", len(mems))
	}
	// deterministic by filename
	if mems[0].Name != "feedback-a" || mems[1].Name != "project-b" {
		t.Errorf("unexpected order/names: %v", []string{mems[0].Name, mems[1].Name})
	}
	if mems[0].Type != "feedback" {
		t.Errorf("type = %q; want feedback", mems[0].Type)
	}
	if !strings.Contains(mems[0].Body, "候補者ID は伏せる") {
		t.Errorf("body missing content: %q", mems[0].Body)
	}
}

// memory ディレクトリ不在は「memory が 0 件」ではなく解決ミスの兆候なので、
// 空スライスで握り潰さずエラーにする｡
func TestLoadMemories_MissingDirIsError(t *testing.T) {
	_, err := loadMemories("/nonexistent/dir")
	if err == nil {
		t.Fatal("expected error for missing memory dir, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/dir") {
		t.Errorf("error should name the resolved path, got %v", err)
	}
}

// 存在するが空のディレクトリは正常系 (memory をまだ書いていない project)｡
func TestLoadMemories_EmptyDirIsOK(t *testing.T) {
	mems, err := loadMemories(t.TempDir())
	if err != nil {
		t.Fatalf("empty dir should not error: %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("expected 0 memories, got %d", len(mems))
	}
}

func TestExtractApplicabilityPhrases_HowToApplyLine(t *testing.T) {
	body := `# 概要

前置き｡

**Why:** 過去に事故があった｡

**How to apply:** standup 実行時に必ず PII を伏せる｡ 迷ったら一般化する｡

## 追加ルール

- 面接候補者名は一次面接に一般化する
- ID は伏せる
`
	phrases := extractApplicabilityPhrases(body)
	// 少なくとも「PII を伏せる」「迷ったら一般化」「面接候補者名」「ID は伏せる」的なフレーズが含まれる
	joined := strings.Join(phrases, "|")
	for _, want := range []string{"standup 実行時", "PII", "一般化", "面接候補者名", "ID"} {
		if !strings.Contains(joined, want) {
			t.Errorf("phrases missing keyword %q; got: %v", want, phrases)
		}
	}
	// frontmatter や Why: の直接内容は含まれるが、markdown 装飾は削られる
	for _, unwanted := range []string{"---", "##", "**Why:**"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("phrases should not contain markdown %q; got: %v", unwanted, phrases)
		}
	}
}

func TestSplitFrontmatter(t *testing.T) {
	content := "---\nname: x\ntype: feedback\n---\n\nBody line\nSecond\n"
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fm, "name: x") {
		t.Errorf("frontmatter = %q", fm)
	}
	if strings.TrimSpace(body) != "Body line\nSecond" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	content := "no frontmatter here\njust body\n"
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	if fm != "" {
		t.Errorf("expected empty frontmatter, got %q", fm)
	}
	if body != content {
		t.Errorf("body should equal content when no frontmatter")
	}
}
