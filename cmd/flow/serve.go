package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os/signal"
	"syscall"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/app/server"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/cmdline"
)

// serveOptions are the flags of the serve command.
type serveOptions struct {
	hostname string
	port     int
	cors     []string
}

var serveCmd = &cmdline.Command[serveOptions]{
	Name:  "serve",
	Short: "Run the HTTP API server",
	Flags: func(fs *flag.FlagSet, o *serveOptions) {
		fs.StringVar(&o.hostname, "hostname", server.DefaultHost, "Host name or IP `address` to listen on")
		fs.IntVar(&o.port, "port", 4096, "Port to listen on, or 0 for a free one")
		fs.Func("cors", "Let browser pages of this `origin`, such as https://app.example, call the API (repeatable)", func(s string) error {
			origin, err := parseOrigin(s)
			if err != nil {
				return err
			}
			o.cors = append(o.cors, origin)
			return nil
		})
	},
	Env: []cmdline.EnvVar{
		{Name: config.EnvServerPassword, Usage: "Password to ask every request for, by HTTP basic authentication"},
		{Name: config.EnvServerUsername, Usage: "User name that goes with the password (default flow)"},
	},
	Run: func(ctx context.Context, o serveOptions, args []string) error {
		// A signal also stops the app's work in progress.
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return withApp(runServer)(ctx, o, args)
	},
}

// parseOrigin returns s as a browser origin, a scheme and a host with no
// path, as an Origin header names a page's, or why it is not one.
func parseOrigin(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("%q is not an origin such as https://app.example", s)
	}
	return u.Scheme + "://" + u.Host, nil
}

// runServer serves the HTTP API of a on the address o names until ctx is
// done. A request that names no model goes to the default model in the
// config file, which PATCH /config sets. It warns on w when the server is
// reachable from other machines with no password.
func runServer(ctx context.Context, w io.Writer, a *app.App, o serveOptions, _ []string) error {
	username, password := config.ServerCredentials()
	opts := server.Options{Hostname: o.hostname, Port: o.port, CORS: o.cors, Username: username, Password: password}
	if !opts.Loopback() && password == "" {
		fmt.Fprintf(w, "warning: %s is not set, so anyone who reaches %s can run commands on this machine\n", config.EnvServerPassword, opts.Host())
	}
	return server.ListenAndServe(ctx, a, opts)
}
