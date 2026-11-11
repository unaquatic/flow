// Package logging writes the structured log to a file shared by processes,
// tagging each record with the package that logged it, copies the records of
// each session to a file of its own, and can send standard error to the log.
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// OpenFile opens the named log file for appending, creating it and its
// directory if needed.
func OpenFile(name string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// Init makes the default logger write structured logs to the named file,
// appending to it, at level and above. Each line records its time in UTC,
// written with a Z suffix, the source line that logged it, and the process
// ID. A record with a session attribute is also written to <session>.log
// beside the file. Standard error is left as it is; see [CaptureStderr]. It
// fails, and leaves the default logger as it is, when the file cannot be
// opened.
//
// It returns the open file, which a process that logs until it exits leaves
// open. Closing it once the default logger no longer writes to it, as at the
// end of a test, lets Windows remove it.
func Init(name string, level slog.Level) (io.Closer, error) {
	f, err := OpenFile(name)
	if err != nil {
		return nil, err
	}

	// Every line carries the process ID, since processes share the log file,
	// and the component that logged it.
	opts := &slog.HandlerOptions{
		AddSource:   true,
		Level:       level,
		ReplaceAttr: replaceLogAttr,
	}
	slog.SetDefault(slog.New(SessionHandler{
		Handler: ComponentHandler{Handler: slog.NewTextHandler(f, opts)},
		Dir:     filepath.Dir(name),
		Options: opts,
	}).With("pid", os.Getpid()))
	return f, nil
}

// SessionHandler is a slog.Handler that passes each record to Handler and
// writes a record with a session attribute to <session>.log in Dir too.
type SessionHandler struct {
	// Handler receives every record.
	Handler slog.Handler
	// Dir holds the session log files.
	Dir string
	// Options format the session log files.
	Options *slog.HandlerOptions
	// Session is the session of the attributes added so far, if any.
	Session string
	// Grouped reports whether attributes added now belong to a group.
	Grouped bool
	// Steps replay WithAttrs and WithGroup on a session file's handler.
	Steps []func(slog.Handler) slog.Handler
}

// Enabled reports whether Handler handles records at level.
func (h SessionHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Handler.Enabled(ctx, level)
}

// Handle passes r to Handler, and writes it to its session's file.
func (h SessionHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.Handler.Handle(ctx, r.Clone())
	id := h.Session
	r.Attrs(func(a slog.Attr) bool {
		if s, ok := sessionOf(a); ok && !h.Grouped {
			id = s
		}
		return true
	})
	if id == "" {
		return err
	}
	f, ferr := OpenFile(filepath.Join(h.Dir, id+".log"))
	if ferr != nil {
		return errors.Join(err, ferr)
	}
	defer f.Close()
	var sh slog.Handler = ComponentHandler{Handler: slog.NewTextHandler(f, h.Options)}
	for _, step := range h.Steps {
		sh = step(sh)
	}
	return errors.Join(err, sh.Handle(ctx, r))
}

// WithAttrs returns the handler with attrs added.
func (h SessionHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := h
	out.Handler = h.Handler.WithAttrs(attrs)
	out.Steps = append(slices.Clip(h.Steps), func(sh slog.Handler) slog.Handler { return sh.WithAttrs(attrs) })
	for _, a := range attrs {
		if s, ok := sessionOf(a); ok && !h.Grouped {
			out.Session = s
		}
	}
	return out
}

// WithGroup returns the handler with the group name.
func (h SessionHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	out := h
	out.Handler = h.Handler.WithGroup(name)
	out.Steps = append(slices.Clip(h.Steps), func(sh slog.Handler) slog.Handler { return sh.WithGroup(name) })
	out.Grouped = true
	return out
}

// sessionOf returns the session that a names, when it is a session attribute
// whose value is safe as a file name.
func sessionOf(a slog.Attr) (string, bool) {
	if a.Key != "session" {
		return "", false
	}
	id := a.Value.Resolve().String()
	unsafe := func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_')
	}
	if id == "" || strings.ContainsFunc(id, unsafe) {
		return "", false
	}
	return id, true
}

// ComponentHandler is a slog.Handler that adds a component attribute to each
// record: the name of the Go package whose code logged it, such as engine or
// tool. Records without a program counter get none.
type ComponentHandler struct {
	// Handler receives the records.
	Handler slog.Handler
}

// Enabled reports whether the wrapped handler handles records at level.
func (h ComponentHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Handler.Enabled(ctx, level)
}

// Handle passes r to the wrapped handler, with the component as its first
// attribute.
func (h ComponentHandler) Handle(ctx context.Context, r slog.Record) error {
	component := packageOf(r.PC)
	if component == "" {
		return h.Handler.Handle(ctx, r)
	}
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	out.AddAttrs(slog.String("component", component))
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(a)
		return true
	})
	return h.Handler.Handle(ctx, out)
}

// WithAttrs returns a ComponentHandler wrapping the wrapped handler with
// attrs.
func (h ComponentHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ComponentHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup returns a ComponentHandler wrapping the wrapped handler with the
// group name. The component then belongs to the group too.
func (h ComponentHandler) WithGroup(name string) slog.Handler {
	return ComponentHandler{Handler: h.Handler.WithGroup(name)}
}

// packageOf returns the name of the package of the function at pc, or "" when
// pc is zero or unknown.
func packageOf(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	// The function is the package path and the name, such as
	// github.com/unaquatic/flow/extension/snapshot/internal/engine.(*scan).stageFile.
	// Type arguments of a generic function can hold '/' and '.', so they go
	// first.
	name, _, _ := strings.Cut(frame.Function, "[")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name, _, _ = strings.Cut(name, ".")
	return name
}

// replaceLogAttr writes a record's time in UTC, which the text handler shows
// with a Z suffix rather than an offset, and its source as the package
// directory, file and line, such as agent/agent.go:24.
func replaceLogAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		a.Value = slog.TimeValue(a.Value.Time().UTC())
	case slog.SourceKey:
		if s, ok := a.Value.Any().(*slog.Source); ok && s.File != "" {
			a.Value = slog.StringValue(fmt.Sprintf("%s/%s:%d", path.Base(path.Dir(s.File)), path.Base(s.File), s.Line))
		}
	}
	return a
}
