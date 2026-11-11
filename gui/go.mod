module github.com/usadamasa/agents-daemon/gui

// net/http と cgo のビルド時の脆弱性は go1.27.4 で修正された。toolchain 行は GOTOOLCHAIN=local で効かないので、
// go 行を下限にして利用者の手元のビルドも修正版に限る
go 1.27.4

require github.com/wailsapp/wails/v3 v3.1.0

require (
	github.com/coder/websocket v1.8.14 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
)
