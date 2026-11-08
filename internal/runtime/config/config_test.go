package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("テストファイルの書き込みに失敗: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	t.Run("ファイルが存在しない場合は全項目デフォルトを返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist.json")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(cfg, Default()) {
			t.Errorf("got %+v, want default %+v", cfg, Default())
		}
	})

	t.Run("空オブジェクトは全項目デフォルトを返す", func(t *testing.T) {
		path := writeTestFile(t, `{}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(cfg, Default()) {
			t.Errorf("got %+v, want default %+v", cfg, Default())
		}
	})

	t.Run("JSON の構文が壊れている場合は全項目デフォルトを返す", func(t *testing.T) {
		path := writeTestFile(t, `{invalid`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(cfg, Default()) {
			t.Errorf("got %+v, want default %+v", cfg, Default())
		}
	})

	t.Run("有効な値は反映される", func(t *testing.T) {
		path := writeTestFile(t, `{
			"enabled": false,
			"pollIntervalSeconds": 10,
			"marginSeconds": 30,
			"maxRetries": 3,
			"retryMessage": "resume please",
			"readLines": 40,
			"detectionTailLines": 20,
			"handleTransient": false,
			"transientWaitSeconds": 10,
			"transientMaxWaitSeconds": 20,
			"dismissMenu": false,
			"menuDismissDelayMs": 100,
			"submitDelayMs": 200,
			"idleShutdownMinutes": 15,
			"deferToNativeAutoContinue": false
		}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := Config{
			Enabled:                 false,
			PollIntervalSeconds:     10,
			MarginSeconds:           30,
			MaxRetries:              3,
			RetryMessage:            "resume please",
			ReadLines:               40,
			DetectionTailLines:      20,
			HandleTransient:         false,
			TransientWaitSeconds:    10,
			TransientMaxWaitSeconds: 20,
			DismissMenu:             false,
			MenuDismissDelayMs:      100,
			SubmitDelayMs:           200,
			IdleShutdownMinutes:     15,

			DeferToNativeAutoContinue: false,
		}
		if !reflect.DeepEqual(cfg, want) {
			t.Errorf("got %+v, want %+v", cfg, want)
		}
	})

	t.Run("知らないキーは無視され、他のキーはそのまま効く", func(t *testing.T) {
		// 知らないキーで起動が止まったり他のキーが落ちたりしない。
		path := writeTestFile(t, `{"unknownKey": true, "otherUnknown": {"x": 1}, "marginSeconds": 45}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MarginSeconds != 45 {
			t.Errorf("marginSeconds: got %d, want 45", cfg.MarginSeconds)
		}
	})

	t.Run("不正な単一キーはそのキーだけデフォルトへ落ちる", func(t *testing.T) {
		path := writeTestFile(t, `{
			"pollIntervalSeconds": 0,
			"marginSeconds": 45,
			"maxRetries": "not-a-number",
			"readLines": 2,
			"retryMessage": "",
			"detectionTailLines": 0
		}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		def := Default()
		if cfg.PollIntervalSeconds != def.PollIntervalSeconds {
			t.Errorf("pollIntervalSeconds: got %d, want default %d", cfg.PollIntervalSeconds, def.PollIntervalSeconds)
		}
		if cfg.MarginSeconds != 45 {
			t.Errorf("marginSeconds: got %d, want 45 (valid key must survive neighboring bad keys)", cfg.MarginSeconds)
		}
		if cfg.MaxRetries != def.MaxRetries {
			t.Errorf("maxRetries: got %d, want default %d", cfg.MaxRetries, def.MaxRetries)
		}
		if cfg.ReadLines != def.ReadLines {
			t.Errorf("readLines: got %d, want default %d", cfg.ReadLines, def.ReadLines)
		}
		if cfg.RetryMessage != def.RetryMessage {
			t.Errorf("retryMessage: got %q, want default %q", cfg.RetryMessage, def.RetryMessage)
		}
		if cfg.DetectionTailLines != def.DetectionTailLines {
			t.Errorf("detectionTailLines: got %v, want default %v", cfg.DetectionTailLines, def.DetectionTailLines)
		}
	})

	t.Run("旧判定の設定キー (usedPercentageThreshold / stateMaxAgeSeconds / customPatterns / fallbackWaitHours) は知らないキーとして無視する", func(t *testing.T) {
		path := writeTestFile(t, `{"usedPercentageThreshold": 50, "stateMaxAgeSeconds": 120, "customPatterns": ["foo"], "fallbackWaitHours": 2.5, "marginSeconds": 45}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := Default()
		want.MarginSeconds = 45
		if !reflect.DeepEqual(cfg, want) {
			t.Errorf("got %+v, want %+v", cfg, want)
		}
	})

	t.Run("transientMaxWaitSeconds が transientWaitSeconds を下回るなら引き上げる", func(t *testing.T) {
		path := writeTestFile(t, `{"transientWaitSeconds": 100, "transientMaxWaitSeconds": 50}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TransientMaxWaitSeconds != 100 {
			t.Errorf("transientMaxWaitSeconds: got %d, want 100", cfg.TransientMaxWaitSeconds)
		}
	})

	t.Run("readLines の下限未満はデフォルトへ落ちる", func(t *testing.T) {
		path := writeTestFile(t, `{"readLines": 3}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ReadLines != Default().ReadLines {
			t.Errorf("readLines: got %d, want default %d", cfg.ReadLines, Default().ReadLines)
		}
	})
}
