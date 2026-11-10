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
// whose protocol extension makes the providers of the catalog, unless opts
// gives them, as extensionsWith makes them, and the other parts opts gives.
func newApp(ctx context.Context, opts app.Options) (*app.App, error) {
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
	if opts.Extensions == nil {
		opts.Extensions = extensions
	}
	return app.New(ctx, conf, opts)
}

// withApp returns a Command.Run that starts the app, as newApp does, passes it
// to run with standard output as w, where run writes its output, and closes
// it once run returns.
func withApp[T any](run func(ctx context.Context, w io.Writer, a *app.App, o T, args []string) error) func(context.Context, T, []string) error {
	return withAppOptions(func(T) app.Options { return app.Options{} }, run)
}

// withAppOptions returns a Command.Run that runs run as withApp does, on an
// app started with the options that options gives for the command's own.
func withAppOptions[T any](options func(T) app.Options, run func(ctx context.Context, w io.Writer, a *app.App, o T, args []string) error) func(context.Context, T, []string) error {
	return func(ctx context.Context, o T, args []string) error {
		a, err := newApp(ctx, options(o))
		if err != nil {
			return err
		}
		defer a.Close()
		return run(ctx, os.Stdout, a, o, args)
	}
}
