// Package protocol は plugin の mod とバイナリのあいだで受け渡す JSON の形を、protocol.schema.json の $defs で持つ。
// Go の型 (types.go) と mod の型 (types/index.d.ts の生成の印の間、hooks/protocol.ts) は schema から生成する。
// mod が呼ぶコマンドは入力を stdin の JSON で受け、Decode が schema で確かめてから読む。
package protocol

//go:generate go run ./generate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	_ "embed"

	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed protocol.schema.json
var raw []byte

// root は protocol.schema.json を読んだもの。読めなければ embed した中身が壊れているので、起動時に落とす。
var root = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		panic(fmt.Sprintf("protocol.schema.json を読めない: %v", err))
	}
	return &s
}()

// defNames は $defs の名前を名前の順で返す。
func defNames() []string {
	names := make([]string, 0, len(root.Defs))
	for name := range root.Defs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// resolve は $defs の def を、ほかの $defs への $ref を解決できる 1 つの schema にする。
func resolve(def string) (*jsonschema.Resolved, error) {
	if _, ok := root.Defs[def]; !ok {
		return nil, fmt.Errorf("protocol.schema.json に $defs の %s が無い", def)
	}
	s := &jsonschema.Schema{Ref: "#/$defs/" + def, Defs: root.Defs}
	return s.Resolve(nil)
}

// Decode は r の JSON を def の形か確かめてから v に読む。
func Decode(r io.Reader, def string, v any) error {
	resolved, err := resolve(def)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("stdin を読めない: %w", err)
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		return fmt.Errorf("stdin の JSON を読めない: %w", err)
	}
	if err := resolved.Validate(instance); err != nil {
		return fmt.Errorf("stdin の JSON が %s の形でない: %w", def, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("stdin の JSON を %s として読めない: %w", def, err)
	}
	return nil
}
