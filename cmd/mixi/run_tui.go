package main

import (
	"context"
	"fmt"
	"io"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/compact"
	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
	"github.com/user/mixi-agent/internal/tui"
)

// runTUI hands the assembled runtime to the interactive frontend and maps
// its outcome onto a process exit code.
func runTUI(a *agent.Agent, eng *perm.Engine, ctrl *compact.Controller,
	store session.Storage, jobs *tools.JobTable, rc *config.RuntimeConfig, stderr io.Writer) int {
	err := tui.Run(context.Background(), tui.Deps{
		Agent:     a,
		Engine:    eng,
		Compactor: ctrl,
		Store:     store,
		Jobs:      jobs,
		Models:    modelCatalog(rc.Model),
	})
	if err != nil {
		fmt.Fprintf(stderr, "mixi: tui: %v\n", err)
		return 1
	}
	return 0
}

// modelCatalog lists the models /model and ctrl+p cycle through. A model
// outside the built-in catalog (e.g. faux/scripted) is prepended so cycling
// can always return to the session's starting model.
func modelCatalog(current ai.Model) []ai.Model {
	catalog := ai.Models()
	for _, m := range catalog {
		if m.Provider == current.Provider && m.ID == current.ID {
			return catalog
		}
	}
	return append([]ai.Model{current}, catalog...)
}
