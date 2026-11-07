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

// version はこのバイナリを建てた plugin の version。hooks/build-gui.sh が -ldflags -X で入れる
// (task gui:build で建てたものは dev のまま)。窓の title と About に出し、開いている窓がどの version かを見分ける。
var version = "dev"

func main() {
	app := application.New(application.Options{
		Name: "agent-tasks",
		// アプリのメニューの About は Info.plist ではなく Name と Description を出す。
		Description: "version " + version,
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
		Title:  "agent-tasks " + version,
		Width:  1280,
		Height: 820,
		URL:    "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
