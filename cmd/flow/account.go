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
	Commands: []cmdline.Node{accountCurrentCmd, accountUseCmd},
}

var accountCurrentCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "current",
	Usage: "[provider]",
	Short: "Show the active account of each provider, or of one",
	Args: func(args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("expects at most 1 argument, got %d", len(args))
		}
		return nil
	},
	Run: withApp(runAccountCurrent),
}

// runAccountCurrent writes to w the active account of the provider args
// name, or of each provider of a that has one when args name none. It fails
// when the named provider has no active account.
func runAccountCurrent(ctx context.Context, w io.Writer, a *app.App, _ cmdline.NoOptions, args []string) error {
	providers := a.Accounts().Providers()
	if len(args) == 1 {
		var t agent.ProviderID
		if err := (providerValue{id: &t}).Set(args[0]); err != nil {
			return err
		}
		providers = []agent.ProviderID{t}
	}
	slices.Sort(providers)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	shown := 0
	for _, t := range providers {
		st, err := a.Accounts().Status(ctx, t)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(st.Accounts, func(acct agent.AccountStatus) bool { return acct.ID == st.Active })
		if i < 0 {
			if len(args) == 1 {
				return fmt.Errorf("%s has no active account; run %s login", t, product.Name)
			}
			continue
		}
		acct := st.Accounts[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t, acct.ID, acct.Kind, acct.Source)
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(tw, "(no active accounts; run "+product.Name+" login)")
	}
	return tw.Flush()
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
