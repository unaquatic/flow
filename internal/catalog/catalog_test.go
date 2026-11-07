package catalog

import (
	"maps"
	"slices"
	"testing"

	"github.com/unaquatic/catalog/gateway"
)

// TestSnapshot checks that the catalog compiled in decodes, as flow reads it
// on a cold start, with the built-in providers, BuiltIn's, each with models; that it
// names every offering as its model; and that it names exactly the labs and
// families of its models. That the catalog names its models from their IDs
// is the catalog module's to check.
func TestSnapshot(t *testing.T) {
	if !Embedded() {
		t.Skip("no snapshot compiled in: go generate ./internal/catalog writes " + SnapshotFile)
	}
	c := Snapshot()
	if c.Version != gateway.Version || len(c.Providers) == 0 {
		t.Fatalf("snapshot: version %d, %d providers", c.Version, len(c.Providers))
	}
	if got, want := slices.Sorted(maps.Keys(c.Providers)), BuiltIn(); !slices.Equal(got, want) {
		t.Errorf("snapshot providers %v, want BuiltIn's %v: keep the go:generate line and BuiltIn alike, and run go generate", got, want)
	}
	for pid, p := range c.Providers {
		if len(p.Models) == 0 {
			t.Errorf("snapshot: %s lists no models", pid)
		}
	}
	labs, families := make(map[gateway.ProviderID]bool), make(map[string]bool)
	for id, m := range c.Models {
		if m.Name == "" {
			t.Errorf("%s has no name", id)
		}
		labs[m.Lab] = true
		if m.Family != "" {
			families[m.Family] = true
		}
	}
	for pid, p := range c.Providers {
		for id, o := range p.Models {
			if o.Name != c.Models[id].Name {
				t.Errorf("%s/%s is named %q, want its model's %q", pid, id, o.Name, c.Models[id].Name)
			}
		}
	}
	if got, want := slices.Sorted(maps.Keys(c.Labs)), slices.Sorted(maps.Keys(labs)); !slices.Equal(got, want) {
		t.Errorf("labs %v, want its models' %v", got, want)
	}
	if got, want := slices.Sorted(maps.Keys(c.Families)), slices.Sorted(maps.Keys(families)); !slices.Equal(got, want) {
		t.Errorf("families %v, want its models' %v", got, want)
	}
}

// TestEmptySnapshot checks that a build without a snapshot falls back to an
// empty catalog that still decodes as one.
func TestEmptySnapshot(t *testing.T) {
	if Embedded() {
		t.Skip("a snapshot is compiled in")
	}
	if c := Snapshot(); c.Version != gateway.Version || len(c.Providers) != 0 || c.Models == nil {
		t.Errorf("snapshot without a file = %+v, want an empty catalog", c)
	}
}

// TestNewStore checks that a store with nothing cached returns the
// snapshot, and one given a snapshot of its own returns that.
func TestNewStore(t *testing.T) {
	if got := NewStore(gateway.StoreOptions{Dir: t.TempDir()}).Get(); got != Snapshot() {
		t.Error("a store with nothing cached does not return the snapshot")
	}
	own := &gateway.Catalog{Version: gateway.Version}
	if got := NewStore(gateway.StoreOptions{Dir: t.TempDir(), Snapshot: func() *gateway.Catalog { return own }}).Get(); got != own {
		t.Error("a store given a snapshot of its own does not return it")
	}
}
