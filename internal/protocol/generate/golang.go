package main

import (
	"fmt"
	"go/format"
	"strings"
)

// goTypes は $defs を protocol パッケージの Go の型に書く。必須でない欄は omitempty、必須でない object と時刻は
// ポインタにする (無いことを JSON から落とすため)。
func goTypes(defs *node) ([]byte, error) {
	var b strings.Builder
	b.WriteString(goHeader + "\n\n")
	b.WriteString("package protocol\n\n")
	if usesTime(defs) {
		b.WriteString("import \"time\"\n\n")
	}
	for _, name := range defs.keys {
		def := defs.fields[name]
		goComment(&b, name+" は", def)
		switch def.str("type") {
		case "object":
			body, err := goStruct(defs, def)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			fmt.Fprintf(&b, "type %s %s\n\n", name, body)
		case "array":
			item, err := goType(defs, def.fields["items"], true)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			fmt.Fprintf(&b, "type %s []%s\n\n", name, item)
		default:
			return nil, fmt.Errorf("%s: def の type %q を Go に書けない", name, def.str("type"))
		}
	}
	return format.Source([]byte(b.String()))
}

func goStruct(defs, n *node) (string, error) {
	required := n.required()
	props := n.fields["properties"]
	var b strings.Builder
	b.WriteString("struct {\n")
	if props != nil {
		for _, name := range props.keys {
			prop := props.fields[name]
			goComment(&b, "", prop)
			ty, err := goType(defs, prop, required[name])
			if err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			tag := name
			if !required[name] {
				tag += ",omitempty"
			}
			fmt.Fprintf(&b, "%s %s `json:\"%s\"`\n", goName(name), ty, tag)
		}
	}
	b.WriteString("}")
	return b.String(), nil
}

func goType(defs, n *node, required bool) (string, error) {
	if ref := n.str("$ref"); ref != "" {
		name := refName(ref)
		def, ok := defs.fields[name]
		if !ok {
			return "", fmt.Errorf("$ref %s を解決できない", ref)
		}
		if !required && def.str("type") == "object" {
			return "*" + name, nil
		}
		return name, nil
	}
	switch n.str("type") {
	case "string":
		if n.str("format") == "date-time" {
			if required {
				return "time.Time", nil
			}
			return "*time.Time", nil
		}
		return "string", nil
	case "integer":
		return "int", nil
	case "number":
		return "float64", nil
	case "boolean":
		return "bool", nil
	case "array":
		item, err := goType(defs, n.fields["items"], true)
		if err != nil {
			return "", err
		}
		return "[]" + item, nil
	}
	return "", fmt.Errorf("type %q を Go に書けない (object は $defs に置いて $ref で指す)", n.str("type"))
}

// goName は JSON の欄名を Go の欄名にする。id と cwd はリポジトリの Go の書き方に合わせて大文字にする。
func goName(name string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(name, "_") {
		switch part {
		case "id", "cwd":
			b.WriteString(strings.ToUpper(part))
		default:
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

func goComment(b *strings.Builder, prefix string, n *node) {
	d := n.str("description")
	if d == "" {
		return
	}
	if !strings.HasSuffix(d, "。") {
		d += "。"
	}
	// 「X は」の後は、英数字で始まる説明だけ空白を挟む (リポジトリのコメントの書き方)。
	if prefix != "" && d[0] < 0x80 {
		prefix += " "
	}
	fmt.Fprintf(b, "// %s%s\n", prefix, d)
}

func usesTime(n *node) bool {
	if n.str("format") == "date-time" {
		return true
	}
	for _, f := range n.fields {
		if usesTime(f) {
			return true
		}
	}
	return false
}
