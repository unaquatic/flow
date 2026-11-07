package engine

import (
	"bytes"
	"compress/flate"
	"fmt"
	"io"
	"sync"
)

// Codec compresses the content of objects for storage. A Codec must be safe
// for concurrent use, since a snapshot compresses files in parallel.
type Codec interface {
	// Compress appends src, compressed, to dst.
	Compress(dst *bytes.Buffer, src []byte) error

	// Decompress returns the content src decompresses to, which must be
	// exactly size bytes.
	Decompress(src []byte, size int64) ([]byte, error)
}

// Stored is the Codec that stores content as it is, without compressing it.
type Stored struct{}

// Compress appends src to dst unchanged.
func (Stored) Compress(dst *bytes.Buffer, src []byte) error {
	_, err := dst.Write(src)
	return err
}

// Decompress returns src, which must be exactly size bytes.
func (Stored) Decompress(src []byte, size int64) ([]byte, error) {
	if int64(len(src)) != size {
		return nil, fmt.Errorf("stored %d bytes, want %d", len(src), size)
	}
	return src, nil
}

// Deflate is the Codec that compresses with DEFLATE. It favors speed, since
// a snapshot compresses every changed file.
type Deflate struct{}

// flateWriters holds compressors for reuse; each allocates large tables.
var flateWriters = sync.Pool{
	New: func() any {
		w, err := flate.NewWriter(nil, flate.BestSpeed)
		if err != nil {
			panic(err) // unreachable: BestSpeed is a valid level
		}
		return w
	},
}

// Compress appends src compressed with DEFLATE to dst.
func (Deflate) Compress(dst *bytes.Buffer, src []byte) error {
	w := flateWriters.Get().(*flate.Writer)
	defer flateWriters.Put(w)
	w.Reset(dst)
	if _, err := w.Write(src); err != nil {
		return err
	}
	return w.Close()
}

// Decompress inflates src, which must expand to exactly size bytes.
func (Deflate) Decompress(src []byte, size int64) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(src))
	defer r.Close()
	buf := bytes.NewBuffer(make([]byte, 0, size))
	if _, err := io.Copy(buf, io.LimitReader(r, size+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) != size {
		return nil, fmt.Errorf("inflated to %d bytes, want %d", buf.Len(), size)
	}
	return buf.Bytes(), nil
}
