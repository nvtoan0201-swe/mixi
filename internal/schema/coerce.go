package schema

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Coercion rules ported from Pi's argument-normalization pass: LLMs often
// send numbers/booleans as strings (or numbers where strings are wanted), so
// values are converted toward the schema's declared type before validation,
// and only when the conversion is lossless. The walk never fails — values
// that cannot be coerced pass through unchanged and validation reports them.

// coerce returns val converted toward the type declared by schemaNode.
// ptr is the JSON pointer of schemaNode within the root schema document,
// used to look up pre-compiled union member subschemas.
func (v *Validator) coerce(val any, schemaNode any, ptr string) any {
	node, ok := schemaNode.(map[string]any)
	if !ok {
		return val // boolean schemas (true/false) declare no type to coerce toward
	}

	for _, kw := range []string{"anyOf", "oneOf"} {
		if members, ok := node[kw].([]any); ok {
			return v.coerceUnion(val, members, ptr, kw)
		}
	}

	// "type" as an array of types is rare in tool schemas; values pass
	// through uncoerced and the validator decides.
	t, _ := node["type"].(string)
	switch t {
	case "object":
		return v.coerceObject(val, node, ptr)
	case "array":
		return v.coerceArray(val, node, ptr)
	case "number", "integer":
		return coerceNumber(val)
	case "boolean":
		return coerceBool(val)
	case "string":
		return coerceString(val)
	}
	return val
}

// coerceUnion tries each member in order: coerce a copy of the value toward
// the member, keep the first result the member's subschema accepts. No match
// returns the value untouched.
func (v *Validator) coerceUnion(val any, members []any, ptr, kw string) any {
	for i, member := range members {
		p := ptr + "/" + kw + "/" + strconv.Itoa(i)
		candidate := v.coerce(deepCopy(val), member, p)
		if sch := v.subs[p]; sch != nil && sch.Validate(candidate) == nil {
			return candidate
		}
	}
	return val
}

func (v *Validator) coerceObject(val any, node map[string]any, ptr string) any {
	m, ok := val.(map[string]any)
	if !ok {
		return val
	}
	props, _ := node["properties"].(map[string]any)
	for name, sub := range props {
		if cur, present := m[name]; present {
			m[name] = v.coerce(cur, sub, ptr+"/properties/"+escapePointerToken(name))
		}
	}
	// Unknown properties pass through untouched (provider tolerance).
	return m
}

func (v *Validator) coerceArray(val any, node map[string]any, ptr string) any {
	arr, ok := val.([]any)
	if !ok {
		return val
	}
	items, ok := node["items"]
	if !ok {
		return arr
	}
	for i, el := range arr {
		arr[i] = v.coerce(el, items, ptr+"/items")
	}
	return arr
}

// coerceNumber converts numeric strings ("5", "3.14", "1e3") to json.Number.
func coerceNumber(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}
	if n, ok := parseJSONNumber(s); ok {
		return n
	}
	return val
}

// coerceBool accepts the string forms "true"/"false" and the numeric 1/0.
func coerceBool(val any) any {
	switch x := val.(type) {
	case string:
		switch x {
		case "true":
			return true
		case "false":
			return false
		}
	case json.Number:
		switch x.String() {
		case "1":
			return true
		case "0":
			return false
		}
	}
	return val
}

// coerceString renders numbers as their literal text when a string is wanted.
func coerceString(val any) any {
	if n, ok := val.(json.Number); ok {
		return n.String()
	}
	return val
}

// parseJSONNumber reports whether s is exactly one JSON number literal
// (rejects Inf/NaN/hex floats that strconv would accept, and trailing junk).
func parseJSONNumber(s string) (json.Number, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", false
	}
	n, ok := v.(json.Number)
	if !ok {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	return n, true
}

// deepCopy clones maps and slices so union try-each attempts cannot leak
// partial mutations into the original value.
func deepCopy(val any) any {
	switch x := val.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = deepCopy(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = deepCopy(v)
		}
		return out
	default:
		return val // string, json.Number, bool, nil are immutable
	}
}

// escapePointerToken escapes a property name for use as a JSON pointer
// reference token (RFC 6901: ~ → ~0, / → ~1).
func escapePointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}
