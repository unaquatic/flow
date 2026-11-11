package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
)

// logoutOptions are the flags of the logout command.
type logoutOptions struct {
	provider agent.ProviderID
	account  agent.AccountID
	// format is how the removed account is written.
	format OutputFormat
}

var logoutCmd = &cmdline.Command[logoutOptions]{
	Name:  "logout",
	Short: "Remove a provider account",
	Flags: func(fs *flag.FlagSet, o *logoutOptions) {
		providerFlag(fs, &o.provider, "Provider `name` to log out from (required)")
		accountFlag(fs, &o.account, "`ID` of the account to remove (required)")
		formatFlag(fs, &o.format)
	},
	Run: func(ctx context.Context, o logoutOptions, args []string) error {
		if o.provider == "" {
			return errProviderRequired
		}
		if o.account == "" {
			return fmt.Errorf("--account is required; list account IDs with %s account", product.Name)
		}
		return withApp(runLogout)(ctx, o, args)
	},
}

// runLogout removes the account that o names from a, and writes to w that
// it did: in OutputText, as introLine draws it, and under it the account's
// row, as writeAccountRow writes it, its ID highlighted; in OutputJSON, as
// an AccountRef.
func runLogout(ctx context.Context, w io.Writer, a *app.App, o logoutOptions, _ []string) error {
	// The account's kind and source go with it, so read them first.
	acct := agent.AccountStatus{ID: o.account}
	if st, err := a.Accounts().Status(ctx, o.provider); err == nil {
		if i := slices.IndexFunc(st.Accounts, func(s agent.AccountStatus) bool { return s.ID == o.account }); i >= 0 {
			acct = st.Accounts[i]
		}
	}
	if _, err := a.Accounts().Logout(ctx, o.provider, o.account); err != nil {
		return err
	}
	if o.format == OutputJSON {
		return writeJSON(w, newAccountRef(o.provider, acct))
	}
	fmt.Fprintln(w, introLine("Removed:", showsColor(w)))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	writeAccountRow(tw, o.provider, acct, highlighter(w))
	return tw.Flush()
}
