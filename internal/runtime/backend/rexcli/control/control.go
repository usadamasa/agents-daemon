// Package control は rex server の control API (Unix socket の上の WebSocket、subprotocol rex.control.v1) の client。
package control

import (
	"context"
	"encoding/json"
	"errors"
)

// APIRevision は hello で名乗る API revision。
const APIRevision = 14

// ErrEventOverflow は event の queue が溢れて接続を閉じたことを表す。
var ErrEventOverflow = errors.New("event の queue が溢れた")

// Error は server が response の error で返した失敗。
type Error struct {
	Method  string
	Code    string
	Message string
	Status  int
}

func (e *Error) Error() string { return "" }

// IsNotFound は err が対象 (block、window、session) が無いことによる失敗かを返す。
func IsNotFound(err error) bool { return false }

// Options は Dial の設定。
type Options struct {
	ClientName    string
	ClientVersion string
	EventBuffer   int
}

// Welcome は server が hello に返した welcome のうち、client が使うもの。
type Welcome struct {
	APIRevision     int
	MaxMessageBytes int
}

// Event は server が送った event 1 件。
type Event struct {
	Seq     int64
	Payload json.RawMessage
}

// Conn は control 接続 1 本。
type Conn struct{}

// Dial は socket へ control 接続を張る。
func Dial(ctx context.Context, socketPath string, opts Options) (*Conn, error) {
	return nil, errors.New("未実装")
}

func (c *Conn) Welcome() Welcome { return Welcome{} }

func (c *Conn) Call(ctx context.Context, method string, payload, out any) error {
	return errors.New("未実装")
}

func (c *Conn) Events() <-chan Event { return nil }

func (c *Conn) Done() <-chan struct{} { return nil }

func (c *Conn) Err() error { return nil }

func (c *Conn) Close() error { return nil }

// SocketPath は control 接続の socket のパスを返す。
func SocketPath(getenv func(string) string, home string) (string, error) {
	return "", errors.New("未実装")
}
