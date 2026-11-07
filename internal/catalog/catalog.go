// Package catalog holds the model catalog compiled into flow, and makes the
// gateway.Store flow reads its catalog server with, which falls back to it.
package catalog

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/unaquatic/catalog/gateway"
)

// SnapshotFile is the file, in the snapshot directory of this package,
// that go generate writes the snapshot to. Git ignores it.
const SnapshotFile = "snapshot/catalog.json"

// snapshot holds the catalog compiled into flow, of the built-in providers
// alone, for a cold start with no cache and no server: the catalog of the
// catalog module version go.mod requires, which go run builds the catalog
// command at, of the providers BuiltIn returns, which the go:generate line
// below names. Generate it with go generate before a release build, and
// after a change of that version, as go get
// github.com/unaquatic/catalog@latest makes. A build without it compiles in
// an empty snapshot.
//
//go:generate go run github.com/unaquatic/catalog/cmd/catalog snapshot -providers anthropic,openai,opencode-go -o snapshot/catalog.json
//go:embed all:snapshot
var snapshot embed.FS

// BuiltIn returns the providers flow is built with, in ID order: those the
// snapshot holds, as the go:generate line above names them, which the login
// menu lists whether or not they have a browser login. It returns a new
// slice on each call.
func BuiltIn() []gateway.ProviderID {
	return []gateway.ProviderID{"anthropic", "openai", "opencode-go"}
}

// Snapshot returns the catalog compiled into flow, or an empty catalog when
// the build compiled in none.
var Snapshot = sync.OnceValue(func() *gateway.Catalog {
	data, err := snapshot.ReadFile(SnapshotFile)
	if errors.Is(err, fs.ErrNotExist) {
		return &gateway.Catalog{
			Version:   gateway.Version,
			Models:    map[gateway.ModelID]gateway.Model{},
			Providers: map[gateway.ProviderID]gateway.Provider{},
		}
	}
	if err != nil {
		panic(fmt.Sprintf("catalog: embedded snapshot: %v", err))
	}
	c, err := gateway.Decode(data)
	if err != nil {
		// The snapshot is compiled in, so only a broken build gets here.
		panic(fmt.Sprintf("catalog: embedded snapshot: %v", err))
	}
	return c
})

// Embedded reports whether the build compiled in a snapshot.
func Embedded() bool {
	_, err := fs.Stat(snapshot, SnapshotFile)
	return err == nil
}

// NewStore returns a gateway.Store configured by opts, which falls back to
// Snapshot unless opts names a snapshot of its own.
func NewStore(opts gateway.StoreOptions) *gateway.Store {
	if opts.Snapshot == nil {
		opts.Snapshot = Snapshot
	}
	return gateway.NewStore(opts)
}
