package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
)

// modelOptions are the flags that pick the model of the commands that start
// the agent.
type modelOptions struct {
	// provider limits model to the models of one provider.
	provider agent.ProviderID
	// model is a model ID, a provider/id, or a pattern of one, which may
	// name a catalog variant with a ":option" suffix, such as ":fast", and
	// a reasoning effort with an ":effort" one.
	model string
	// effort is the reasoning effort, which overrides the effort model
	// names, or empty to keep it.
	effort agent.ReasoningEffort
}

// modelFlags binds --provider, --model and --effort to o. --provider has
// no -p, which is --print's.
func modelFlags(fs *flag.FlagSet, o *modelOptions) {
	fs.Var(providerValue{id: &o.provider}, "provider", "Model provider `name` to choose from")
	const modelUsage = "Model ID or `pattern` to use, such as name:fast:high"
	fs.StringVar(&o.model, "model", "", modelUsage)
	fs.StringVar(&o.model, "m", "", modelUsage)
	fs.Var(effortValue{effort: &o.effort}, "effort",
		"Reasoning effort `level` the model lists, such as low, medium or high (default: the provider's)")
}

// selectModel returns the model variant of a that o picks, as
// Models.Select selects it, in the form agent.Variant.String writes, or
// "" when o sets no flag, which leaves the model to the terminal. It fails
// when o names a provider without a model.
func selectModel(ctx context.Context, a *app.App, o modelOptions) (string, error) {
	switch {
	case o.model == "" && o.provider == "" && o.effort == "":
		return "", nil
	case o.model == "" && o.provider != "":
		return "", errors.New("--provider needs --model")
	}
	v, err := a.Models().Select(ctx, o.provider, o.model, o.effort)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// effortValue is the flag.Value of a reasoning effort, named as
// agent.ParseReasoningEffort reads it. Any name parses, as the catalog
// decides which efforts a model takes; Models.Select checks it against the
// model.
type effortValue struct {
	// effort is the variable the flag sets.
	effort *agent.ReasoningEffort
}

func (v effortValue) String() string {
	if v.effort == nil {
		return ""
	}
	return string(*v.effort)
}

// Set sets the reasoning effort named s, which must not be empty.
func (v effortValue) Set(s string) error {
	t := agent.ParseReasoningEffort(s)
	if t == "" {
		return errors.New("empty reasoning effort")
	}
	*v.effort = t
	return nil
}

// positiveValue is the flag.Value of a positive whole number.
type positiveValue struct {
	// n is the variable the flag sets.
	n *int
}

func (v positiveValue) String() string {
	if v.n == nil || *v.n == 0 {
		return ""
	}
	return strconv.Itoa(*v.n)
}

// Set sets the number s names, which must be above zero.
func (v positiveValue) Set(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return fmt.Errorf("%q is not a positive number", s)
	}
	*v.n = n
	return nil
}

// listValue is the flag.Value of a list of names, separated by commas or
// spaces, such as "read,grep". Each use of the flag adds to the list. Any
// use makes the list non-nil, so that one of an empty string, "", sets an
// empty list, which tells apart a flag given no names from one not given.
type listValue struct {
	// list is the variable the flag sets.
	list *[]string
}

func (v listValue) String() string {
	if v.list == nil {
		return ""
	}
	return strings.Join(*v.list, ",")
}

// Set adds the names of s to the list.
func (v listValue) Set(s string) error {
	names := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if *v.list == nil {
		*v.list = []string{}
	}
	*v.list = append(*v.list, names...)
	return nil
}

// pathsValue is the flag.Value of a list of paths, one per use of the
// flag, so that a path may hold commas and spaces.
type pathsValue struct {
	// paths is the variable the flag sets.
	paths *[]string
}

func (v pathsValue) String() string {
	if v.paths == nil {
		return ""
	}
	return strings.Join(*v.paths, ",")
}

// Set adds the path s, which must not be empty, to the list.
func (v pathsValue) Set(s string) error {
	if s == "" {
		return errors.New("empty path")
	}
	*v.paths = append(*v.paths, s)
	return nil
}

// colorFlag is the color mode that --color names: colorAuto, which detects
// the mode from the terminal, or a name in colorModes. The zero colorFlag
// detects it too.
type colorFlag string

// colorAuto detects the color mode from the terminal, as the terminal UI
// does when no flag sets it: from FLUID_COLOR, and then from TERM and the
// variables that terminals set.
const colorAuto colorFlag = "auto"

// colorModes maps the names --color takes, which FLUID_COLOR takes as well,
// to the color modes they name.
var colorModes = map[colorFlag]style.ColorMode{
	"truecolor": style.ColorTrueColor,
	"24bit":     style.ColorTrueColor,
	"256":       style.Color256,
	"16":        style.Color16,
}

// MarshalText returns c as text.
func (c colorFlag) MarshalText() ([]byte, error) {
	return []byte(c), nil
}

// UnmarshalText sets c to colorAuto or to the color mode that text names, in
// any case.
func (c *colorFlag) UnmarshalText(text []byte) error {
	name := colorFlag(strings.ToLower(strings.TrimSpace(string(text))))
	if _, ok := colorModes[name]; !ok && name != colorAuto {
		return fmt.Errorf("unknown color mode %q; use auto, truecolor, 256 or 16", text)
	}
	*c = name
	return nil
}

// mode returns the color mode that c names, or colorMode, the mode detected
// from the terminal, for colorAuto.
func (c colorFlag) mode() style.ColorMode {
	if m, ok := colorModes[c]; ok {
		return m
	}
	return colorMode
}

// colorFlags binds --color to c.
func colorFlags(fs *flag.FlagSet, c *colorFlag) {
	fs.TextVar(c, "color", new(colorAuto), "Color `mode` to draw in: auto, truecolor, 256 or 16")
}

// errProviderRequired is the error of a command that acts on a provider run
// without the flag that names it.
var errProviderRequired = errors.New("--provider is required")

// providerFlag binds the flag naming the provider to act on. It has no
// default: a command that acts on a provider fails with errProviderRequired
// without it.
func providerFlag(fs *flag.FlagSet, t *agent.ProviderID, usage string) {
	fs.Var(providerValue{id: t}, "provider", usage)
	fs.Var(providerValue{id: t}, "p", usage)
}

// accountFlag binds the flag holding the ID of the account to act on.
func accountFlag(fs *flag.FlagSet, id *agent.AccountID, usage string) {
	fs.TextVar(id, "account", new(agent.AccountID), usage)
	fs.TextVar(id, "a", new(agent.AccountID), usage)
}

// providerValue is the flag.Value of a provider, named by its catalog ID,
// such as provider-a. The app checks that the catalog lists it.
type providerValue struct {
	// id is the variable the flag sets.
	id *agent.ProviderID
}

func (v providerValue) String() string {
	if v.id == nil {
		return ""
	}
	return string(*v.id)
}

// Set sets the provider named s.
func (v providerValue) Set(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return errors.New("empty provider")
	}
	*v.id = agent.ProviderID(s)
	return nil
}
