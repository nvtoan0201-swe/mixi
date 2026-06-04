package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// maxErrorLines caps the bullet list so a deeply broken argument object
// cannot flood the model's context with hundreds of violations.
const maxErrorLines = 10

var errPrinter = message.NewPrinter(language.English)

// formatValidationError renders the LLM-readable report. The model reads
// this as a tool result and self-corrects, so the format is load-bearing:
//
//	Validation failed for tool "<name>":
//	  - <fieldPath>: <message>
//
//	Received arguments:
//	<pretty JSON>
func formatValidationError(toolName string, original json.RawMessage, err error) error {
	var lines []string
	var verr *jsonschema.ValidationError
	if errors.As(err, &verr) {
		lines = flattenLeaves(verr, nil)
	} else {
		lines = []string{"(root): " + err.Error()}
	}
	if len(lines) > maxErrorLines {
		lines = lines[:maxErrorLines]
	}
	return errors.New(buildReport(toolName, original, lines))
}

// formatJSONError reports arguments that failed to parse as JSON at all.
func formatJSONError(toolName string, original json.RawMessage, err error) error {
	line := "(root): arguments are not valid JSON: " + err.Error()
	return errors.New(buildReport(toolName, original, []string{line}))
}

func buildReport(toolName string, original json.RawMessage, lines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Validation failed for tool %q:\n", toolName)
	for _, l := range lines {
		b.WriteString("  - ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteString("\nReceived arguments:\n")
	b.WriteString(prettyJSON(original))
	return b.String()
}

// flattenLeaves collects leaf violations depth-first; intermediate nodes
// (e.g. "doesn't validate with anyOf") only group their causes.
func flattenLeaves(e *jsonschema.ValidationError, out []string) []string {
	if len(e.Causes) == 0 {
		msg := e.ErrorKind.LocalizedString(errPrinter)
		return append(out, fieldPath(e.InstanceLocation)+": "+msg)
	}
	for _, c := range e.Causes {
		out = flattenLeaves(c, out)
	}
	return out
}

func fieldPath(loc []string) string {
	if len(loc) == 0 {
		return "(root)"
	}
	return strings.Join(loc, ".")
}

// prettyJSON re-indents the original raw bytes (key order preserved) so the
// model sees exactly the arguments it sent; falls back to raw on bad JSON.
func prettyJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
