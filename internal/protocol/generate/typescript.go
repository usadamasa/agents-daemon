package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// tsTypes は $defs を TypeScript の型に書く。
func tsTypes(defs *node) ([]byte, error) {
	var b strings.Builder
	for _, name := range defs.keys {
		def := defs.fields[name]
		b.WriteString("\n")
		tsComment(&b, "", def)
		ty, err := tsType(def, "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(&b, "export type %s = %s\n", name, ty)
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// tsDefinitions は $defs をそのまま持つ definitions の module を書く。
func tsDefinitions(schema []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, doc["$defs"], "", "  "); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(tsHeader + "\n")
	b.WriteString("// 型は types/index.d.ts の生成の印の間にある。\n\n")
	b.WriteString("// definitions は schema の $defs。$ref を含まないもの (Memo) はそのまま tool の inputSchema に渡せる。\n")
	fmt.Fprintf(&b, "export const definitions = %s as const\n", indented.String())
	return []byte(b.String()), nil
}

func tsComment(b *strings.Builder, indent string, n *node) {
	if d := n.str("description"); d != "" {
		fmt.Fprintf(b, "%s// %s\n", indent, d)
	}
}

func tsType(n *node, indent string) (string, error) {
	if ref := n.str("$ref"); ref != "" {
		return refName(ref), nil
	}
	if enum, ok := n.fields["enum"]; ok {
		values := make([]string, 0, len(enum.items))
		for _, v := range enum.items {
			values = append(values, fmt.Sprintf("'%s'", v.scalar))
		}
		return strings.Join(values, " | "), nil
	}
	switch n.str("type") {
	case "string":
		return "string", nil
	case "integer", "number":
		return "number", nil
	case "boolean":
		return "boolean", nil
	case "array":
		item, err := tsType(n.fields["items"], indent)
		if err != nil {
			return "", err
		}
		if strings.Contains(item, " | ") {
			item = "(" + item + ")"
		}
		return item + "[]", nil
	case "object":
		return tsObject(n, indent)
	}
	return "", fmt.Errorf("type %q を TypeScript に書けない", n.str("type"))
}

func tsObject(n *node, indent string) (string, error) {
	required := n.required()
	props := n.fields["properties"]
	if props == nil {
		return "Record<string, never>", nil
	}
	inner := indent + "  "
	var b strings.Builder
	b.WriteString("{\n")
	for _, name := range props.keys {
		prop := props.fields[name]
		tsComment(&b, inner, prop)
		ty, err := tsType(prop, inner)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		optional := "?"
		if required[name] {
			optional = ""
		}
		fmt.Fprintf(&b, "%s%s%s: %s\n", inner, name, optional, ty)
	}
	b.WriteString(indent + "}")
	return b.String(), nil
}
