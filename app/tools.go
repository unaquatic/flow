package app

import "github.com/unaquatic/flow/agent"

// Tools returns the tools the model may call, which the extensions add.
func (a *App) Tools() *agent.Tools {
	return a.tools
}
