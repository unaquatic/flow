// Package catalog holds the model catalog compiled into flow, and makes the
// gateway.Store flow reads its catalog server with, which falls back to it.
package catalog

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/unaquatic/catalog/gateway"
)

// snapshot is the catalog compiled into flow, of the built-in providers
// alone, for a cold start with no cache and no server: the catalog of the
// catalog module version go.mod requires, which go run builds the catalog
// command at. Regenerate it with go generate after a change of that
// version, as go get github.com/unaquatic/catalog@latest makes.
//
//go:generate go run github.com/unaquatic/catalog/cmd/catalog snapshot -providers anthropic,openai -o snapshot.json
//go:embed snapshot.json
var snapshot []byte

// Snapshot returns the catalog compiled into flow.
var Snapshot = sync.OnceValue(func() *gateway.Catalog {
	c, err := gateway.Decode(snapshot)
	if err != nil {
		// The snapshot is compiled in, so only a broken build gets here.
		panic(fmt.Sprintf("catalog: embedded snapshot: %v", err))
	}
	return c
})

// NewStore returns a gateway.Store configured by opts, which falls back to
// Snapshot unless opts names a snapshot of its own.
func NewStore(opts gateway.StoreOptions) *gateway.Store {
	if opts.Snapshot == nil {
		opts.Snapshot = Snapshot
	}
	return gateway.NewStore(opts)
}
