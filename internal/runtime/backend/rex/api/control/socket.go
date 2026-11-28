package control

import (
	"fmt"
	"net/url"
	"path/filepath"
)

// SocketPath は control 接続の socket のパスを返す。REX_SERVER (`unix://` の URL、パスは percent-encoded) があれば
// それを使い、無ければ macOS の既定の場所 (home の下) を返す。daemon や依存図のアプリは rex の外で起きると
// REX_SERVER を持たないので、既定の場所は CLI に聞かずにここで持つ。
// rex-premise: preview.37 api REX_SERVER は unix:// の URL。既定の socket は ~/Library/Application Support/rex/server.sock
func SocketPath(getenv func(string) string, home string) (string, error) {
	raw := getenv("REX_SERVER")
	if raw == "" {
		return filepath.Join(home, "Library", "Application Support", "rex", "server.sock"), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("REX_SERVER (%q) を URL として読めない: %w", raw, err)
	}
	if u.Scheme != "unix" || u.Path == "" {
		return "", fmt.Errorf("REX_SERVER (%q) は unix:// の socket でない (ほかの carrier には対応していない)", raw)
	}
	return u.Path, nil
}
