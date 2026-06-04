package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type grepTool struct{ cwd string }

func (t *grepTool) Name() string { return "grep" }

func (t *grepTool) Description() string {
	return "Search file contents with a regex (ripgrep). Returns matching lines with file:line."
}

func (t *grepTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"pattern":{"type":"string","description":"Regular expression (or literal string with literal=true)"},
"path":{"type":"string","description":"File or directory to search; defaults to cwd"},
"glob":{"type":"string","description":"Only search files matching this glob"},
"ignoreCase":{"type":"boolean"},
"literal":{"type":"boolean","description":"Treat pattern as a fixed string"},
"context":{"type":"integer","minimum":0,"maximum":10,"description":"Context lines around each match"},
"limit":{"type":"integer","minimum":1,"default":100,"description":"Stop after this many matches"}
},"required":["pattern"]}`)
}

func (t *grepTool) Mode() ExecMode { return ExecParallel }

func (t *grepTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignoreCase"`
		Literal    bool   `json:"literal"`
		Context    int    `json:"context"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("grep: bad arguments: %w", err)
	}
	if a.Limit <= 0 {
		a.Limit = 100
	}
	rg, ok := findBinary("rg")
	if !ok {
		return missingRipgrepError(), nil
	}

	argv := []string{"--json", "--line-number", "--color=never", "--hidden"}
	if a.IgnoreCase {
		argv = append(argv, "-i")
	}
	if a.Literal {
		argv = append(argv, "-F")
	}
	if a.Context > 0 {
		argv = append(argv, "-C", strconv.Itoa(a.Context))
	}
	if a.Glob != "" {
		argv = append(argv, "--glob", a.Glob)
	}
	argv = append(argv, "--", a.Pattern)
	if a.Path != "" {
		argv = append(argv, a.Path)
	}

	cmd := exec.CommandContext(ctx, rg, argv...)
	cmd.Dir = t.cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ToolResult{}, fmt.Errorf("grep: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return ToolResult{}, fmt.Errorf("grep: %w", err)
	}

	lines, matches, hitLimit := parseRgJSON(stdout, a.Limit)
	if hitLimit {
		cmd.Process.Kill() // stop scanning a huge tree once we have enough
	}
	waitErr := cmd.Wait()

	if matches == 0 {
		// rg exits 1 for "no matches" — not a failure for the model.
		if ee, ok := waitErr.(*exec.ExitError); waitErr == nil || (ok && ee.ExitCode() == 1) {
			return Text("no matches found"), nil
		}
		if !hitLimit && waitErr != nil {
			return commandFailedError("grep (rg)", waitErr, stderr.String()), nil
		}
	}
	out := strings.Join(lines, "\n")
	if hitLimit {
		out += fmt.Sprintf("\n[truncated at %d matches]", a.Limit)
	}
	return Text(out), nil
}

// rgEvent is the subset of ripgrep's --json line stream we consume.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
		Lines      struct {
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"data"`
}

// parseRgJSON renders match/context events as file:line lines, clipping each
// to GrepMaxLineLen and stopping after limit matches.
func parseRgJSON(r io.Reader, limit int) (lines []string, matches int, hitLimit bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev rgEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		sep := ""
		switch ev.Type {
		case "match":
			matches++
			sep = ":"
		case "context":
			sep = "-"
		default:
			continue
		}
		text := strings.TrimRight(ev.Data.Lines.Text, "\n")
		lines = append(lines, fmt.Sprintf("%s%s%d%s%s",
			ev.Data.Path.Text, sep, ev.Data.LineNumber, sep, clipLine(text, GrepMaxLineLen)))
		if matches >= limit {
			return lines, matches, true
		}
	}
	return lines, matches, false
}
