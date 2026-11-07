package builtin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent"
)

// defaultGrepLimit is the number of matches grep returns when the call sets
// no limit.
const defaultGrepLimit = 100

// Grep returns a recursive content-search tool using Go regular
// expressions. It returns the first limit matches in walk order, by path and
// then line number, at most MaxBytes bytes, with lines cut to MaxGrepLine
// characters, ending with a notice when it cut anything.
func Grep() agent.Tool {
	return agent.Tool{
		Name:        "grep",
		Description: fmt.Sprintf("Search file contents using a Go RE2 regular expression. Returns lines as 'path:line:text'. Output is truncated to %d matches or %dKB (whichever is hit first). Long lines are truncated to %d chars.", defaultGrepLimit, MaxBytes/1024, MaxGrepLine),
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"pattern": {Type: "string", Description: "Regular expression (Go RE2 syntax)."},
				"path":    {Type: "string", Description: "Root directory to search. Defaults to '.'."},
				"include": {Type: "string", Description: "Optional glob to filter file names (e.g. '*.go')."},
				"limit":   {Type: "integer", Description: fmt.Sprintf("Maximum number of matches to return (default: %d).", defaultGrepLimit)},
			},
			Required: []string{"pattern"},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			pattern, err := stringArg(args, "pattern")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			re, err := regexp.Compile(pattern)
			if se, ok := errors.AsType[*syntax.Error](err); ok {
				return agent.ToolOutput{}, fmt.Errorf("invalid pattern: %s: `%s`", se.Code, se.Expr)
			}
			if err != nil {
				return agent.ToolOutput{}, fmt.Errorf("invalid pattern: %w", err)
			}
			root := "."
			if v, ok := args["path"].(string); ok && v != "" {
				root = v
			}
			var include string
			if v, ok := args["include"].(string); ok {
				include = v
			}
			if _, err := filepath.Match(include, ""); err != nil {
				return agent.ToolOutput{}, fmt.Errorf("invalid include: %w", err)
			}
			limit := defaultGrepLimit
			if n, ok := NumberAsInt64(args["limit"]); ok && n > 0 {
				limit = int(n)
			}

			var hits, skipped []string
			linesCut := false
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
				if include != "" && !matchName(include, d.Name()) {
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				found, cut, err := grepFile(path, rel, re, limit-len(hits))
				hits = append(hits, found...)
				linesCut = linesCut || cut
				if err != nil {
					skipped = append(skipped, path)
				}
				if len(hits) >= limit {
					return fs.SkipAll
				}
				return nil
			})
			if err != nil && err != fs.SkipAll {
				return agent.ToolOutput{}, err
			}
			if len(hits) == 0 {
				return agent.ToolOutput{Text: "[no matches]" + skippedNote(skipped)}, nil
			}
			// The walk visits paths in lexical order and each file's lines in
			// order, so the hits need no sorting.
			t := TruncateHead(strings.Join(hits, "\n"), math.MaxInt, MaxBytes)
			var notices []string
			switch {
			case t.Truncated:
				notices = append(notices, fmt.Sprintf("showing first %d matches with %d bytes limit", t.OutputLines, MaxBytes))
			case len(hits) >= limit:
				notices = append(notices, fmt.Sprintf("showing first %d matches", limit))
			}
			if linesCut {
				notices = append(notices, fmt.Sprintf("some lines cut to %d chars", MaxGrepLine))
			}
			out := t.Content
			if len(notices) > 0 {
				out += "\n\n[" + strings.Join(notices, ", ") + "]"
			}
			return agent.ToolOutput{Text: out + skippedNote(skipped)}, nil
		},
	}
}

// grepFile returns up to limit lines of the file at path that match re,
// formatted as "name:line:text" with the text cut to MaxGrepLine characters,
// and reports whether it cut any. A binary file has none. Lines found before
// a read error are returned with it.
func grepFile(path, name string, re *regexp.Regexp, limit int) ([]string, bool, error) {
	binary, err := isLikelyBinary(path)
	if err != nil || binary {
		return nil, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	var hits []string
	cut := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for ln := 1; scanner.Scan() && len(hits) < limit; ln++ {
		if re.MatchString(scanner.Text()) {
			text, lineCut := TruncateLine(scanner.Text(), MaxGrepLine)
			cut = cut || lineCut
			hits = append(hits, fmt.Sprintf("%s:%d:%s", name, ln, text))
		}
	}
	return hits, cut, scanner.Err()
}

// isLikelyBinary reports whether the first 512 bytes of the file at path
// contain a NUL byte.
func isLikelyBinary(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return slices.Contains(buf[:n], 0), nil
}
