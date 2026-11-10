package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/terminal/ui"
)

// ModelsOptions are the flags of flow models.
type ModelsOptions struct {
	// Refresh downloads the model catalog before the models are listed.
	Refresh bool
	// Format is how the models are written.
	Format OutputFormat
}

// errNoModels is the error of flow models when no signed-in provider has a
// model to list.
var errNoModels = errors.New("no models found: log in to a provider with flow login, or download the catalog with --refresh")

// modelsCmd lists the models that --model names.
var modelsCmd = &cmdline.Command[ModelsOptions]{
	Name:  "models",
	Usage: "[provider]",
	Short: "List the models of the signed-in providers, as --model names them",
	Flags: func(fs *flag.FlagSet, o *ModelsOptions) {
		fs.BoolVar(&o.Refresh, "refresh", false, "Download the model catalog first")
		formatFlag(fs, &o.Format)
	},
	Args: cmdline.MaxArgs(1),
	Run:  withApp(runModels),
}

// runModels writes to w the models of each signed-in provider, or of the
// provider args names, as Models.Listings lists them, after downloading
// the catalog with o.Refresh: in OutputText, a row for each variant, its
// name as --model takes it and the reasoning efforts --effort takes for it;
// in OutputJSON, as the HTTP API lists them. It fails with errNoModels when
// it has no row to write.
func runModels(ctx context.Context, w io.Writer, a *app.App, o ModelsOptions, args []string) error {
	var provider agent.ProviderID
	if len(args) == 1 {
		provider = agent.ProviderID(args[0])
	}
	if o.Refresh {
		if err := a.Models().Refresh(ctx); err != nil {
			return fmt.Errorf("refresh the model catalog: %w", err)
		}
	}
	listings, err := a.Models().Listings(ctx, provider)
	if err != nil {
		return err
	}
	if o.Format == OutputJSON {
		return writeJSON(w, listings)
	}
	rows := modelRows(listings)
	if len(rows) == 0 {
		return errNoModels
	}
	return writeModelRows(w, rows)
}

// writeModelRows writes rows to w, a line each: its name, and its efforts
// after it, in a column of their own.
func writeModelRows(w io.Writer, rows []ModelRow) error {
	width := 0
	for _, r := range rows {
		width = max(width, len(r.Name))
	}
	for _, r := range rows {
		line := r.Name
		if len(r.Efforts) > 0 {
			line = ui.PadWidth(r.Name, width) + "  " + strings.Join(r.Efforts, ", ")
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// ModelRow is a row of flow models: a variant, as --model names it, and
// the reasoning efforts --effort takes for its model.
type ModelRow struct {
	// Name is the variant in the form agent.Variant.String writes.
	Name string
	// Efforts are the model's reasoning efforts, in the catalog's order.
	Efforts []string
}

// modelRows returns a row for each variant of each model of listings, in
// their order.
func modelRows(listings []app.ModelListing) []ModelRow {
	var rows []ModelRow
	for _, l := range listings {
		var efforts []string
		for _, m := range l.Models {
			efforts = efforts[:0:0]
			for _, e := range l.ReasoningEfforts[m.Model] {
				efforts = append(efforts, string(e))
			}
			for _, v := range m.Variants(l.Provider) {
				rows = append(rows, ModelRow{Name: v.String(), Efforts: efforts})
			}
		}
	}
	return rows
}
