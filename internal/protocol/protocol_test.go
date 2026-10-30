package protocol

import (
	"strings"
	"testing"
)

func TestDecodeReadsValidInput(t *testing.T) {
	var got Memo
	if err := Decode(strings.NewReader(`{"kind":"aside","title":"t","files":["a"]}`), "Memo", &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Kind != "aside" || got.Title != "t" || len(got.Files) != 1 {
		t.Errorf("Decode = %+v", got)
	}
}

func TestDecodeRejectsInputOutsideSchema(t *testing.T) {
	cases := map[string]string{
		"空":          ``,
		"JSON でない":   `{`,
		"required":   `{"kind":"aside"}`,
		"enum":       `{"kind":"other","title":"t"}`,
		"型":          `{"kind":"aside","title":1}`,
		"知らない欄":      `{"kind":"aside","title":"t","extra":1}`,
		"object でない": `[]`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var got Memo
			if err := Decode(strings.NewReader(in), "Memo", &got); err == nil {
				t.Errorf("Decode(%q) は error を返さない", in)
			}
		})
	}
}

func TestDecodeRejectsUnknownDef(t *testing.T) {
	var got Memo
	if err := Decode(strings.NewReader(`{}`), "NoSuchDef", &got); err == nil {
		t.Error("無い def で error を返さない")
	}
}

func TestEveryDefResolves(t *testing.T) {
	for _, name := range defNames() {
		if _, err := resolve(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
