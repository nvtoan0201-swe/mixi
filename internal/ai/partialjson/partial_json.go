// Package partialjson parses incomplete or malformed JSON fragments, as
// produced by streaming LLM tool-call arguments, into valid JSON.
package partialjson

import (
	"bytes"
	"encoding/json"
	"strings"
)

var emptyObject = json.RawMessage("{}")

// ParsePartial converts a (possibly truncated or malformed) JSON fragment
// into valid JSON. Pipeline: as-is → repaired → structurally completed →
// completed(repaired) → "{}". It never returns an error and never panics.
func ParsePartial(in []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(in)
	if len(trimmed) == 0 {
		return emptyObject
	}
	if json.Valid(trimmed) {
		return json.RawMessage(bytes.Clone(trimmed))
	}
	repaired := Repair(trimmed)
	if json.Valid(repaired) {
		return repaired
	}
	if c, ok := complete(trimmed); ok && json.Valid(c) {
		return c
	}
	if c, ok := complete(repaired); ok && json.Valid(c) {
		return c
	}
	return emptyObject
}

// complete structurally finishes a truncated JSON value: closes open strings,
// arrays, and objects; finishes partially-typed literals; trims dangling
// number/escape fragments. Output validity is verified by the caller.
func complete(in []byte) ([]byte, bool) {
	p := &parser{in: in}
	p.skipWS()
	return p.value()
}

type parser struct {
	in  []byte
	pos int
}

func (p *parser) eof() bool { return p.pos >= len(p.in) }

func (p *parser) skipWS() {
	for !p.eof() {
		switch p.in[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) value() ([]byte, bool) {
	if p.eof() {
		return nil, false
	}
	switch c := p.in[p.pos]; {
	case c == '"':
		return p.str(), true
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == 't' || c == 'f' || c == 'n':
		return p.literal()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	default:
		return nil, false
	}
}

// str consumes a string literal, closing it if input ends mid-string and
// dropping a dangling escape or partial \uXXXX sequence at EOF.
func (p *parser) str() []byte {
	out := []byte{'"'}
	p.pos++
	for !p.eof() {
		c := p.in[p.pos]
		switch {
		case c == '"':
			p.pos++
			return append(out, '"')
		case c == '\\':
			if p.pos+1 >= len(p.in) { // dangling backslash at EOF → drop
				p.pos = len(p.in)
				return append(out, '"')
			}
			if p.in[p.pos+1] == 'u' {
				if p.pos+6 > len(p.in) { // partial \uXXXX at EOF → drop
					p.pos = len(p.in)
					return append(out, '"')
				}
				out = append(out, p.in[p.pos:p.pos+6]...)
				p.pos += 6
				continue
			}
			out = append(out, c, p.in[p.pos+1])
			p.pos += 2
		default:
			out = append(out, c)
			p.pos++
		}
	}
	return append(out, '"') // unterminated → close
}

func (p *parser) object() ([]byte, bool) {
	p.pos++ // '{'
	var pairs [][]byte
	for {
		p.skipWS()
		if p.eof() {
			break
		}
		if p.in[p.pos] == '}' {
			p.pos++
			break
		}
		if p.in[p.pos] != '"' { // malformed key → drop remainder
			break
		}
		key := p.str()
		p.skipWS()
		if p.eof() || p.in[p.pos] != ':' { // key without value → null
			pairs = append(pairs, append(append(key, ':'), "null"...))
			break
		}
		p.pos++ // ':'
		p.skipWS()
		v, ok := p.value()
		if !ok { // value truncated to nothing → null
			pairs = append(pairs, append(append(key, ':'), "null"...))
			break
		}
		pairs = append(pairs, append(append(key, ':'), v...))
		p.skipWS()
		if p.eof() {
			break
		}
		if p.in[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.in[p.pos] == '}' {
			p.pos++
			break
		}
		break // malformed separator → close with what we have
	}
	out := append([]byte{'{'}, bytes.Join(pairs, []byte{','})...)
	return append(out, '}'), true
}

func (p *parser) array() ([]byte, bool) {
	p.pos++ // '['
	var elems [][]byte
	for {
		p.skipWS()
		if p.eof() {
			break
		}
		if p.in[p.pos] == ']' {
			p.pos++
			break
		}
		v, ok := p.value()
		if !ok { // truncated/garbage element dropped
			break
		}
		elems = append(elems, v)
		p.skipWS()
		if p.eof() {
			break
		}
		if p.in[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.in[p.pos] == ']' {
			p.pos++
			break
		}
		break // malformed separator → close with what we have
	}
	out := append([]byte{'['}, bytes.Join(elems, []byte{','})...)
	return append(out, ']'), true
}

// literal completes a partially-typed true/false/null token.
func (p *parser) literal() ([]byte, bool) {
	start := p.pos
	for !p.eof() && p.in[p.pos] >= 'a' && p.in[p.pos] <= 'z' {
		p.pos++
	}
	tok := string(p.in[start:p.pos])
	for _, lit := range []string{"true", "false", "null"} {
		if tok != "" && strings.HasPrefix(lit, tok) {
			return []byte(lit), true
		}
	}
	return nil, false
}

// number consumes a number token and trims trailing bytes until it is a
// valid JSON number (e.g. "1.2e" → "1.2", "-" → dropped).
func (p *parser) number() ([]byte, bool) {
	start := p.pos
	for !p.eof() && isNumberByte(p.in[p.pos]) {
		p.pos++
	}
	tok := p.in[start:p.pos]
	for len(tok) > 0 && !json.Valid(tok) {
		tok = tok[:len(tok)-1]
	}
	if len(tok) == 0 {
		return nil, false
	}
	return bytes.Clone(tok), true
}

func isNumberByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E'
}
