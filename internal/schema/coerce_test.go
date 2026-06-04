package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// mustCoerce compiles schema, runs ValidateAndCoerce, and decodes both the
// output and want JSON (json.Number mode) for structural comparison.
func mustCoerce(t *testing.T, schemaJSON, argsJSON, wantJSON string) {
	t.Helper()
	v, err := Compile(json.RawMessage(schemaJSON))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := v.ValidateAndCoerce("test", json.RawMessage(argsJSON))
	if err != nil {
		t.Fatalf("ValidateAndCoerce: %v", err)
	}
	got, err := decodeWithNumbers(out)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	want, err := decodeWithNumbers(json.RawMessage(wantJSON))
	if err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coerced mismatch\n got: %s\nwant: %s", out, wantJSON)
	}
}

func TestCoercionTable(t *testing.T) {
	intSchema := `{"type":"object","properties":{"n":{"type":"integer"}}}`
	numSchema := `{"type":"object","properties":{"x":{"type":"number"}}}`
	boolSchema := `{"type":"object","properties":{"b":{"type":"boolean"}}}`
	strSchema := `{"type":"object","properties":{"s":{"type":"string"}}}`

	cases := []struct {
		name, schema, args, want string
	}{
		{"numeric string to integer", intSchema, `{"n":"5"}`, `{"n":5}`},
		{"numeric string to float", numSchema, `{"x":"3.14"}`, `{"x":3.14}`},
		{"scientific notation string", numSchema, `{"x":"1e3"}`, `{"x":1e3}`},
		{"negative numeric string", intSchema, `{"n":"-7"}`, `{"n":-7}`},
		{"number stays number", intSchema, `{"n":42}`, `{"n":42}`},
		{"string true to bool", boolSchema, `{"b":"true"}`, `{"b":true}`},
		{"string false to bool", boolSchema, `{"b":"false"}`, `{"b":false}`},
		{"number 1 to bool", boolSchema, `{"b":1}`, `{"b":true}`},
		{"number 0 to bool", boolSchema, `{"b":0}`, `{"b":false}`},
		{"bool stays bool", boolSchema, `{"b":true}`, `{"b":true}`},
		{"number to string", strSchema, `{"s":42}`, `{"s":"42"}`},
		{"float to string", strSchema, `{"s":3.5}`, `{"s":"3.5"}`},
		{"string stays string", strSchema, `{"s":"42"}`, `{"s":"42"}`},
		{"unknown props pass through", intSchema, `{"n":"5","extra":"x"}`, `{"n":5,"extra":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustCoerce(t, tc.schema, tc.args, tc.want)
		})
	}
}

func TestCoercionNestedObjectsAndArrays(t *testing.T) {
	schema := `{"type":"object","properties":{
		"items":{"type":"array","items":{"type":"object","properties":{
			"count":{"type":"integer"},
			"active":{"type":"boolean"}
		}}},
		"meta":{"type":"object","properties":{"depth":{"type":"integer"}}}
	}}`
	args := `{"items":[{"count":"3","active":"true"},{"count":7,"active":0}],"meta":{"depth":"2"}}`
	want := `{"items":[{"count":3,"active":true},{"count":7,"active":false}],"meta":{"depth":2}}`
	mustCoerce(t, schema, args, want)
}

func TestCoercionAnyOfTryEach(t *testing.T) {
	// First union member that accepts the coerced value wins.
	schema := `{"type":"object","properties":{"v":{"anyOf":[{"type":"integer"},{"type":"string"}]}}}`
	mustCoerce(t, schema, `{"v":"5"}`, `{"v":5}`)       // member 0 (integer) wins
	mustCoerce(t, schema, `{"v":"abc"}`, `{"v":"abc"}`) // member 0 fails, member 1 (string) wins

	// No member matches even after coercion → validation fails, original echoed.
	v, err := Compile(json.RawMessage(schema))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.ValidateAndCoerce("test", json.RawMessage(`{"v":true}`)); err == nil {
		t.Fatal("expected validation error for bool against [integer,string] union")
	}
}

func TestCoercionOneOf(t *testing.T) {
	schema := `{"type":"object","properties":{"v":{"oneOf":[{"type":"boolean"},{"type":"number"}]}}}`
	mustCoerce(t, schema, `{"v":"true"}`, `{"v":true}`)
	mustCoerce(t, schema, `{"v":"2.5"}`, `{"v":2.5}`)
}

func TestCoercionNonCoercibleFailsWithOriginalArgs(t *testing.T) {
	v, err := Compile(json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`))
	if err != nil {
		t.Fatal(err)
	}
	original := `{"n":"not-a-number"}`
	_, err = v.ValidateAndCoerce("test", json.RawMessage(original))
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Error must echo the ORIGINAL string value, never a mutated form.
	if !strings.Contains(err.Error(), `"n": "not-a-number"`) {
		t.Fatalf("error must echo original args:\n%s", err)
	}
}

func TestCoercionDoesNotMutateInsideFailedUnion(t *testing.T) {
	// Union members are tried on copies: a member that coerces a nested
	// field but then fails overall must not leak the partial change.
	schema := `{"type":"object","properties":{"v":{"anyOf":[
		{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]},
		{"type":"object","properties":{"a":{"type":"string"}}}
	]}}}`
	// Member 0 coerces a:"1" → 1 but fails (b missing) → must fall through
	// to member 1 with the ORIGINAL a:"1" string intact.
	mustCoerce(t, schema, `{"v":{"a":"1"}}`, `{"v":{"a":"1"}}`)
}

func TestParseJSONNumberRejectsNonJSON(t *testing.T) {
	for _, s := range []string{"Inf", "NaN", "0x10", "1.2.3", "5 junk", "", "abc", "true"} {
		if n, ok := parseJSONNumber(s); ok {
			t.Errorf("parseJSONNumber(%q) = %v, want reject", s, n)
		}
	}
	for _, s := range []string{"5", "-7", "3.14", "1e3", "0"} {
		if _, ok := parseJSONNumber(s); !ok {
			t.Errorf("parseJSONNumber(%q): want accept", s)
		}
	}
}
