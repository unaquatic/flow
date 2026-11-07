package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// Metadata is implemented by the parts that are stored with a message but
// never sent to a provider, such as the token counts of its request, or the
// workspace snapshot an extension records.
type Metadata interface {
	ContentPart
	// Metadata marks the part as metadata.
	Metadata()
}

// LastPart returns the last part of type T among parts, and reports whether
// there is one. A message holds at most one part of each metadata type, such
// as its usage, which is then the one LastPart finds.
func LastPart[T ContentPart](parts []ContentPart) (T, bool) {
	for _, p := range slices.Backward(parts) {
		if v, ok := p.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// ExtensionPart is a content part that a package other than this one
// defines, such as an extension, and makes storable with RegisterPart.
type ExtensionPart[T any] interface {
	ContentPart
	// WithID returns a copy of the part with its ID replaced by id.
	WithID(id PartID) T
}

// partCodec encodes and decodes the parts of one registered type.
type partCodec struct {
	// withID returns a part of the type with its ID replaced.
	withID func(ContentPart, PartID) ContentPart
	// decode decodes the tagged form of a part of the type.
	decode func([]byte) (ContentPart, error)
}

var (
	codecsMu sync.RWMutex
	// codecs holds the codec of each registered part type.
	codecs = make(map[ContentType]partCodec)
)

// RegisterPart makes parts of type T, which PartType tags typ, storable: they
// are stored as T encodes itself to JSON, with "type" added in front, and
// decoded into T again. T must encode its ID as "id" and have no field it
// encodes as "type". The package that defines T registers it in its init
// function, as gob.Register is used, so that every program that can make a
// part of the type can also read it back. Registering a type twice, or a
// type of this package, panics.
func RegisterPart[T ExtensionPart[T]](typ ContentType) {
	codecsMu.Lock()
	defer codecsMu.Unlock()
	if _, ok := codecs[typ]; ok || slices.Contains(builtinTypes, typ) {
		panic(fmt.Sprintf("message: part type %q registered twice", typ))
	}
	codecs[typ] = partCodec{
		withID: func(p ContentPart, id PartID) ContentPart { return p.(T).WithID(id) },
		decode: func(data []byte) (ContentPart, error) {
			var v T
			if err := json.Unmarshal(data, &v); err != nil {
				return nil, err
			}
			return v.WithID(ensurePartID(v.PartID())), nil
		},
	}
}

// codecFor returns the codec of part type typ, and reports whether typ is
// registered.
func codecFor(typ ContentType) (partCodec, bool) {
	codecsMu.RLock()
	defer codecsMu.RUnlock()
	c, ok := codecs[typ]
	return c, ok
}

// MarshalJSON encodes j in its tagged form: the fields of a built-in part, or
// "type" followed by the fields that Ext encodes.
func (j PartJSON) MarshalJSON() ([]byte, error) {
	if j.Ext == nil {
		type plain PartJSON
		return json.Marshal(plain(j))
	}
	fields, err := json.Marshal(j.Ext)
	if err != nil {
		return nil, fmt.Errorf("encode %s part: %w", j.Type, err)
	}
	fields = bytes.TrimSpace(fields)
	if len(fields) < 2 || fields[0] != '{' {
		return nil, fmt.Errorf("encode %s part: not a JSON object", j.Type)
	}
	typ, err := json.Marshal(j.Type)
	if err != nil {
		return nil, err
	}
	out := append([]byte(`{"type":`), typ...)
	if rest := fields[1:]; rest[0] != '}' {
		out = append(out, ',')
		out = append(out, rest...)
	} else {
		out = append(out, '}')
	}
	return out, nil
}

// UnmarshalJSON decodes the tagged form of a part. A part of a registered
// type is decoded into that type, and held in Ext.
func (j *PartJSON) UnmarshalJSON(data []byte) error {
	type plain PartJSON
	var p plain
	err := json.Unmarshal(data, &p)
	typ := p.Type
	if err != nil {
		// A registered part may hold a field that a built-in field of the
		// same name cannot, so its type decides alone.
		var head struct {
			Type ContentType `json:"type"`
		}
		if json.Unmarshal(data, &head) != nil {
			return err
		}
		typ = head.Type
	}
	c, ok := codecFor(typ)
	if !ok {
		if err != nil {
			return err
		}
		*j = PartJSON(p)
		return nil
	}
	part, derr := c.decode(data)
	if derr != nil {
		return fmt.Errorf("decode %s part: %w", typ, derr)
	}
	*j = PartJSON{Type: typ, PartID: part.PartID(), Ext: part}
	return nil
}
