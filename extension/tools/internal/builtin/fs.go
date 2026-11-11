package builtin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent"
)

// defaultGlobLimit is the number of paths glob returns when the call sets no
// limit.
const defaultGlobLimit = 1000

// defaultLsLimit is the number of entries ls returns when the call sets no
// limit.
const defaultLsLimit = 500

// Read returns a tool that reads a file's lines from an optional offset,
// returning at most MaxLines lines and MaxBytes bytes. A result that leaves
// out lines ends with a notice of the lines it shows. An image, as its
// content shows, it returns whole as an attachment, as ReadImage does.
func Read() agent.Tool {
	return agent.Tool{
		Name:        "read",
		Description: fmt.Sprintf("Read the contents of a file from disk. Use absolute paths or paths relative to the working directory. Output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete. PNG, JPEG, GIF, and WebP images up to %d px per side and %dMB base64-encoded are returned as images for you to look at; offset and limit do not apply to them. A larger image is an error: resize or compress it into another file, and read that.", MaxLines, MaxBytes/1024, MaxImageSide, MaxImageEncoded>>20),
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"path":   {Type: "string", Description: "File path to read."},
				"offset": {Type: "integer", Description: "Line number to start reading from (1-indexed)."},
				"limit":  {Type: "integer", Description: "Maximum number of lines to read."},
			},
			Required: []string{"path"},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			path, err := stringArg(args, "path")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			st, err := os.Stat(path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if st.IsDir() {
				return agent.ToolOutput{}, fmt.Errorf("is a directory: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if mediaType := http.DetectContentType(data); slices.Contains(imageTypes, mediaType) {
				return ReadImage(path, mediaType, data)
			}
			offset, _ := NumberAsInt64(args["offset"])
			limit, hasLimit := NumberAsInt64(args["limit"])
			text, err := readWindow(string(data), int(offset), int(limit), hasLimit && limit > 0)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: text}, nil
		},
	}
}

// readWindow returns the lines of content from the 1-based line offset, at
// most limit lines when hasLimit is set, cut to MaxLines lines and MaxBytes
// bytes. A result that leaves out lines ends with a notice of the lines it
// shows. An offset past the last line is an error.
func readWindow(content string, offset, limit int, hasLimit bool) (string, error) {
	lines := splitLines(content)
	start := max(offset-1, 0)
	if start > 0 && start >= len(lines) {
		return "", fmt.Errorf("offset %d beyond %d lines", offset, len(lines))
	}
	end := len(lines)
	if hasLimit {
		end = min(start+limit, len(lines))
	}
	t := TruncateHead(strings.Join(lines[start:end], "\n"), MaxLines, MaxBytes)
	first := start + 1
	switch {
	case t.FirstLineTooLong:
		return fmt.Sprintf("[line %d is %d bytes exceeds %d bytes limit]", first, len(lines[start]), MaxBytes), nil
	case t.Truncated && t.ByBytes:
		return fmt.Sprintf("%s\n\n[showing lines %d-%d of %d with %d bytes limit]",
			t.Content, first, start+t.OutputLines, len(lines), MaxBytes), nil
	case t.Truncated:
		return fmt.Sprintf("%s\n\n[showing lines %d-%d of %d]", t.Content, first, start+t.OutputLines, len(lines)), nil
	case end < len(lines):
		return fmt.Sprintf("%s\n\n[showing lines %d-%d of %d]", t.Content, first, end, len(lines)), nil
	}
	return t.Content, nil
}

// Write returns a tool that replaces a file and creates parent directories.
func Write() agent.Tool {
	return agent.Tool{
		Name:        "write",
		Description: "Write text content to a file, replacing it if it exists. Creates parent directories as needed.",
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"path":    {Type: "string", Description: "File path to write."},
				"content": {Type: "string", Description: "Full file contents."},
			},
			Required: []string{"path", "content"},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			path, err := stringArg(args, "path")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			content, err := stringArg(args, "content")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if st, err := os.Stat(path); err == nil && st.IsDir() {
				return agent.ToolOutput{}, fmt.Errorf("is a directory: %s", path)
			}
			if dir := filepath.Dir(path); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return agent.ToolOutput{}, err
				}
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: fmt.Sprintf("wrote %d bytes", len(content))}, nil
		},
	}
}

// Edit returns a tool that replaces one unique string occurrence in a file.
func Edit() agent.Tool {
	return agent.Tool{
		Name:        "edit",
		Description: "Replace exactly one occurrence of old_string with new_string in a file. Fails if old_string is missing or appears more than once. Use this for in-place edits to existing files.",
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"path":       {Type: "string", Description: "File path to edit."},
				"old_string": {Type: "string", Description: "Exact text to replace. Must appear exactly once."},
				"new_string": {Type: "string", Description: "Replacement text."},
			},
			Required: []string{"path", "old_string", "new_string"},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			path, err := stringArg(args, "path")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			oldS, err := stringArg(args, "old_string")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			newS, err := stringArg(args, "new_string")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if oldS == "" {
				return agent.ToolOutput{}, errors.New("old_string is empty")
			}
			if oldS == newS {
				return agent.ToolOutput{}, errors.New("old_string equals new_string")
			}
			if st, err := os.Stat(path); err == nil && st.IsDir() {
				return agent.ToolOutput{}, fmt.Errorf("is a directory: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			body := string(data)
			count := strings.Count(body, oldS)
			if count == 0 {
				return agent.ToolOutput{}, errors.New("old_string not found")
			}
			if count > 1 {
				return agent.ToolOutput{}, fmt.Errorf("old_string found %d times", count)
			}
			body = strings.Replace(body, oldS, newS, 1)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: "edited"}, nil
		},
	}
}

// Ls returns a tool that lists the immediate entries of a directory in
// name order, at most limit entries and MaxBytes bytes, ending with a notice
// when it cut any.
func Ls() agent.Tool {
	return agent.Tool{
		Name:        "ls",
		Description: fmt.Sprintf("List the immediate entries of a directory (non-recursive). Returns one entry per line, alphabetically sorted; directories are suffixed with '/'. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).", defaultLsLimit, MaxBytes/1024),
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"path":  {Type: "string", Description: "Directory path. Defaults to '.'."},
				"limit": {Type: "integer", Description: fmt.Sprintf("Maximum number of entries to return (default: %d).", defaultLsLimit)},
			},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			dir := "."
			if v, ok := args["path"].(string); ok && v != "" {
				dir = v
			}
			limit := defaultLsLimit
			if n, ok := NumberAsInt64(args["limit"]); ok && n > 0 {
				limit = int(n)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if len(entries) == 0 {
				return agent.ToolOutput{Text: "[empty directory]"}, nil
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})
			total := len(entries)
			entries = entries[:min(total, limit)]
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
				if e.IsDir() {
					names[i] += "/"
				}
			}
			t := TruncateHead(strings.Join(names, "\n"), math.MaxInt, MaxBytes)
			out := t.Content
			switch {
			case t.Truncated:
				out += fmt.Sprintf("\n\n[showing entries 1-%d of %d with %d bytes limit]", t.OutputLines, total, MaxBytes)
			case total > limit:
				out += fmt.Sprintf("\n\n[showing entries 1-%d of %d]", limit, total)
			}
			return agent.ToolOutput{Text: out}, nil
		},
	}
}

// Glob returns a recursive path-matching tool. It walks every directory
// below the root and returns the newest matches, at most limit paths and
// MaxBytes bytes, ending with a notice when it cut any.
func Glob() agent.Tool {
	return agent.Tool{
		Name:        "glob",
		Description: fmt.Sprintf("Find files matching a glob pattern (e.g. '**/*.go'). Recursively walks every directory below the working directory. Results are sorted by modification time (newest first). Output is truncated to %d results or %dKB (whichever is hit first).", defaultGlobLimit, MaxBytes/1024),
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"pattern": {Type: "string", Description: "Glob pattern. Supports '*', '?', '[...]', and '**' (zero or more path segments)."},
				"path":    {Type: "string", Description: "Root directory to search. Defaults to '.'."},
				"limit":   {Type: "integer", Description: fmt.Sprintf("Maximum number of results (default: %d).", defaultGlobLimit)},
			},
			Required: []string{"pattern"},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			pattern, err := stringArg(args, "pattern")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if err := checkGlob(pattern); err != nil {
				return agent.ToolOutput{}, err
			}
			root := "."
			if v, ok := args["path"].(string); ok && v != "" {
				root = v
			}
			limit := defaultGlobLimit
			if n, ok := NumberAsInt64(args["limit"]); ok && n > 0 {
				limit = int(n)
			}

			type match struct {
				path  string
				mtime time.Time
			}
			var (
				matches []match
				skipped []string
			)
			err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					if path == root {
						return walkErr
					}
					skipped = append(skipped, path)
					return nil
				}
				if d.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if !matchGlob(pattern, rel) {
					return nil
				}
				info, err := d.Info()
				var mtime time.Time
				if err == nil {
					mtime = info.ModTime()
				}
				matches = append(matches, match{path: rel, mtime: mtime})
				return nil
			})
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if len(matches) == 0 {
				return agent.ToolOutput{Text: "[no matches]" + skippedNote(skipped)}, nil
			}

			// Keep the newest matches when the result is truncated.
			sort.Slice(matches, func(i, j int) bool {
				return matches[i].mtime.After(matches[j].mtime)
			})

			total := len(matches)
			matches = matches[:min(total, limit)]
			lines := make([]string, len(matches))
			for i, m := range matches {
				lines[i] = m.path
			}
			t := TruncateHead(strings.Join(lines, "\n"), math.MaxInt, MaxBytes)
			out := t.Content
			switch {
			case t.Truncated:
				out += fmt.Sprintf("\n\n[showing newest %d of %d files with %d bytes limit]", t.OutputLines, total, MaxBytes)
			case total > limit:
				out += fmt.Sprintf("\n\n[showing newest %d of %d files]", limit, total)
			}
			return agent.ToolOutput{Text: out + skippedNote(skipped)}, nil
		},
	}
}

// skippedNote tells the model which paths a search could not read, naming the
// first few. It is empty when the search read everything.
func skippedNote(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	const named = 5
	names := strings.Join(paths[:min(len(paths), named)], ", ")
	if len(paths) > named {
		names += ", ..."
	}
	return fmt.Sprintf("\n\n[skipped %d unreadable: %s]", len(paths), names)
}

// checkGlob reports whether pattern is a well-formed pattern for matchGlob.
func checkGlob(pattern string) error {
	segments := []string{pattern}
	if strings.Contains(pattern, "**") {
		segments = strings.Split(pattern, "/")
	}
	for _, seg := range segments {
		if seg == "**" {
			continue
		}
		if _, err := filepath.Match(seg, ""); err != nil {
			return fmt.Errorf("invalid pattern: %w", err)
		}
	}
	return nil
}

// matchName reports whether name matches the pattern p. The callers check p
// beforehand, so filepath.Match has no syntax error to report.
func matchName(p, name string) bool {
	ok, err := filepath.Match(p, name)
	return err == nil && ok
}

// matchGlob applies filepath.Match with support for ** path components. The
// pattern must have passed checkGlob.
func matchGlob(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		return matchName(pattern, name)
	}
	patParts := strings.Split(pattern, "/")
	nameParts := strings.Split(name, "/")
	return matchSegments(patParts, nameParts)
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		p := pat[0]
		if p == "**" {
			// Match zero or more path segments.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if !matchName(p, name[0]) {
			return false
		}
		pat = pat[1:]
		name = name[1:]
	}
	return len(name) == 0
}

// stringArg returns a required string argument.
func stringArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing argument: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return s, nil
}
