package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
)

// loginOptions are the flags of the login command.
type loginOptions struct {
	method   loginMethod
	provider agent.ProviderID
	account  agent.AccountID
}

// loginMethod is how the login command authenticates: an
// [agent.LoginMethod], or keyMethod to enter an API key.
type loginMethod string

// keyMethod reads an API key from standard input instead of logging in.
const keyMethod loginMethod = "key"

// MarshalText returns m as text.
func (m *loginMethod) MarshalText() ([]byte, error) {
	return []byte(*m), nil
}

// UnmarshalText sets m to keyMethod or to the agent.LoginMethod that text
// names.
func (m *loginMethod) UnmarshalText(text []byte) error {
	if loginMethod(text) == keyMethod {
		*m = keyMethod
		return nil
	}
	method, err := agent.ParseLoginMethod(string(text))
	if err != nil {
		return err
	}
	*m = loginMethod(method)
	return nil
}

var loginCmd = &cmdline.Command[loginOptions]{
	Name:  "login",
	Short: "Authenticate with a provider account",
	Flags: func(fs *flag.FlagSet, o *loginOptions) {
		const usage = "Login `method`: browser, device, or key"
		fs.TextVar(&o.method, "method", new(loginMethod(agent.LoginBrowser)), usage)
		fs.TextVar(&o.method, "m", new(loginMethod(agent.LoginBrowser)), usage)
		providerFlag(fs, &o.provider, "Provider `name` to authenticate with (required)")
		accountFlag(fs, &o.account, "Account `ID` to log in to again; omit to add a new account")
	},
	Run: func(ctx context.Context, o loginOptions, args []string) error {
		if o.provider == "" {
			return errProviderRequired
		}
		return withApp(runLogin)(ctx, o, args)
	},
}

// runLogin logs in to the account that o names in a, as login does, reading
// from standard input.
func runLogin(ctx context.Context, w io.Writer, a *app.App, o loginOptions, _ []string) error {
	return login(ctx, w, os.Stdin, a, o)
}

// login logs in to the account that o names in a, or saves an API key for
// it, and makes the account active. It reads the authorization code or the
// key from in, and writes the login instructions and the outcome to w.
func login(ctx context.Context, w io.Writer, in io.Reader, a *app.App, o loginOptions) error {
	if o.method == keyMethod {
		return saveKey(ctx, w, in, a, o.provider, o.account)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := a.Subscribe(ctx)
	id, err := a.Accounts().StartLogin(o.provider, o.account, agent.LoginMethod(o.method))
	if err != nil {
		return err
	}
	input := bufio.NewReader(in)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-events:
			switch p := ev.(type) {
			case app.EventLoginPrompt:
				if p.Provider != o.provider || p.Account != id {
					continue
				}
				fmt.Fprintln(w, p.Prompt())
				if p.State == nil {
					continue
				}
				for {
					fmt.Fprint(w, "Authorization code: ")
					code, err := input.ReadString('\n')
					if err != nil && (!errors.Is(err, io.EOF) || code == "") {
						return fmt.Errorf("read authorization code: %w", err)
					}
					if err := a.Accounts().CompleteLogin(ctx, p.Provider, p.Account, *p.State, code); err != nil {
						if errors.Is(err, agent.ErrInvalidLoginCode) {
							fmt.Fprintln(w, err)
							continue
						}
						return err
					}
					break
				}
			case app.EventLoginSucceeded:
				if p.Provider == o.provider && p.Account == id {
					fmt.Fprintf(w, "Logged in to %s account %s; it is now active.\n", o.provider, id)
					return nil
				}
			case app.EventLoginFailed:
				if p.Provider == o.provider && p.Account == id && !p.Retry {
					return errors.New(p.Error.Message)
				}
			}
		}
	}
}

// saveKey asks on w for an API key, reads it from in and saves it as account
// id of provider t, or as a new account when id is empty. The key is echoed
// as typed.
func saveKey(ctx context.Context, w io.Writer, in io.Reader, a *app.App, t agent.ProviderID, id agent.AccountID) error {
	fmt.Fprintf(w, "Enter the %s API key: ", t)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read API key: %w", err)
	}
	if id, err = a.Accounts().SetKey(ctx, t, id, agent.APIKey(line)); err != nil {
		return err
	}
	fmt.Fprintf(w, "Saved the API key as %s account %s; it is now active.\n", t, id)
	return nil
}
