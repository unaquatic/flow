package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
)

// loginOptions are the flags of the login command.
type loginOptions struct {
	method   loginMethod
	provider agent.ProviderID
	account  agent.AccountID
	// format is how the account logged in to is written. In OutputJSON,
	// the login's steps go to stderr.
	format OutputFormat
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
		formatFlag(fs, &o.format)
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
// key from in. In OutputText, it writes to w the login instructions, as
// writeLoginPrompt lays them out, that it waits while the login has no code
// to read, and that it logged in, as introLine draws it. In OutputJSON, it
// writes those steps to stderr, and the account it logged in to to w, as an
// AccountRef.
func login(ctx context.Context, w io.Writer, in io.Reader, a *app.App, o loginOptions) error {
	steps := w
	if o.format == OutputJSON {
		steps = stderr
	}
	if o.method == keyMethod {
		return saveKey(ctx, w, steps, in, a, o)
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
				writeLoginPrompt(steps, p.Prompt())
				if p.State == nil {
					fmt.Fprintln(steps, introLine("Waiting for authorization...", showsColor(steps)))
					continue
				}
				for {
					fmt.Fprint(steps, introLine("Authorization code:", showsColor(steps))+" ")
					code, err := input.ReadString('\n')
					if err != nil && (!errors.Is(err, io.EOF) || code == "") {
						return fmt.Errorf("read authorization code: %w", err)
					}
					if err := a.Accounts().CompleteLogin(ctx, p.Provider, p.Account, *p.State, code); err != nil {
						if errors.Is(err, agent.ErrInvalidLoginCode) {
							fmt.Fprintln(steps, err)
							continue
						}
						return err
					}
					break
				}
			case app.EventLoginSucceeded:
				if p.Provider == o.provider && p.Account == id {
					return loggedIn(ctx, w, a, o, id)
				}
			case app.EventLoginFailed:
				if p.Provider == o.provider && p.Account == id && !p.Retry {
					return errors.New(p.Error.Message)
				}
			}
		}
	}
}

// loggedIn writes to w that the login of o saved account id: in
// OutputText, as introLine draws it; in OutputJSON, the account as an
// AccountRef, with its kind and source as a holds them.
func loggedIn(ctx context.Context, w io.Writer, a *app.App, o loginOptions, id agent.AccountID) error {
	if o.format != OutputJSON {
		_, err := fmt.Fprintf(w, "\n%s\n", introLine(loggedInText(o), showsColor(w)))
		return err
	}
	acct := agent.AccountStatus{ID: id}
	st, err := a.Accounts().Status(ctx, o.provider)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(st.Accounts, func(s agent.AccountStatus) bool { return s.ID == id }); i >= 0 {
		acct = st.Accounts[i]
	}
	return writeJSON(w, newAccountRef(o.provider, acct))
}

// loggedInText is what login says once the login of o is done.
func loggedInText(o loginOptions) string {
	if o.method == keyMethod {
		return "API key saved"
	}
	return "Logged in successfully"
}

// writeLoginPrompt writes to w what p asks the user to do, a blank line
// after each step: the page to open, as writeURLField lays it out, and the
// code to enter there when p has one, as writeField does.
func writeLoginPrompt(w io.Writer, p agent.LoginPrompt) {
	writeURLField(w, "Open this URL in your browser:", p.URL)
	fmt.Fprintln(w)
	if p.Code != "" {
		writeField(w, "Enter this code in your browser:", p.Code)
		fmt.Fprintln(w)
	}
}

// saveKey asks on steps for an API key, bold as introLine draws it, reads
// it from in and saves it as the account of o, or as a new account when o
// names none, and writes that it saved it, as loggedIn writes it to w. The
// key is echoed as typed, after the question.
func saveKey(ctx context.Context, w, steps io.Writer, in io.Reader, a *app.App, o loginOptions) error {
	fmt.Fprint(steps, introLine("Enter the API key:", showsColor(steps))+" ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read API key: %w", err)
	}
	id, err := a.Accounts().SetKey(ctx, o.provider, o.account, agent.APIKey(line))
	if err != nil {
		return err
	}
	return loggedIn(ctx, w, a, o, id)
}
