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
			"fallbackWaitHours": 2.5,
			"maxRetries": 3,
			"retryMessage": "resume please",
			"readSource": "visible",
			"readLines": 40,
			"detectionTailLines": 20,
			"usedPercentageThreshold": 50,
			"stateMaxAgeSeconds": 120,
			"handleTransient": false,
			"transientWaitSeconds": 10,
			"transientMaxWaitSeconds": 20,
			"dismissMenu": false,
			"menuDismissDelayMs": 100,
			"submitDelayMs": 200,
			"engagedLabel": "waiting",
			"customPatterns": ["foo.*bar"],
			"customTransientPatterns": ["baz.*qux"],
			"idleShutdownMinutes": 15,
			"deferToNativeAutoContinue": false,
			"compactStallEnabled": false,
			"compactStallQuietSeconds": 300,
			"compactStallCooldownMinutes": 20,
			"compactStallMaxNudges": 5,
			"compactStallMessage": "keep going",
			"compactAutoEnabled": true,
			"compactAutoThresholdPercent": 70,
			"compactAutoPrepMessage": "/prep",
			"compactAutoMessage": "/compact now",
			"compactAutoPrepTimeoutMinutes": 3,
			"compactAutoCooldownMinutes": 30,
			"compactResumeEnabled": false,
			"compactResumeDelaySeconds": 90
		}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := Config{
			Enabled:                 false,
			PollIntervalSeconds:     10,
			MarginSeconds:           30,
			FallbackWaitHours:       2.5,
			MaxRetries:              3,
			RetryMessage:            "resume please",
			ReadSource:              "visible",
			ReadLines:               40,
			DetectionTailLines:      20,
			UsedPercentageThreshold: 50,
			StateMaxAgeSeconds:      120,
			HandleTransient:         false,
			TransientWaitSeconds:    10,
			TransientMaxWaitSeconds: 20,
			DismissMenu:             false,
			MenuDismissDelayMs:      100,
			SubmitDelayMs:           200,
			EngagedLabel:            "waiting",
			CustomPatterns:          []string{"foo.*bar"},
			CustomTransientPatterns: []string{"baz.*qux"},
			IdleShutdownMinutes:     15,

			DeferToNativeAutoContinue:   false,
			CompactStallEnabled:         false,
			CompactStallQuietSeconds:    300,
			CompactStallCooldownMinutes: 20,
			CompactStallMaxNudges:       5,
			CompactStallMessage:         "keep going",

			CompactAutoEnabled:            true,
			CompactAutoThresholdPercent:   70,
			CompactAutoPrepMessage:        "/prep",
			CompactAutoMessage:            "/compact now",
			CompactAutoPrepTimeoutMinutes: 3,
			CompactAutoCooldownMinutes:    30,
			CompactResumeEnabled:          false,
			CompactResumeDelaySeconds:     90,
		}
		if !reflect.DeepEqual(cfg, want) {
			t.Errorf("got %+v, want %+v", cfg, want)
		}
	})

	t.Run("compactStallQuietSeconds が短すぎる値なら既定へ落ちる", func(t *testing.T) {
		// 数秒で突くと compact 直後に考え始めた Claude へ割り込む｡
		path := writeTestFile(t, `{"compactStallQuietSeconds": 3}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CompactStallQuietSeconds != Default().CompactStallQuietSeconds {
			t.Errorf("CompactStallQuietSeconds = %d, want %d", cfg.CompactStallQuietSeconds, Default().CompactStallQuietSeconds)
		}
	})

	t.Run("不正な単一キーはそのキーだけデフォルトへ落ちる", func(t *testing.T) {
		path := writeTestFile(t, `{
			"pollIntervalSeconds": 0,
			"marginSeconds": 45,
			"maxRetries": "not-a-number",
			"readSource": "carrier-pigeon",
			"retryMessage": "",
			"usedPercentageThreshold": 150
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
		if cfg.ReadSource != def.ReadSource {
			t.Errorf("readSource: got %q, want default %q", cfg.ReadSource, def.ReadSource)
		}
		if cfg.RetryMessage != def.RetryMessage {
			t.Errorf("retryMessage: got %q, want default %q", cfg.RetryMessage, def.RetryMessage)
		}
		if cfg.UsedPercentageThreshold != def.UsedPercentageThreshold {
			t.Errorf("usedPercentageThreshold: got %v, want default %v", cfg.UsedPercentageThreshold, def.UsedPercentageThreshold)
		}
	})

	t.Run("customPatterns の不正な正規表現だけ除外する", func(t *testing.T) {
		path := writeTestFile(t, `{"customPatterns": ["valid.*", "["]}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"valid.*"}
		if !reflect.DeepEqual(cfg.CustomPatterns, want) {
			t.Errorf("got %v, want %v", cfg.CustomPatterns, want)
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
