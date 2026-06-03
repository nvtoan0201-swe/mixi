package partialjson

import "testing"

func TestRepair(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"untouched", `{"a":1}`, `{"a":1}`},
		{"raw newline", "{\"s\":\"a\nb\"}", `{"s":"a\nb"}`},
		{"raw cr", "{\"s\":\"a\rb\"}", `{"s":"a\rb"}`},
		{"raw tab", "{\"s\":\"a\tb\"}", `{"s":"a\tb"}`},
		{"raw backspace", "{\"s\":\"a\bb\"}", `{"s":"a\bb"}`},
		{"raw formfeed", "{\"s\":\"a\fb\"}", `{"s":"a\fb"}`},
		{"other control", "{\"s\":\"a\x01b\"}", `{"s":"a\u0001b"}`},
		{"valid escapes kept", `{"s":"a\n\t\\\"A"}`, `{"s":"a\n\t\\\"A"}`},
		{"invalid escape doubled", `{"s":"a\qb"}`, `{"s":"a\\qb"}`},
		{"control outside string untouched", "{\n\"a\":1}", "{\n\"a\":1}"},
		{"trailing backslash kept", `{"s":"a\`, `{"s":"a\`},
		{"escaped quote does not end string", `{"s":"a\"\n"}`, `{"s":"a\"\n"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Repair([]byte(c.in)); string(got) != c.want {
				t.Errorf("Repair(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
