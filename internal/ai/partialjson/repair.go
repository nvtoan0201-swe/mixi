package partialjson

// Repair fixes the two malformations LLMs commonly emit inside JSON string
// literals: raw (unescaped) control characters and invalid backslash escapes.
// Bytes outside string literals pass through unchanged.
func Repair(in []byte) []byte {
	out := make([]byte, 0, len(in)+8)
	inString := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			out = append(out, c)
			continue
		}
		switch {
		case c == '"':
			inString = false
			out = append(out, c)
		case c == '\\':
			if i+1 < len(in) && isValidEscapeChar(in[i+1]) {
				out = append(out, c, in[i+1])
				i++
			} else if i+1 == len(in) {
				out = append(out, c) // trailing backslash at EOF: keep for partial parse
			} else {
				out = append(out, '\\', '\\') // invalid escape → literal backslash
			}
		case c < 0x20: // raw control char inside string → escape it
			out = append(out, escapeControl(c)...)
		default:
			out = append(out, c)
		}
	}
	return out
}

func isValidEscapeChar(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
		return true
	}
	return false
}

func escapeControl(c byte) []byte {
	switch c {
	case '\b':
		return []byte(`\b`)
	case '\f':
		return []byte(`\f`)
	case '\n':
		return []byte(`\n`)
	case '\r':
		return []byte(`\r`)
	case '\t':
		return []byte(`\t`)
	}
	const hex = "0123456789abcdef"
	return []byte{'\\', 'u', '0', '0', hex[c>>4], hex[c&0xf]}
}
