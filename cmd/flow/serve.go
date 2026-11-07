package main

import (
	"context"
	"flag"
	"io"
	"os/signal"
	"syscall"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/app/server"
	"github.com/unaquatic/flow/internal/cmdline"
)

// serveOptions are the flags of the serve command.
type serveOptions struct {
	port int
}

var serveCmd = &cmdline.Command[serveOptions]{
	Name:  "serve",
	Short: "Run the HTTP API server (localhost only)",
	Flags: func(fs *flag.FlagSet, o *serveOptions) {
		fs.IntVar(&o.port, "port", 4096, "port to listen on")
	},
	Run: func(ctx context.Context, o serveOptions, args []string) error {
		// A signal also stops the app's work in progress.
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return withApp(runServer)(ctx, o, args)
	},
}

// runServer serves the HTTP API of a on the port o names until ctx is done.
// A request that names no model goes to the default model in the config file,
// which PATCH /config sets. It writes nothing to w.
func runServer(ctx context.Context, _ io.Writer, a *app.App, o serveOptions, _ []string) error {
	return server.ListenAndServe(ctx, a, o.port)
}
