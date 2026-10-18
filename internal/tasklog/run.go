package tasklog

import (
	"fmt"
	"io"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/domain"
)

// Run は PostToolUse hook の stdin を読み切って記録する (`agents-daemon ingest-task`)。
// domain は config の homePaths と defaultDomain で判定する。config が読めなければ既定 (すべて業務) に
// 倒して記録は続け、読めなかったことはエラーとして返す。
func Run(stdin io.Reader, p apppath.Paths, userHome string, now time.Time) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	cfg, cfgErr := config.Load(p.ConfigFile())
	c := domain.New(userHome, cfg.HomePaths, cfg.DefaultDomain)
	if err := New(p.StateDir()).Ingest(input, Resolver{Domain: c.Classify, Repo: RepoRoot}, now); err != nil {
		return err
	}
	if cfgErr != nil {
		return fmt.Errorf("設定を読めないため業務として記録しました: %w", cfgErr)
	}
	return nil
}
