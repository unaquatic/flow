package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/terminal/ui"
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
// name, or of each provider of a that has one when args name none: under
// "Current account:", or "Current accounts:" for more than one, as
// introLine draws it, a row each as writeAccountRow writes it, or that none
// has one. It fails when the named provider has no active account.
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
	type row struct {
		provider agent.ProviderID
		account  agent.AccountStatus
	}
	var rows []row
	for _, t := range providers {
		st, err := a.Accounts().Status(ctx, t)
		if err != nil {
			return err
		}
		acct, ok := activeAccount(st)
		if !ok {
			if len(args) == 1 {
				return fmt.Errorf("no active account: %s", t)
			}
			continue
		}
		rows = append(rows, row{t, acct})
	}
	heading := "Current account:"
	if len(rows) > 1 {
		heading = "Current accounts:"
	}
	fmt.Fprintln(w, introLine(heading, showsColor(w)))
	hl := highlighter(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		writeAccountRow(tw, r.provider, r.account, hl)
	}
	if len(rows) == 0 {
		fmt.Fprintln(tw, accountIndent+"[no active accounts]")
	}
	return tw.Flush()
}

// accountIndent indents the rows of the account commands, as ui.ResumeIndent
// indents the resume command.
var accountIndent = strings.Repeat(" ", ui.ResumeIndent)

// activeAccount returns the active account of st, and reports whether st
// has one.
func activeAccount(st agent.AuthStatus) (agent.AccountStatus, bool) {
	i := slices.IndexFunc(st.Accounts, func(acct agent.AccountStatus) bool { return acct.ID == st.Active })
	if i < 0 {
		return agent.AccountStatus{}, false
	}
	return st.Accounts[i], true
}

// writeAccountRow writes to tw account acct of provider t, as account
// current and account use show it: indented by accountIndent, the
// provider's ID, the account's ID through hl, its kind and its source, in
// tw's columns.
func writeAccountRow(tw io.Writer, t agent.ProviderID, acct agent.AccountStatus, hl func(string) string) {
	fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\n", accountIndent, t, hl(string(acct.ID)), acct.Kind, acct.Source)
}

var accountUseCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "use",
	Usage: "<provider> <account-id>",
	Short: "Make an account the active one of its provider",
	Args:  cmdline.ExactArgs(2),
	Run:   withApp(runAccountUse),
}

// runAccountUse makes the account that args name, a provider and an account
// ID, the active one of its provider in a, and writes to w that it switched
// to it, as introLine draws it, and under it its row, as writeAccountRow
// writes it, its ID highlighted.
func runAccountUse(ctx context.Context, w io.Writer, a *app.App, _ cmdline.NoOptions, args []string) error {
	var t agent.ProviderID
	if err := (providerValue{id: &t}).Set(args[0]); err != nil {
		return err
	}
	id, err := agent.ParseAccountID(args[1])
	if err != nil {
		return err
	}
	st, err := a.Accounts().Use(ctx, t, id)
	if err != nil {
		return err
	}
	acct, ok := activeAccount(st)
	if !ok {
		acct = agent.AccountStatus{ID: id}
	}
	fmt.Fprintln(w, introLine("Switched to:", showsColor(w)))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	writeAccountRow(tw, t, acct, highlighter(w))
	return tw.Flush()
}

// listAccounts writes the accounts of every provider of a to w, under the
// provider's ID, the active one marked with *, their IDs highlighted, or
// that the provider has none. Each ID takes the same escape codes, so the
// columns still line up.
func listAccounts(ctx context.Context, w io.Writer, a *app.App) error {
	hl := highlighter(w)
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
			fmt.Fprintln(tw, accountIndent+"[no accounts]")
		}
		for _, acct := range st.Accounts {
			mark := " "
			if acct.ID == st.Active {
				mark = "*"
			}
			fmt.Fprintf(tw, "%s %s\t%s\t%s\n", mark, hl(string(acct.ID)), acct.Kind, acct.Source)
		}
	}
	return tw.Flush()
}
