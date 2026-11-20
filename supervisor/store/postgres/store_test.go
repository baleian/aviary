package postgres

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStorable(t *testing.T) {
	for _, output := range []string{
		"plain",
		"a\x00b",
		"\x00\x00",
		`a backslash \ and a quote "`,
		// Text that only looks like an escaped U+0000.
		`\u0000`,
		`\\u0000`,
		"\\\x00",
		"\n\x00\t\\",
		"한\x00글",
	} {
		item, err := json.Marshal(map[string]string{"output": output})
		if err != nil {
			t.Fatal(err)
		}
		var kept map[string]string
		if err := json.Unmarshal(storable(item), &kept); err != nil {
			t.Errorf("storable(%s) is not JSON: %v", item, err)
			continue
		}
		if want := strings.ReplaceAll(output, "\x00", "�"); kept["output"] != want {
			t.Errorf("storable(%s) holds %q, not %q", item, kept["output"], want)
		}
	}
}
