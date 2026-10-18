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
			"customPatterns": ["foo.*bar"],
			"idleShutdownMinutes": 15,
			"deferToNativeAutoContinue": false,
			"homePaths": ["~/src/github.com/usadamasa"],
			"defaultDomain": "home"
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
			CustomPatterns:          []string{"foo.*bar"},
			IdleShutdownMinutes:     15,

			DeferToNativeAutoContinue: false,
			HomePaths:                 []string{"~/src/github.com/usadamasa"},
			DefaultDomain:             "home",
		}
		if !reflect.DeepEqual(cfg, want) {
			t.Errorf("got %+v, want %+v", cfg, want)
		}
	})

	t.Run("旧版の compact / cache のキーは無視され、他のキーはそのまま効く", func(t *testing.T) {
		// 利用者の config.json に残ったキーで起動が止まったり他のキーが落ちたりしない。
		path := writeTestFile(t, `{"compactAutoEnabled": true, "cacheIdleCompactEnabled": false, "marginSeconds": 45}`)
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
		if cfg.ReadLines != def.ReadLines {
			t.Errorf("readLines: got %d, want default %d", cfg.ReadLines, def.ReadLines)
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

func TestLoad_domain(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantPaths   []string
		wantDefault string
	}{
		{"書かなければ私用のパスは無く、既定は業務", `{}`, []string{}, "work"},
		{"私用の端末は defaultDomain を home にする", `{"defaultDomain":"home"}`, []string{}, "home"},
		{"homePaths は書かれたとおり残す", `{"homePaths":["~/src/github.com/usadamasa"]}`, []string{"~/src/github.com/usadamasa"}, "work"},
		{"defaultDomain の typo は業務のまま (私用へ倒さない)", `{"defaultDomain":"personal"}`, []string{}, "work"},
		{"defaultDomain の型違いも業務のまま", `{"defaultDomain":1}`, []string{}, "work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeTestFile(t, tc.content))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(cfg.HomePaths, tc.wantPaths) || cfg.DefaultDomain != tc.wantDefault {
				t.Errorf("HomePaths/DefaultDomain = %v/%q, want %v/%q", cfg.HomePaths, cfg.DefaultDomain, tc.wantPaths, tc.wantDefault)
			}
		})
	}
}
