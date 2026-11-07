package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// configCases are configuration files and the core.excludesFile they set.
var configCases = []struct {
	name   string
	config string
	value  string
	set    bool
}{
	{"plain", "[core]\n\texcludesFile = ~/.gitignore_global\n", "~/.gitignore_global", true},
	{"names in any case", "[Core]\n\tEXCLUDESFILE=/abs/path\n", "/abs/path", true},
	{"trailing comment", "[core]\n\texcludesfile = /a/b # note\n", "/a/b", true},
	{"semicolon comment", "[core]\n\texcludesfile = /a/b ; note\n", "/a/b", true},
	{"quoted", "[core]\n\texcludesfile = \"with spaces # kept\" ; note\n", "with spaces # kept", true},
	{"escapes", "[core]\n\texcludesfile = \"a\\\"b\\\\c\\td\"\n", "a\"b\\c\td", true},
	{"newline and backspace escapes", "[core]\n\texcludesfile = a\\nb\\bc\n", "a\nb\bc", true},
	{"inner whitespace kept", "[core]\n\texcludesfile = two  words\n", "two  words", true},
	{"last one wins", "[core]\n\texcludesfile = first\n[user]\n\tname = x\n[core]\n\texcludesfile = second\n", "second", true},
	{"key on the header line", "[core] excludesfile = inline\n", "inline", true},
	{"subsection ignored", "[core \"sub\"]\n\texcludesfile = no\n", "", false},
	{"dotted subsection ignored", "[core.sub]\n\texcludesfile = no\n", "", false},
	{"other section", "[user]\n\texcludesfile = no\n", "", false},
	{"commented out", "[core]\n\t# excludesfile = no\n", "", false},
	{"other key", "[core]\n\texcludes = no\n", "", false},
	{"CRLF", "[core]\r\n\texcludesfile = crlf\r\n", "crlf", true},
}

func TestConfigValue(t *testing.T) {
	for _, tt := range configCases {
		t.Run(tt.name, func(t *testing.T) {
			got, set := configValue([]byte(tt.config), "core", "excludesfile")
			if got != tt.value || set != tt.set {
				t.Errorf("configValue = %q, %t; want %q, %t", got, set, tt.value, tt.set)
			}
		})
	}
}

// TestConfigValue_AgreesWithGit reads each case with git config.
func TestConfigValue_AgreesWithGit(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	for _, tt := range configCases {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(file, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("git", "config", "--file", file, "--get", "core.excludesFile").Output()
			set := err == nil
			if e, ok := errors.AsType[*exec.ExitError](err); ok && e.ExitCode() == 1 {
				err = nil // the key is not set
			}
			if err != nil {
				t.Fatalf("git config: %v", err)
			}
			want := strings.TrimSuffix(string(out), "\n")
			if got, ok := configValue([]byte(tt.config), "core", "excludesfile"); got != want || ok != set {
				t.Errorf("configValue = %q, %t; git config says %q, %t", got, ok, want, set)
			}
		})
	}
}
