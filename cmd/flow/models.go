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
	Short: "List the models of the signed-in providers, with their reasoning efforts",
	Flags: func(fs *flag.FlagSet, o *ModelsOptions) {
		fs.BoolVar(&o.Refresh, "refresh", false, "Download the model catalog first")
		formatFlag(fs, &o.Format)
	},
	Args: cmdline.MaxArgs(1),
	Run:  withApp(runModels),
}

// runModels writes to w the models of each signed-in provider, or of the
// provider args names, as Models.Listings lists them, after downloading
// the catalog with o.Refresh: in OutputText, a row for each variant, as
// writeModelRows writes it; in OutputJSON, as the HTTP API lists them. It
// fails with errNoModels when it has no row to write.
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

// writeModelRows writes rows to w, a line each, in columns as wide as their
// widest value: the provider, as --provider takes it, the model, as --model
// takes it at that provider, and the reasoning efforts --effort takes for
// it, when it takes any.
func writeModelRows(w io.Writer, rows []ModelRow) error {
	providers, models := 0, 0
	for _, r := range rows {
		providers = max(providers, len(r.Provider))
		models = max(models, len(r.Model))
	}
	for _, r := range rows {
		line := ui.PadWidth(string(r.Provider), providers) + "  " + r.Model
		if len(r.Efforts) > 0 {
			line = ui.PadWidth(line, providers+2+models) + "  " + strings.Join(r.Efforts, ", ")
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// ModelRow is a row of flow models: a variant, its provider and its model
// apart, and the reasoning efforts --effort takes for its model.
type ModelRow struct {
	// Provider is the variant's provider.
	Provider agent.ProviderID
	// Model is the variant without its provider, in the form
	// agent.Variant.Ref writes, such as "lab-a/model-a:fast".
	Model string
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
				rows = append(rows, ModelRow{Provider: v.Provider, Model: v.Ref(), Efforts: efforts})
			}
		}
	}
	return rows
}
