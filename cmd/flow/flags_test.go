package main

import (
	"errors"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/fluid/style"
)

func TestProviderValue(t *testing.T) {
	tests := []struct {
		name    string
		want    agent.ProviderID
		wantErr bool
	}{
		{name: "provider-a", want: "provider-a"},
		{name: "provider-b", want: "provider-b"},
		// Any catalog provider parses; the app checks that it is registered.
		{name: "provider-c", want: "provider-c"},
		{name: "", wantErr: true},
	}
	for _, tt := range tests {
		var got agent.ProviderID
		err := providerValue{id: &got}.Set(tt.name)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("set %q = %q, %v; want %q, error %v", tt.name, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestModelFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    modelOptions
		wantErr string
	}{
		{name: "none"},
		{name: "pattern", args: []string{"--model", "small"}, want: modelOptions{model: "small"}},
		{name: "provider", args: []string{"--provider", "provider-a", "-m", "model-a"},
			want: modelOptions{provider: "provider-a", model: "model-a"}},
		// -p is --print's, on the commands that take the model flags.
		{name: "no -p", args: []string{"-p", "provider-a"}, wantErr: "flag provided but not defined: -p"},
		{name: "gateway provider", args: []string{"--provider", "provider-c", "-m", "large"},
			want: modelOptions{provider: "provider-c", model: "large"}},
		{name: "empty provider", args: []string{"--provider", " "}, wantErr: "empty provider"},
		{name: "variant and level", args: []string{"-m", "large:fast:low", "--effort", "XHIGH"},
			want: modelOptions{model: "large:fast:low", effort: "xhigh"}},
		{name: "level alone", args: []string{"--effort", "none"}, want: modelOptions{effort: "none"}},
		// Any name parses: the catalog decides which levels there are.
		{name: "level flow does not know", args: []string{"--effort", "XXXHigh"}, want: modelOptions{effort: "xxxhigh"}},
		{name: "empty level", args: []string{"--effort", " "}, wantErr: "empty reasoning effort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o modelOptions
			fs := flag.NewFlagSet("flow", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			modelFlags(fs, &o)
			err := fs.Parse(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parse %q: err = %v, want one containing %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil || o != tt.want {
				t.Errorf("parse %q = %+v, %v; want %+v", tt.args, o, err, tt.want)
			}
		})
	}
}

// TestColorFlags checks that --color takes auto and the names FLUID_COLOR
// takes, in any case, defaults to auto, and that auto and the zero colorFlag
// draw in the detected color mode.
func TestColorFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		want     colorFlag
		wantMode style.ColorMode
		wantErr  string
	}{
		{name: "default", want: colorAuto, wantMode: colorMode},
		{name: "auto", args: []string{"--color", "auto"}, want: colorAuto, wantMode: colorMode},
		{name: "truecolor", args: []string{"--color", "TrueColor"}, want: "truecolor", wantMode: style.ColorTrueColor},
		{name: "24bit", args: []string{"--color=24bit"}, want: "24bit", wantMode: style.ColorTrueColor},
		{name: "256", args: []string{"--color", "256"}, want: "256", wantMode: style.Color256},
		{name: "16", args: []string{"-color", " 16 "}, want: "16", wantMode: style.Color16},
		{name: "last value wins", args: []string{"--color", "16", "--color", "256"}, want: "256", wantMode: style.Color256},
		{name: "unknown", args: []string{"--color", "never"}, wantErr: "unknown color mode: never; use auto, truecolor, 256 or 16"},
		{name: "empty", args: []string{"--color="}, wantErr: "unknown color mode: ; use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got colorFlag
			fs := flag.NewFlagSet("flow", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			colorFlags(fs, &got)
			if def := fs.Lookup("color").DefValue; def != "auto" {
				t.Errorf("default = %q, want auto", def)
			}
			err := fs.Parse(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parse %q: err = %v, want one containing %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parse %q = %q, %v; want %q", tt.args, got, err, tt.want)
			}
			if mode := got.mode(); mode != tt.wantMode {
				t.Errorf("mode of %q = %v, want %v", got, mode, tt.wantMode)
			}
		})
	}
	if mode := colorFlag("").mode(); mode != colorMode {
		t.Errorf("mode of the zero colorFlag = %v, want the detected %v", mode, colorMode)
	}
}

// TestSelectModelChecksFlags checks the flags that selectModel rejects or
// ignores before it looks up a model, which needs no app.
func TestSelectModelChecksFlags(t *testing.T) {
	if model, err := selectModel(t.Context(), nil, modelOptions{}); model != "" || err != nil {
		t.Errorf("no flags: %q, %v, want no model", model, err)
	}
	_, err := selectModel(t.Context(), nil, modelOptions{provider: "provider-a"})
	if err == nil || err.Error() != "--provider needs --model" {
		t.Errorf("provider alone: err = %v, want --provider needs --model", err)
	}
}

func TestLoginFlags(t *testing.T) {
	const id = "01a11ff1-50e6-7a76-8b37-44931fa04af6"
	tests := []struct {
		name    string
		args    []string
		want    loginOptions
		wantErr string
	}{
		{name: "defaults, with no provider",
			want: loginOptions{method: loginMethod(agent.LoginBrowser), format: OutputText}},
		{name: "device", args: []string{"-m", "device", "-p", "provider-a"},
			want: loginOptions{method: loginMethod(agent.LoginDevice), provider: "provider-a", format: OutputText}},
		{name: "API key", args: []string{"-method", "key"},
			want: loginOptions{method: keyMethod, format: OutputText}},
		{name: "JSON", args: []string{"-method", "key", "--format", "json"},
			want: loginOptions{method: keyMethod, format: OutputJSON}},
		{name: "account", args: []string{"-account", strings.ToUpper(id)},
			want: loginOptions{method: loginMethod(agent.LoginBrowser), account: id, format: OutputText}},
		{name: "unknown method", args: []string{"-method", "fax"}, wantErr: "unknown login method: fax"},
		{name: "empty provider", args: []string{"-provider", ""}, wantErr: "empty provider"},
		{name: "bad account", args: []string{"-a", "nope"}, wantErr: "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o loginOptions
			fs := flag.NewFlagSet("login", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			loginCmd.Flags(fs, &o)
			err := fs.Parse(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parse %q: err = %v, want one containing %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil || o != tt.want {
				t.Errorf("parse %q = %+v, %v; want %+v", tt.args, o, err, tt.want)
			}
		})
	}
}

// TestProviderRequired checks that login and logout fail without
// --provider, since flow chooses no provider for them, before they start
// the app.
func TestProviderRequired(t *testing.T) {
	if err := loginCmd.Run(t.Context(), loginOptions{}, nil); !errors.Is(err, errProviderRequired) {
		t.Errorf("login without a provider = %v, want %v", err, errProviderRequired)
	}
	if err := logoutCmd.Run(t.Context(), logoutOptions{account: "01a11ff1-50e6-7a76-8b37-44931fa04af6"}, nil); !errors.Is(err, errProviderRequired) {
		t.Errorf("logout without a provider = %v, want %v", err, errProviderRequired)
	}
}

// TestListValue checks that a list flag splits each use at commas and
// spaces, adds each use to the list, and that a use with no names sets an
// empty list, apart from the nil list of a flag not given.
func TestListValue(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"--tools", ""}, []string{}},
		{[]string{"--tools", "read,grep", "--tools", "ls  bash,"}, []string{"read", "grep", "ls", "bash"}},
	} {
		var got []string
		fs := flag.NewFlagSet("flow", flag.ContinueOnError)
		fs.Var(listValue{list: &got}, "tools", "")
		if err := fs.Parse(tt.args); err != nil {
			t.Fatal(err)
		}
		if (got == nil) != (tt.want == nil) || !slices.Equal(got, tt.want) {
			t.Errorf("parse %q = %#v, want %#v", tt.args, got, tt.want)
		}
	}
}

// TestPositiveValue checks that a positive number flag takes a number above
// zero alone.
func TestPositiveValue(t *testing.T) {
	var n int
	v := positiveValue{n: &n}
	if err := v.Set("4"); err != nil || n != 4 {
		t.Errorf("Set(4) = %v, n = %d; want 4", err, n)
	}
	for _, s := range []string{"0", "-1", "x", ""} {
		if err := v.Set(s); err == nil {
			t.Errorf("Set(%q): no error", s)
		}
	}
}
