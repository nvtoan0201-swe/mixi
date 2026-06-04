// Package schema compiles JSON Schemas (draft 2020-12) for tool arguments,
// coerces LLM-provided values toward declared types before validation, and
// renders validation failures as LLM-readable reports.
package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// resourceURL is the synthetic location every tool schema is compiled under.
const resourceURL = "tool:///schema.json"

// validators caches compiled validators by sha256 of the raw schema bytes so
// identical schemas across tool registrations share one compilation.
var validators sync.Map // [32]byte -> *Validator

// Validator pairs a compiled schema with what the coercion pass needs: the
// decoded schema document for the type-directed walk, and pre-compiled
// anyOf/oneOf member subschemas (keyed by JSON pointer) for union try-each.
type Validator struct {
	root *jsonschema.Schema
	doc  map[string]any
	subs map[string]*jsonschema.Schema
}

// Compile parses and compiles a draft 2020-12 schema, returning a cached
// Validator when the same schema bytes were compiled before. Tool arguments
// are always JSON objects, so the root schema must declare "type":"object".
func Compile(raw json.RawMessage) (*Validator, error) {
	key := sha256.Sum256(raw)
	if v, ok := validators.Load(key); ok {
		return v.(*Validator), nil
	}

	docAny, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("schema: invalid JSON: %w", err)
	}
	doc, ok := docAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema: root schema must be a JSON object")
	}
	if t, _ := doc["type"].(string); t != "object" {
		return nil, fmt.Errorf(`schema: root schema must declare "type":"object"`)
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(resourceURL, doc); err != nil {
		return nil, fmt.Errorf("schema: add resource: %w", err)
	}
	root, err := c.Compile(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("schema: compile: %w", err)
	}

	v := &Validator{root: root, doc: doc, subs: map[string]*jsonschema.Schema{}}
	if err := v.compileUnionMembers(c, doc, ""); err != nil {
		return nil, err
	}

	actual, _ := validators.LoadOrStore(key, v)
	return actual.(*Validator), nil
}

// ValidateAndCoerce coerces args toward the schema's declared types,
// validates the result, and returns the re-encoded arguments. On failure the
// error text is the Pi-style report echoing the ORIGINAL (uncoerced) args so
// the model sees exactly what it sent.
func (v *Validator) ValidateAndCoerce(toolName string, args json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	val, err := decodeWithNumbers(args)
	if err != nil {
		return nil, formatJSONError(toolName, args, err)
	}
	coerced := v.coerce(val, v.doc, "")
	if verr := v.root.Validate(coerced); verr != nil {
		return nil, formatValidationError(toolName, args, verr)
	}
	out, err := json.Marshal(coerced)
	if err != nil {
		return nil, fmt.Errorf("schema: re-encode arguments: %w", err)
	}
	return out, nil
}

// compileUnionMembers pre-compiles every anyOf/oneOf member reachable through
// schema positions (properties, items, additionalProperties, nested unions)
// so the coercion pass can validate candidates without touching the compiler.
func (v *Validator) compileUnionMembers(c *jsonschema.Compiler, node any, ptr string) error {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	for _, kw := range []string{"anyOf", "oneOf"} {
		members, _ := m[kw].([]any)
		for i, member := range members {
			p := ptr + "/" + kw + "/" + strconv.Itoa(i)
			sch, err := c.Compile(resourceURL + "#" + p)
			if err != nil {
				return fmt.Errorf("schema: compile union member %s: %w", p, err)
			}
			v.subs[p] = sch
			if err := v.compileUnionMembers(c, member, p); err != nil {
				return err
			}
		}
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, sub := range props {
			if err := v.compileUnionMembers(c, sub, ptr+"/properties/"+escapePointerToken(name)); err != nil {
				return err
			}
		}
	}
	if items, ok := m["items"]; ok {
		if err := v.compileUnionMembers(c, items, ptr+"/items"); err != nil {
			return err
		}
	}
	if ap, ok := m["additionalProperties"].(map[string]any); ok {
		if err := v.compileUnionMembers(c, ap, ptr+"/additionalProperties"); err != nil {
			return err
		}
	}
	return nil
}

// decodeWithNumbers decodes JSON preserving numbers as json.Number, matching
// how the schema library decodes instances (no float64 precision loss).
func decodeWithNumbers(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
