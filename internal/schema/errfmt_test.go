package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// Golden snapshots: the error text is consumed by the LLM for
// self-correction, so the format is asserted byte-exact.

func TestErrorFormatGoldenMinimum(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["read"]))
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.ValidateAndCoerce("read", json.RawMessage(`{"path":"x","offset":0}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	want := `Validation failed for tool "read":
  - offset: minimum: got 0, want 1

Received arguments:
{
  "path": "x",
  "offset": 0
}`
	if err.Error() != want {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", err.Error(), want)
	}
}

func TestErrorFormatGoldenMissingRequired(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["write"]))
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.ValidateAndCoerce("write", json.RawMessage(`{"path":"out.txt"}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	want := `Validation failed for tool "write":
  - (root): missing property 'content'

Received arguments:
{
  "path": "out.txt"
}`
	if err.Error() != want {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", err.Error(), want)
	}
}

func TestErrorFormatGoldenTypeMismatch(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["read"]))
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.ValidateAndCoerce("read", json.RawMessage(`{"path":"x","offset":"abc"}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	want := `Validation failed for tool "read":
  - offset: got string, want integer

Received arguments:
{
  "path": "x",
  "offset": "abc"
}`
	if err.Error() != want {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", err.Error(), want)
	}
}

func TestErrorFormatCapsAtTenLines(t *testing.T) {
	// 12 integer properties, all given non-coercible strings → 12 leaf
	// violations, report must cap at 10 bullets.
	props := make([]string, 12)
	args := make([]string, 12)
	for i := range props {
		name := string(rune('a' + i))
		props[i] = `"` + name + `":{"type":"integer"}`
		args[i] = `"` + name + `":"x"`
	}
	schema := `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`
	v, err := Compile(json.RawMessage(schema))
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.ValidateAndCoerce("test", json.RawMessage(`{`+strings.Join(args, ",")+`}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if got := strings.Count(err.Error(), "\n  - "); got != 10 {
		t.Fatalf("bullet count = %d, want 10 (capped)\n%s", got, err.Error())
	}
}

func TestErrorFormatPreservesArgumentKeyOrder(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["read"]))
	if err != nil {
		t.Fatal(err)
	}
	// Keys deliberately reversed vs schema order; the echo must preserve
	// the wire order the model sent, proving raw bytes are re-indented.
	_, err = v.ValidateAndCoerce("read", json.RawMessage(`{"offset":0,"path":"x"}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	idx := strings.Index(err.Error(), `"offset": 0`)
	jdx := strings.Index(err.Error(), `"path": "x"`)
	if idx < 0 || jdx < 0 || idx > jdx {
		t.Fatalf("argument echo must preserve original key order:\n%s", err.Error())
	}
}
