package main

import (
	"context"
	"io"
	"os"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
)

// newApp starts the app as flow is built: over the data directory, logging
// to its log at the configured level, with the extensions built into flow,
// whose protocol extension makes the providers of the catalog.
func newApp(ctx context.Context) (*app.App, error) {
	dir, err := config.DataDir()
	if err != nil {
		return nil, err
	}
	conf := config.NewService(dir)
	cfg, err := conf.Get()
	if err != nil {
		return nil, err
	}
	app.InitLogging(dir, cfg.LogLevel)
	return app.New(ctx, conf, app.Options{Extensions: extensions})
}

// withApp returns a Command.Run that starts the app, as newApp does, passes it
// to run with standard output as w, where run writes its output, and closes
// it once run returns.
func withApp[T any](run func(ctx context.Context, w io.Writer, a *app.App, o T, args []string) error) func(context.Context, T, []string) error {
	return func(ctx context.Context, o T, args []string) error {
		a, err := newApp(ctx)
		if err != nil {
			return err
		}
		defer a.Close()
		return run(ctx, os.Stdout, a, o, args)
	}
}
