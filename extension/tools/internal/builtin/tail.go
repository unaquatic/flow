package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"uuid"

	"github.com/unaquatic/flow/product"
)

// TailBuffer collects a command's output with bounded memory. It holds the
// output until it exceeds MaxLines lines or MaxBytes bytes; from then on it
// writes all of it to a temporary file, which it keeps, and holds only a
// rolling tail. A TailBuffer is safe for concurrent writes.
type TailBuffer struct {
	mu sync.Mutex
	// buf holds the whole output until the file is opened, and then a tail of
	// at most 4*MaxBytes bytes.
	buf []byte
	// bufAtLine reports whether buf starts at the beginning of a line.
	bufAtLine bool
	// file receives the whole output once it exceeds the limits.
	file    *os.File
	path    string
	fileErr error

	totalBytes int
	newlines   int
	// lineBytes is the size of the line being written, and lastLine the
	// size of the last finished line.
	lineBytes int
	lastLine  int
}

// NewTailBuffer returns an empty TailBuffer.
func NewTailBuffer() *TailBuffer {
	return &TailBuffer{bufAtLine: true}
}

// Write records p. It never fails: a failure to save the full output is
// reported by Text instead.
func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count(p)
	b.buf = append(b.buf, p...)
	if b.file == nil && b.fileErr == nil && b.overLimit() {
		b.openFile()
		return len(p), nil
	}
	if b.file != nil {
		if _, err := b.file.Write(p); err != nil {
			b.fileErr = errors.Join(err, b.file.Close())
			b.file = nil
		}
	}
	if b.file != nil || b.fileErr != nil {
		b.trim()
	}
	return len(p), nil
}

// Text closes the file, if any, and returns the output: whole when it fits
// within MaxLines and MaxBytes, and otherwise its last lines followed by a
// notice naming the file that holds the whole output.
func (b *TailBuffer) Text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		if err := b.file.Close(); err != nil {
			b.fileErr = err
		}
		b.file = nil
	}
	if !b.overLimit() {
		return string(b.buf)
	}

	tail := b.buf
	if !b.bufAtLine {
		// The first line was cut when the tail was trimmed; drop it unless it
		// is the only one.
		if i := bytes.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
			tail = tail[i+1:]
		}
	}
	t := TruncateTail(string(tail), MaxLines, MaxBytes)
	total := b.lines()
	first := total - t.OutputLines + 1

	where := "full output in " + b.path
	if b.path == "" || b.fileErr != nil {
		where = fmt.Sprintf("full output not saved: %v", b.fileErr)
	}
	var notice string
	switch {
	case t.LastLinePartial:
		notice = fmt.Sprintf("[showing last %d bytes of line %d of %d bytes, %s]",
			t.OutputBytes, total, b.lastLineBytes(), where)
	case t.ByBytes:
		notice = fmt.Sprintf("[showing lines %d-%d of %d with %d bytes limit, %s]",
			first, total, total, MaxBytes, where)
	default:
		notice = fmt.Sprintf("[showing lines %d-%d of %d, %s]", first, total, total, where)
	}
	return t.Content + "\n\n" + notice
}

// count updates the byte and line counts with p.
func (b *TailBuffer) count(p []byte) {
	b.totalBytes += len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			b.lineBytes += len(p)
			return
		}
		b.lastLine = b.lineBytes + i
		b.lineBytes = 0
		b.newlines++
		p = p[i+1:]
	}
}

// lines returns the number of lines written so far. A final newline does not
// start another line.
func (b *TailBuffer) lines() int {
	if b.lineBytes > 0 {
		return b.newlines + 1
	}
	return b.newlines
}

// lastLineBytes returns the size of the last line, without its newline.
func (b *TailBuffer) lastLineBytes() int {
	if b.lineBytes > 0 {
		return b.lineBytes
	}
	return b.lastLine
}

// overLimit reports whether the output exceeds MaxLines or MaxBytes.
func (b *TailBuffer) overLimit() bool {
	return b.lines() > MaxLines || b.totalBytes > MaxBytes
}

// openFile creates the temporary file and writes the output so far to it.
func (b *TailBuffer) openFile() {
	name := product.ID + "-bash-" + uuid.NewV7().String() + ".log"
	f, err := os.OpenFile(filepath.Join(os.TempDir(), name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		b.fileErr = err
		b.trim()
		return
	}
	b.file, b.path = f, f.Name()
	if _, err := f.Write(b.buf); err != nil {
		b.fileErr = errors.Join(err, f.Close())
		b.file = nil
	}
	b.trim()
}

// trim keeps the last 2*MaxBytes bytes of buf once it holds more than
// 4*MaxBytes, which is more than the tail Text keeps. It moves them to the
// start of buf, so the buffer is reused.
func (b *TailBuffer) trim() {
	if len(b.buf) <= 4*MaxBytes {
		return
	}
	cut := len(b.buf) - 2*MaxBytes
	b.bufAtLine = b.buf[cut-1] == '\n'
	b.buf = b.buf[:copy(b.buf, b.buf[cut:])]
}
