package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/signal"
	"strconv"
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
	// format is how the address the server listens at is written.
	format OutputFormat
}

var serveCmd = &cmdline.Command[serveOptions]{
	Name:  "serve",
	Short: "Run the HTTP API server",
	Flags: func(fs *flag.FlagSet, o *serveOptions) {
		fs.StringVar(&o.hostname, "hostname", server.DefaultHost, "Host name or IP `address` to listen on")
		fs.IntVar(&o.port, "port", 4096, "Port to listen on, or 0 for a free one")
		fs.Func("cors", "Allow API calls from this browser `origin` (repeatable)", func(s string) error {
			origin, err := parseOrigin(s)
			if err != nil {
				return err
			}
			o.cors = append(o.cors, origin)
			return nil
		})
		formatFlag(fs, &o.format)
	},
	Env: []cmdline.EnvVar{
		{Name: config.EnvServerPassword, Usage: "Password for HTTP basic authentication"},
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
// done, as serve does, on a TCP listener.
func runServer(ctx context.Context, w io.Writer, a *app.App, o serveOptions, _ []string) error {
	return serve(ctx, w, a, o, net.Listen)
}

// serve serves the HTTP API of a, as server.Serve does, on the listener that
// listen opens at the address o names, until ctx is done, once it has
// written to w the URL it listens at, with the port the listener picked
// when o names none: in OutputText, as writeURLField lays it out; in
// OutputJSON, as a ServeInfo on one line, which a script can read before
// the server ends. A request that names no model goes to the default model
// in the config file, which PATCH /config sets. It warns when the server is
// reachable from other machines with no password: on w in OutputText, and
// on stderr in OutputJSON.
func serve(ctx context.Context, w io.Writer, a *app.App, o serveOptions, listen func(network, address string) (net.Listener, error)) error {
	username, password := config.ServerCredentials()
	opts := server.Options{Hostname: o.hostname, Port: o.port, CORS: o.cors, Username: username, Password: password}
	if !opts.Loopback() && password == "" {
		warn := w
		if o.format == OutputJSON {
			warn = stderr
		}
		fmt.Fprintf(warn, "warning: %s is not set, so anyone who reaches %s can run commands on this machine\n", config.EnvServerPassword, opts.Host())
	}
	ln, err := listen("tcp", net.JoinHostPort(opts.Host(), strconv.Itoa(opts.Port)))
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	if o.format == OutputJSON {
		data, err := json.Marshal(ServeInfo{URL: url})
		if err != nil {
			return errors.Join(err, ln.Close())
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return errors.Join(err, ln.Close())
		}
	} else {
		writeURLField(w, "Server listening on:", url)
	}
	return server.Serve(ctx, a, opts, ln)
}

// ServeInfo is where flow serve listens, as it writes it in OutputJSON.
type ServeInfo struct {
	// URL is the server's base URL, with the port the listener picked.
	URL string `json:"url"`
}
