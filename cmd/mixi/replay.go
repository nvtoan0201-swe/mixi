package main

import (
	"fmt"
	"io"

	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/modes"
	"github.com/user/mixi-agent/internal/obs"
)

// runReplay handles `mixi replay <session.jsonl>`: re-render a recorded
// session to stdout with zero API calls.
func runReplay(rc *config.RuntimeConfig, stdout, stderr io.Writer) int {
	f := rc.Flags
	if f.ReplayFile == "" {
		fmt.Fprintln(stderr, "mixi: replay needs a session file: mixi replay <session.jsonl> [--speed 1x|5x|instant] [--until <entryId>]")
		return modes.ExitUsage
	}
	err := obs.RunReplay(f.ReplayFile, obs.ReplayOptions{
		Speed: f.Speed,
		Until: f.Until,
		Out:   stdout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}
	return modes.ExitOK
}
