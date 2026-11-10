package main

import (
	"context"
	"flag"
	"io"
	"os"

	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
)

// VersionOptions are the flags of the version command.
type VersionOptions struct {
	// Format is how the version is written.
	Format OutputFormat
}

var versionCmd = &cmdline.Command[VersionOptions]{
	Name:    "version",
	Aliases: []string{"v"},
	Short:   "Show the version",
	Flags:   func(fs *flag.FlagSet, o *VersionOptions) { formatFlag(fs, &o.Format) },
	Run: func(ctx context.Context, o VersionOptions, args []string) error {
		if o.Format == OutputJSON {
			return writeVersionJSON(os.Stdout)
		}
		printVersion(os.Stdout)
		return nil
	},
}

// VersionInfo is the version of flow, as flow version writes it in
// OutputJSON.
type VersionInfo struct {
	// Name is the application's name.
	Name string `json:"name"`
	// Version is its version, such as v1.2.3, or (devel) for a
	// development build.
	Version string `json:"version"`
}

// writeVersionJSON writes the name and version of the application to w, as
// a VersionInfo.
func writeVersionJSON(w io.Writer) error {
	return writeJSON(w, VersionInfo{Name: product.Name, Version: product.Version()})
}
