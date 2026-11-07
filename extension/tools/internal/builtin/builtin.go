// Package builtin implements flow's built-in tools: read, write, edit, bash,
// grep, glob, and ls. The tools extension offers the model some of them, and
// its ui package describes the calls of all of them. They run with the
// process's file-system privileges.
package builtin

import "github.com/unaquatic/flow/agent"

// All returns every built-in tool in stable order.
func All() []agent.Tool {
	return []agent.Tool{
		Read(),
		Write(),
		Edit(),
		Bash(),
		Grep(),
		Glob(),
		Ls(),
	}
}
