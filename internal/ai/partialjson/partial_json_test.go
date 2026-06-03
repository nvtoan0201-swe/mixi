package partialjson

import (
	"encoding/json"
	"testing"
)

func TestParsePartial(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Already valid → returned as-is.
		{"empty object", `{}`, `{}`},
		{"complete", `{"a":1,"b":"x"}`, `{"a":1,"b":"x"}`},
		{"complete array", `[1,2]`, `[1,2]`},
		{"whitespace padded", "  {\"a\":1}\n", `{"a":1}`},

		// Truncated structures.
		{"empty input", ``, `{}`},
		{"lone brace", `{`, `{}`},
		{"lone bracket", `[`, `[]`},
		{"bare key", `{"a"`, `{"a":null}`},
		{"key colon", `{"a":`, `{"a":null}`},
		{"key value", `{"a":1`, `{"a":1}`},
		{"trailing comma obj", `{"a":1,`, `{"a":1}`},
		{"open string value", `{"a":"hel`, `{"a":"hel"}`},
		{"nested array", `{"a":[1,2`, `{"a":[1,2]}`},
		{"nested object", `{"a":{"b":"c`, `{"a":{"b":"c"}}`},
		{"array trailing comma", `[1,2,`, `[1,2]`},
		{"array open string", `["a","b`, `["a","b"]`},
		{"deep nesting", `{"a":[{"b":[{"c":"d`, `{"a":[{"b":[{"c":"d"}]}]}`},

		// Partial literals & numbers.
		{"partial true", `{"a":tru`, `{"a":true}`},
		{"partial false", `{"a":fal`, `{"a":false}`},
		{"partial null", `{"a":nu`, `{"a":null}`},
		{"bare literal", `tru`, `true`},
		{"partial exponent", `{"a":1.2e`, `{"a":1.2}`},
		{"lone minus", `{"a":-`, `{"a":null}`},
		{"number", `{"a":-12.5`, `{"a":-12.5}`},

		// Dangling escapes.
		{"trailing backslash", `{"s":"line\`, `{"s":"line"}`},
		{"partial unicode escape", `{"s":"u\u00`, `{"s":"u"}`},
		{"complete escape kept", `{"s":"a\nb`, `{"s":"a\nb"}`},

		// Repair path: raw control chars / invalid escapes inside strings.
		{"raw newline in string", "{\"s\":\"a\nb\"}", `{"s":"a\nb"}`},
		{"raw tab in string", "{\"s\":\"a\tb\"}", `{"s":"a\tb"}`},
		{"invalid escape", `{"s":"a\qb"}`, `{"s":"a\\qb"}`},
		{"raw newline in truncated string", "{\"s\":\"a\nb", `{"s":"a\nb"}`},

		// Garbage → {}.
		{"garbage", `hello`, `{}`},
		{"lone colon", `:`, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParsePartial([]byte(c.in))
			if string(got) != c.want {
				t.Errorf("ParsePartial(%q) = %s, want %s", c.in, got, c.want)
			}
			if !json.Valid(got) {
				t.Errorf("ParsePartial(%q) returned invalid JSON: %s", c.in, got)
			}
		})
	}
}

func TestParsePartialPrefixesAlwaysValid(t *testing.T) {
	full := `{"path":"cmd/mixi/main.go","edits":[{"oldText":"a\nb","newText":"c\td"}],"count":3,"force":true}`
	for i := 0; i <= len(full); i++ {
		out := ParsePartial([]byte(full[:i]))
		if !json.Valid(out) {
			t.Fatalf("prefix %d %q → invalid JSON: %s", i, full[:i], out)
		}
	}
}

func FuzzParsePartial(f *testing.F) {
	seeds := []string{
		"", "{", "[", `{"a":1}`, `{"a":`, `{"a":"b`, `[1,2,`, `tru`, `-`,
		`{"s":"a\`, `{"s":"\u12`, "{\"s\":\"a\nb", `{"a":[{"b":tru`,
		`{"a":1.2e+`, "\x00\x01", `{"":{"":{"":`, `[[[[`, `"unclosed`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out := ParsePartial(data) // must never panic
		if !json.Valid(out) {
			t.Fatalf("ParsePartial(%q) returned invalid JSON: %s", data, out)
		}
	})
}
