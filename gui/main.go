// agent-tasks はタスクの依存図をセッションの外で描く macOS のアプリ。root の module とは別に build する
// (Wails が cgo と WebKit の link を要り、SessionStart hook の go build と Linux の CI に載せないため)。
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// frontend/dist だけを embed する。frontend/ 直下は後で npm の package.json と node_modules を持つ。
//
//go:embed all:frontend/dist
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
