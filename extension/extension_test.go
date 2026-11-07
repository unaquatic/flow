package extension

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
)

// named is an extension that implements no hook.
type named string

func (n named) Name() string { return string(n) }

// prompter is an extension that implements PromptHook.
type prompter struct{ named }

func (prompter) OnPrompt(context.Context, Prompt, agent.PartSink) {}

// TestHooks checks that Hooks yields the extensions that implement a hook,
// in order, and Find the first of a type.
func TestHooks(t *testing.T) {
	exts := []Extension{named("a"), prompter{"b"}, named("c"), prompter{"d"}}
	var got []string
	for h := range Hooks[PromptHook](exts) {
		got = append(got, h.Name())
	}
	if want := []string{"b", "d"}; !slices.Equal(got, want) {
		t.Errorf("prompt hooks = %v, want %v", got, want)
	}
	if _, ok := Find[MoveHook](exts); ok {
		t.Error("Find found a move hook among extensions that implement none")
	}
	if p, ok := Find[prompter](exts); !ok || p.Name() != "b" {
		t.Errorf("Find = %v, %v; want the first prompter", p, ok)
	}
}

// TestCoreImportsNoExtension checks that no package of the module outside
// cmd and this package's tree imports an extension, so that the app and its
// clients name none, and cmd/flow chooses the ones flow is built with. Tests
// may load one, as the package doc says.
func TestCoreImportsNoExtension(t *testing.T) {
	root := os.DirFS("..")
	mod, err := fs.ReadFile(root, "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var module string
	for line := range strings.Lines(string(mod)) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.TrimSpace(path)
			break
		}
	}
	if module == "" {
		t.Fatal("go.mod names no module")
	}
	extensions := module + "/extension/"

	err = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == "extension" || path == "cmd" || d.Name() == "testdata" ||
				path != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(imported, extensions) {
				t.Errorf("%s imports %s, which only cmd and other extensions may", path, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRefusal checks that a refusal reads as its reason, and matches both
// itself and ErrRefused, also when wrapped.
func TestRefusal(t *testing.T) {
	var errNope error = Refusal("nope")
	err := fmt.Errorf("move: %w", errNope)
	if !errors.Is(err, errNope) || !errors.Is(err, ErrRefused) {
		t.Errorf("errors.Is(%v) does not match the refusal and ErrRefused", err)
	}
	if errNope.Error() != "nope" {
		t.Errorf("refusal reads %q, want its reason", errNope)
	}
	if errors.Is(errors.New("nope"), ErrRefused) {
		t.Error("an error that is no refusal matches ErrRefused")
	}
}
