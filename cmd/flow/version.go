package main

import (
	"context"
	"os"

	"github.com/unaquatic/flow/internal/cmdline"
)

var versionCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:    "version",
	Aliases: []string{"v"},
	Short:   "Show the version",
	Run: func(ctx context.Context, _ cmdline.NoOptions, args []string) error {
		printVersion(os.Stdout, cmdline.UseColor(os.Stdout), colorMode)
		return nil
	},
}
