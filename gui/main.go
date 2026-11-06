// agent-tasks はタスクの依存図をセッションの外で描く macOS のアプリ。root の module とは別に build する
// (Wails が cgo と WebKit の link を要り、SessionStart hook の go build と Linux の CI に載せないため)。
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// frontend/src をそのまま embed する (bundler は使わない)。配置の library は src/vendor に写してあり、build に npm は要らない
// (SessionStart hook が go build だけで建てられるように)。frontend/ 直下の package.json と node_modules はテストと vendor の
// 更新 (task gui:vendor) のためだけにある。
//
//go:embed all:frontend/src
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name: "agent-tasks",
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Services: []application.Service{
			application.NewService(newDaemon()),
		},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "agent-tasks",
		Width:  1280,
		Height: 820,
		URL:    "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
