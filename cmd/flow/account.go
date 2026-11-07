package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
)

var accountCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "account",
	Short: "List and switch provider accounts",
	Run: withApp(func(ctx context.Context, w io.Writer, a *app.App, _ cmdline.NoOptions, _ []string) error {
		return listAccounts(ctx, w, a)
	}),
	Commands: []cmdline.Node{accountUseCmd},
}

var accountUseCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "use",
	Usage: "<provider> <account-id>",
	Short: "Make an account the active one of its provider",
	Args:  cmdline.ExactArgs(2),
	Run:   withApp(runAccountUse),
}

// runAccountUse makes the account that args name, a provider and an account
// ID, the active one of its provider in a, and writes it to w.
func runAccountUse(ctx context.Context, w io.Writer, a *app.App, _ cmdline.NoOptions, args []string) error {
	var t agent.ProviderID
	if err := (providerValue{id: &t}).Set(args[0]); err != nil {
		return err
	}
	id, err := agent.ParseAccountID(args[1])
	if err != nil {
		return err
	}
	if _, err := a.Accounts().Use(ctx, t, id); err != nil {
		return err
	}
	fmt.Fprintf(w, "Active %s account: %s.\n", t, id)
	return nil
}

// listAccounts writes the accounts of every provider of a to w.
func listAccounts(ctx context.Context, w io.Writer, a *app.App) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	providers := a.Accounts().Providers()
	slices.Sort(providers)
	for _, t := range providers {
		st, err := a.Accounts().Status(ctx, t)
		if err != nil {
			return err
		}
		fmt.Fprintln(tw, t)
		if len(st.Accounts) == 0 {
			fmt.Fprintln(tw, "   (no accounts; run "+product.Name+" login)")
		}
		for _, acct := range st.Accounts {
			mark := " "
			if acct.ID == st.Active {
				mark = "*"
			}
			fmt.Fprintf(tw, " %s %s\t%s\t%s\n", mark, acct.ID, acct.Kind, acct.Source)
		}
	}
	return tw.Flush()
}
