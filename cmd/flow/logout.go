package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
)

// logoutOptions are the flags of the logout command.
type logoutOptions struct {
	provider agent.ProviderID
	account  agent.AccountID
}

var logoutCmd = &cmdline.Command[logoutOptions]{
	Name:  "logout",
	Short: "Remove a provider account",
	Flags: func(fs *flag.FlagSet, o *logoutOptions) {
		providerFlag(fs, &o.provider, "Provider `name` to log out from (required)")
		accountFlag(fs, &o.account, "`ID` of the account to remove (required)")
	},
	Run: func(ctx context.Context, o logoutOptions, args []string) error {
		if o.provider == "" {
			return errProviderRequired
		}
		if o.account == "" {
			return errors.New("--account is required; list account IDs with " + product.Name + " account")
		}
		return withApp(runLogout)(ctx, o, args)
	},
}

// runLogout removes the account that o names from a, and writes to w which
// account of its provider is active now.
func runLogout(ctx context.Context, w io.Writer, a *app.App, o logoutOptions, _ []string) error {
	st, err := a.Accounts().Logout(ctx, o.provider, o.account)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Removed %s account %s.\n", o.provider, o.account)
	if st.Active != "" {
		fmt.Fprintf(w, "Active %s account: %s.\n", o.provider, st.Active)
	}
	return nil
}
