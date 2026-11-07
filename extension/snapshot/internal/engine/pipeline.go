package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/unaquatic/flow/extension/snapshot/internal/engine/internal/ignore"
)

// maxPooledBuffer is the capacity of the largest buffer kept for reuse.
// Larger buffers are left to the garbage collector.
const maxPooledBuffer = 64 << 20

// fileJob is a stale file for the pipeline to store.
type fileJob struct {
	// entry is the file's index entry, without its hash.
	entry IndexEntry

	// content is what the file's blob records, open for reading.
	content io.ReadCloser
}

// fileBlob is the content of a stale file, ready to store.
type fileBlob struct {
	// entry is the file's index entry, with its hash set.
	entry IndexEntry

	// size is the length of the content, which differs from entry.Size when
	// the file changed after the walk looked at it.
	size int

	// data holds the content compressed by the pipeline's codec.
	data *bytes.Buffer
}

// pipeline stores the stale files a walk finds. Workers read, hash, and
// compress the files in parallel, while the goroutine holding the
// transaction stores them, since only it may use the database.
type pipeline struct {
	workers int
	codec   Codec
	jobs    chan fileJob
	blobs   chan fileBlob
}

// newPipeline returns a pipeline that compresses with codec, with one worker
// per usable CPU.
func newPipeline(codec Codec) *pipeline {
	n := runtime.GOMAXPROCS(0)
	return &pipeline{
		workers: n,
		codec:   codec,
		jobs:    make(chan fileJob, n),
		blobs:   make(chan fileBlob, n),
	}
}

// stageFiles walks the worktree open as root, where the patterns of
// excludes apply, and stages every file below it: s records the unchanged
// files, and a pipeline stores the stale ones in tx. It returns the entries of
// the stored files.
func (s *scan) stageFiles(ctx context.Context, tx *Tx, root *os.Root, excludes ignore.Matcher) ([]IndexEntry, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	p := newPipeline(tx.codec)
	s.jobs = p.jobs
	var wg, workers sync.WaitGroup
	wg.Go(func() {
		defer close(p.jobs)
		if err := s.walk(ctx, root, "", excludes); err != nil {
			cancel(err)
		}
	})
	// A worker takes jobs after an error too, so that each file is closed.
	for range p.workers {
		workers.Go(func() {
			for j := range p.jobs {
				if err := p.compress(ctx, j); err != nil {
					cancel(err)
				}
			}
		})
	}
	wg.Go(func() {
		workers.Wait()
		close(p.blobs)
	})
	stored, err := p.storeAll(ctx, tx)
	if err != nil {
		cancel(err)
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return stored, nil
}

// compress reads, hashes, and compresses the content of j, closing it, and
// passes the blob on to be stored.
func (p *pipeline) compress(ctx context.Context, j fileJob) error {
	if ctx.Err() != nil {
		return errors.Join(context.Cause(ctx), j.content.Close())
	}
	b, err := p.readBlob(j)
	if err != nil {
		return err
	}
	select {
	case p.blobs <- b:
		return nil
	case <-ctx.Done():
		putBuffer(b.data)
		return context.Cause(ctx)
	}
}

// storeAll stores each blob the workers send in tx and records its entry,
// until the workers are done. It returns the entries.
func (p *pipeline) storeAll(ctx context.Context, tx *Tx) ([]IndexEntry, error) {
	var stored []IndexEntry
	for b := range p.blobs {
		if err := p.store(ctx, tx, b); err != nil {
			return nil, err
		}
		stored = append(stored, b.entry)
	}
	return stored, nil
}

// store stores b in tx, records its entry, and frees its memory.
func (p *pipeline) store(ctx context.Context, tx *Tx, b fileBlob) error {
	defer putBuffer(b.data)
	if err := tx.insert(ctx, b.entry.Hash, BlobType, b.size, b.data.Bytes()); err != nil {
		return err
	}
	return tx.SetEntry(ctx, b.entry)
}

// readBlob reads the content of j, closing it, and returns it hashed and
// compressed.
func (p *pipeline) readBlob(j fileJob) (fileBlob, error) {
	buf := getBuffer()
	defer putBuffer(buf)
	buf.Grow(int(j.entry.Size) + bytes.MinRead)
	_, err := buf.ReadFrom(j.content)
	if err := errors.Join(err, j.content.Close()); err != nil {
		return fileBlob{}, err
	}
	e := j.entry
	e.Hash = HashObject(BlobType, buf.Bytes())
	z := getBuffer()
	if err := p.codec.Compress(z, buf.Bytes()); err != nil {
		putBuffer(z)
		return fileBlob{}, err
	}
	return fileBlob{entry: e, size: buf.Len(), data: z}, nil
}

// buffers holds the buffers the workers read and compress into.
var buffers = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// getBuffer returns an empty buffer.
func getBuffer() *bytes.Buffer {
	return buffers.Get().(*bytes.Buffer)
}

// putBuffer returns b for reuse, unless it grew larger than maxPooledBuffer.
func putBuffer(b *bytes.Buffer) {
	if b.Cap() > maxPooledBuffer {
		return
	}
	b.Reset()
	buffers.Put(b)
}
