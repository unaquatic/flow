package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/unaquatic/catalog/gateway"
)

// ModelsFile is the name of the file in the data directory that adds
// providers and models to the catalog and changes the ones it lists.
const ModelsFile = "models.json"

// Document is the form of a models file: the providers of a catalog
// document, each written as a change to the catalog's provider of its ID,
// in the keys of the catalog document, as gateway.Provider has them.
//
// Objects merge into the catalog's, key by key, at any depth; any other
// value replaces the catalog's, and null removes it. A provider the catalog
// does not list is added, and needs an endpoint whose format is one of
// gateway.Formats and whose api is set. Under a provider:
//
//   - models holds offerings of the catalog's models, by model ID. An
//     offering the provider lacks starts from the model's facts, and the
//     reasoning of its lab's offering, and sends the model ID without its
//     lab, unless provider_options.id says otherwise.
//   - unlisted holds models the catalog does not know, by the provider's
//     model ID, which an offering sends unless provider_options.id says
//     otherwise.
//
// For example:
//
//	{
//	  "providers": {
//	    "local": {
//	      "endpoint": {"format": "<one of gateway.Formats>", "api": "http://local.example/v1", "auth": {"scheme": "bearer"}},
//	      "unlisted": {"model-x:large": {"limit": {"context": 131072, "output": 16384}}}
//	    }
//	  }
//	}
type Document struct {
	// Schema is the URL of a JSON Schema of the document, for editors.
	Schema string `json:"$schema,omitempty"`
	// Providers are the changes to the catalog's providers, by provider ID.
	Providers map[gateway.ProviderID]json.RawMessage `json:"providers"`
}

// Overlay applies the models file of a data directory to catalogs. It reads
// the file again only once it changes, and keeps the catalog it last made,
// so that applying it to the same catalog again costs a stat. An Overlay is
// safe for concurrent use.
type Overlay struct {
	// Dir is the data directory that holds ModelsFile.
	Dir string

	mu sync.Mutex
	// base and stamp are the catalog and the file as they were when the
	// rest was made from them; made reports that it was.
	base  *gateway.Catalog
	stamp fileStamp
	made  bool
	// out is base with the file merged into it, and err why the file did
	// not merge, when it did not, which leaves out base with the last
	// version that merged, good, merged into it.
	out *gateway.Catalog
	err error
	// good is the last version of the file that merged.
	good []byte
}

// fileStamp tells one version of a file from another. The zero fileStamp
// is a missing file's.
type fileStamp struct {
	mod  time.Time
	size int64
}

// NewOverlay returns the Overlay of data directory dir.
func NewOverlay(dir string) *Overlay {
	return &Overlay{Dir: dir}
}

// Path returns the path of the models file.
func (m *Overlay) Path() string {
	return filepath.Join(m.Dir, ModelsFile)
}

// Load returns c with the models file merged into it, as Merge merges it,
// or c itself when the file is missing or empty. It fails when the file
// cannot be read or merged, as flow is not to start without the providers
// and models it adds. The catalog it returns is shared and must not be
// changed.
func (m *Overlay) Load(c *gateway.Catalog) (*gateway.Catalog, error) {
	out, err, _ := m.load(c)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Apply returns c with the models file merged into it, as Load does. A
// version of the file that Load fails on, as an edit while flow runs may
// make, is logged once and left out: Apply merges the last version that
// merged in its place, or returns c when none has.
func (m *Overlay) Apply(c *gateway.Catalog) *gateway.Catalog {
	out, err, fresh := m.load(c)
	if err != nil && fresh {
		slog.Warn("models file not applied; using its last version that merged", "err", err)
	}
	return out
}

// load returns c with the models file merged into it, or, when the file
// does not merge, why, with c with the last version that merged merged into
// it. fresh reports that load read the file, rather than reusing what it
// made of the same file and catalog before.
func (m *Overlay) load(c *gateway.Catalog) (out *gateway.Catalog, err error, fresh bool) {
	st, err := m.stat()
	statted := err == nil
	m.mu.Lock()
	defer m.mu.Unlock()
	if statted && m.made && m.base == c && m.stamp == st {
		return m.out, m.err, false
	}
	var data []byte
	out = c
	if err == nil && st != (fileStamp{}) {
		data, err = os.ReadFile(m.Path())
		if err == nil && LegacyCache(data) {
			// Flow's old catalog cache, which no flow reads any more, is
			// in the way of the models file: remove it, and go on as
			// without one.
			err = os.Remove(m.Path())
			if err == nil || errors.Is(err, fs.ErrNotExist) {
				slog.Info("removed old catalog cache", "path", m.Path())
				data, err, st = nil, nil, fileStamp{}
			}
		}
		if err == nil {
			out, err = Merge(c, data)
		}
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", m.Path(), err)
		out = c
		if prev, perr := Merge(c, m.good); perr == nil {
			out = prev
		}
	} else {
		m.good = data
	}
	// A file that cannot be stat'ed has no stamp to tell when it changes,
	// so what it made is not kept.
	m.base, m.stamp, m.made, m.out, m.err = c, st, statted, out, err
	return out, err, true
}

// legacyCacheKeys are the keys of the catalog cache that flow kept in
// ModelsFile before it moved to gateway.StoreFile.
var legacyCacheKeys = []string{"etag", "fetched", "checked", "data"}

// LegacyCache reports whether data is the catalog cache that flow kept in
// ModelsFile before it moved to gateway.StoreFile: a JSON object holding
// data, and no keys but those of the cache.
func LegacyCache(data []byte) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil {
		return false
	}
	if _, ok := keys["data"]; !ok {
		return false
	}
	for k := range keys {
		if !slices.Contains(legacyCacheKeys, k) {
			return false
		}
	}
	return true
}

// stat returns the stamp of the models file, the zero one when it is
// missing.
func (m *Overlay) stat() (fileStamp, error) {
	info, err := os.Stat(m.Path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileStamp{}, nil
	case err != nil:
		return fileStamp{}, err
	}
	return fileStamp{mod: info.ModTime(), size: info.Size()}, nil
}

// Merge returns a copy of c with data, a models file in the form Document
// describes, merged into it. It returns c itself when data is empty. It
// fails, naming the key at fault, when data is not such a document, sets a
// key gateway.Provider or gateway.Offering does not have, offers a model
// the catalog does not list under models, or leaves a provider without an
// endpoint flow can call.
func Merge(c *gateway.Catalog, data []byte) (*gateway.Catalog, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return c, nil
	}
	var doc Document
	if err := decodeStrict(data, &doc); err != nil {
		return nil, err
	}
	out := *c
	out.Providers = maps.Clone(c.Providers)
	out.Models = maps.Clone(c.Models)
	if out.Providers == nil {
		out.Providers = make(map[gateway.ProviderID]gateway.Provider)
	}
	if out.Models == nil {
		out.Models = make(map[gateway.ModelID]gateway.Model)
	}
	for _, id := range slices.Sorted(maps.Keys(doc.Providers)) {
		if err := mergeProvider(&out, id, doc.Providers[id]); err != nil {
			return nil, at("providers."+string(id), err)
		}
	}
	return &out, nil
}

// PathError is an error of a models file at one of its keys.
type PathError struct {
	// Path is the key at fault, its parents' keys before it, joined by
	// dots, such as "providers.local.endpoint.api".
	Path string
	// Err is what is wrong with it.
	Err error
}

// Error returns the key and what is wrong with it.
func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }

// Unwrap returns Err.
func (e *PathError) Unwrap() error { return e.Err }

// at returns err as an error at key path, below which an error of a key
// below it already is, or err itself when path is empty.
func at(path string, err error) error {
	if path == "" {
		return err
	}
	if pe, ok := errors.AsType[*PathError](err); ok {
		return &PathError{Path: path + "." + pe.Path, Err: pe.Err}
	}
	return &PathError{Path: path, Err: err}
}

// mergeProvider merges raw, the change to provider id, into c's provider of
// that ID, or adds the provider, removing it when raw is null.
func mergeProvider(c *gateway.Catalog, id gateway.ProviderID, raw json.RawMessage) error {
	patch, err := decodeValue(raw)
	if err != nil {
		return at("", err)
	}
	if patch == nil {
		removeProvider(c, id)
		return nil
	}
	fields, ok := patch.(map[string]any)
	if !ok {
		return errors.New("not an object")
	}
	base, listed := c.Providers[id]
	if !listed {
		base = gateway.Provider{ID: id, Name: string(id), Kind: gateway.KindGateway, Type: gateway.ProviderCommunity}
	}
	models, unlisted := fields["models"], fields["unlisted"]
	delete(fields, "models")
	delete(fields, "unlisted")

	// Merge the provider's own keys, leaving its offerings as they are.
	own := base
	own.Models, own.Unlisted = nil, nil
	var p gateway.Provider
	if err := mergeInto(own, fields, &p); err != nil {
		return at("", err)
	}
	p.ID = id
	p.Models, p.Unlisted = maps.Clone(base.Models), maps.Clone(base.Unlisted)
	if p.Models == nil {
		p.Models = make(map[gateway.ModelID]gateway.Offering)
	}

	if err := mergeModels(c, &p, models); err != nil {
		return at("models", err)
	}
	if err := mergeUnlisted(&p, unlisted); err != nil {
		return at("unlisted", err)
	}
	if !slices.Contains(gateway.Formats, p.Endpoint.Format) {
		return at("endpoint.format", fmt.Errorf("%q is none of %v", p.Endpoint.Format, gateway.Formats))
	}
	if p.Endpoint.API == "" {
		return at("endpoint.api", errors.New("not set"))
	}
	c.Providers[id] = p
	return nil
}

// mergeModels merges patch, the models of a provider's change, into the
// offerings of p, a provider of c, keeping the providers c lists for each
// model up to date.
func mergeModels(c *gateway.Catalog, p *gateway.Provider, patch any) error {
	switch patch := patch.(type) {
	case nil:
		return nil
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(patch)) {
			id := gateway.ModelID(strings.ToLower(key))
			if patch[key] == nil {
				delete(p.Models, id)
				setOffered(c, id, p.ID, false)
				continue
			}
			off, ok := p.Models[id]
			if !ok {
				m, known := c.Models[id]
				if !known {
					return at(key, errors.New("not a model of the catalog; list it under unlisted"))
				}
				off = offeringOf(c, m)
			}
			if err := mergeInto(off, patch[key], &off); err != nil {
				return at(key, err)
			}
			p.Models[id] = off
			setOffered(c, id, p.ID, true)
		}
		return nil
	}
	return errors.New("not an object")
}

// mergeUnlisted merges patch, the unlisted models of a provider's change,
// into those of p.
func mergeUnlisted(p *gateway.Provider, patch any) error {
	switch patch := patch.(type) {
	case nil:
		return nil
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(patch)) {
			// The catalog's keys are lower case, as Lookup finds them; the
			// model ID sent keeps the case the file gives it.
			id := strings.ToLower(key)
			if patch[key] == nil {
				delete(p.Unlisted, id)
				continue
			}
			off, ok := p.Unlisted[id]
			if !ok {
				off = gateway.Offering{Name: key, ProviderOptions: gateway.ProviderOptions{ID: key}}
			}
			if err := mergeInto(off, patch[key], &off); err != nil {
				return at(key, err)
			}
			if p.Unlisted == nil {
				p.Unlisted = make(map[string]gateway.Offering)
			}
			p.Unlisted[id] = off
		}
		return nil
	}
	return errors.New("not an object")
}

// offeringOf returns the offering a provider starts with of model m of c:
// the model's facts, the reasoning its lab's offering lists, and its ID
// without the lab to send.
func offeringOf(c *gateway.Catalog, m gateway.Model) gateway.Offering {
	off := gateway.Offering{
		Name:         m.Name,
		Family:       m.Family,
		Type:         m.Type,
		Released:     m.Released,
		Updated:      m.Updated,
		Knowledge:    m.Knowledge,
		OpenWeights:  m.OpenWeights,
		Capabilities: m.Capabilities,
		Limit:        m.Limit,
	}
	_, send, found := strings.Cut(string(m.ID), "/")
	if !found {
		send = string(m.ID)
	}
	off.ProviderOptions.ID = send
	if lab, ok := c.Providers[m.Lab].Models[m.ID]; ok {
		off.Reasoning, off.Interleaved = lab.Reasoning, lab.Interleaved
	} else if m.Reasoning {
		off.Reasoning = &gateway.Reasoning{}
	}
	return off
}

// setOffered adds provider p to the providers c lists for model id, or
// removes it, keeping them in ID order. It copies what it changes.
func setOffered(c *gateway.Catalog, id gateway.ModelID, p gateway.ProviderID, offered bool) {
	m, ok := c.Models[id]
	if !ok {
		return
	}
	i, has := slices.BinarySearch(m.Providers, p)
	switch {
	case offered && !has:
		m.Providers = slices.Insert(slices.Clone(m.Providers), i, p)
	case !offered && has:
		m.Providers = slices.Delete(slices.Clone(m.Providers), i, i+1)
	default:
		return
	}
	c.Models[id] = m
}

// removeProvider removes provider id from c, and from the providers c lists
// for each model.
func removeProvider(c *gateway.Catalog, id gateway.ProviderID) {
	p, ok := c.Providers[id]
	if !ok {
		return
	}
	delete(c.Providers, id)
	for m := range p.Models {
		setOffered(c, m, id, false)
	}
}

// mergeInto merges patch into base, as JSON, and decodes the result into
// out, failing on a key that out's type does not have.
func mergeInto[T any](base T, patch any, out *T) error {
	if _, ok := patch.(map[string]any); !ok {
		return errors.New("not an object")
	}
	data, err := json.Marshal(base)
	if err != nil {
		return err
	}
	merged, err := decodeValue(data)
	if err != nil {
		return err
	}
	data, err = json.Marshal(mergeValue(merged, patch))
	if err != nil {
		return err
	}
	var v T
	if err := decodeStrict(data, &v); err != nil {
		return err
	}
	*out = v
	return nil
}

// mergeValue returns patch merged into base: an object's keys one by one,
// removing those patch sets to null, and any other value of patch in place
// of base's. It may change base.
func mergeValue(base, patch any) any {
	b, ok := base.(map[string]any)
	p, isObj := patch.(map[string]any)
	if !ok || !isObj {
		return patch
	}
	for k, v := range p {
		if v == nil {
			delete(b, k)
			continue
		}
		b[k] = mergeValue(b[k], v)
	}
	return b
}

// decodeValue decodes data as a JSON value, keeping numbers as written.
func decodeValue(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the value")
	}
	return v, nil
}

// decodeStrict decodes data into v, failing on a key v's type does not have.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
