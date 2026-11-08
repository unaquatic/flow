package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestFileRead checks that a missing or empty file has no keys and that a
// file that is not a JSON object fails to read.
func TestFileRead(t *testing.T) {
	tests := []struct {
		name    string
		data    *string
		want    int
		wantErr bool
	}{
		{name: "missing"},
		{name: "empty", data: new("")},
		{name: "object", data: new(`{"a":1,"b":{"c":2}}`), want: 2},
		{name: "array", data: new(`[1]`), wantErr: true},
		{name: "corrupt", data: new(`{`), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := File{Dir: t.TempDir(), Name: "x.json"}
			if tt.data != nil {
				writeFile(t, f, *tt.data)
			}
			keys, err := f.Read()
			if (err != nil) != tt.wantErr || len(keys) != tt.want {
				t.Errorf("Read = %d keys, %v; want %d keys, error %v", len(keys), err, tt.want, tt.wantErr)
			}
		})
	}
}

// TestFileUpdateKeepsOtherKeys checks that Update changes its key alone,
// removes a key when change returns nil, and creates the file and its
// directory when missing.
func TestFileUpdateKeepsOtherKeys(t *testing.T) {
	f := File{Dir: filepath.Join(t.TempDir(), "home"), Name: "x.json"}
	set := func(key, value string) {
		t.Helper()
		err := f.Update(t.Context(), key, func(json.RawMessage) (json.RawMessage, error) {
			if value == "" {
				return nil, nil
			}
			return json.RawMessage(value), nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	set("a", `1`)
	set("b", `{"c":2}`)
	set("a", "")
	keys, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := json.Compact(&b, keys["b"]); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["a"]; ok || b.String() != `{"c":2}` {
		t.Errorf("keys = %s, want b alone", keys)
	}
}

// TestFileUpdateLeavesFileOnFailure checks that Update writes nothing when
// change fails, when the file is not a JSON object, or when the result is
// the file as it was.
func TestFileUpdateLeavesFileOnFailure(t *testing.T) {
	errChange := errors.New("change failed")
	same := `{
  "a": 1
}
`
	tests := []struct {
		name    string
		data    string
		change  func(json.RawMessage) (json.RawMessage, error)
		wantErr bool
	}{
		{"change fails", `{"a":1}`, func(json.RawMessage) (json.RawMessage, error) { return nil, errChange }, true},
		{"corrupt file", `{`, func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`2`), nil }, true},
		{"unchanged", same, func(old json.RawMessage) (json.RawMessage, error) { return old, nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := File{Dir: t.TempDir(), Name: "x.json"}
			writeFile(t, f, tt.data)
			path := filepath.Join(f.Dir, f.Name)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Update(t.Context(), "a", tt.change); (err != nil) != tt.wantErr {
				t.Fatalf("Update = %v, want error %v", err, tt.wantErr)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Error("Update replaced the file")
			}
		})
	}
}

// TestFileUpdateConcurrent updates one file from many goroutines and checks
// that no update is lost.
func TestFileUpdateConcurrent(t *testing.T) {
	f := File{Dir: t.TempDir(), Name: "x.json"}
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			// Every goroutine also counts in one shared key.
			if err := f.Update(t.Context(), fmt.Sprint("k", i), func(json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`true`), nil
			}); err != nil {
				t.Error(err)
			}
			if err := (Section[int]{File: f, Key: "count"}).Update(t.Context(), func(c *int) error {
				*c++
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	keys, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if _, ok := keys[fmt.Sprint("k", i)]; !ok {
			t.Errorf("update of k%d lost", i)
		}
	}
	if string(keys["count"]) != fmt.Sprint(n) {
		t.Errorf("count = %s, want %d", keys["count"], n)
	}
}

// entry is a section value with an IsZero method, as agent.AuthEntry has.
type entry struct {
	Names []string `json:"names,omitempty"`
}

func (e entry) IsZero() bool { return len(e.Names) == 0 }

// TestSection checks that Section loads the zero value of a missing key,
// round-trips a value, removes a key whose value its IsZero method reports
// zero, and keeps one whose type has none.
func TestSection(t *testing.T) {
	f := File{Dir: t.TempDir(), Name: "x.json"}
	s := Section[entry]{File: f, Key: "e"}
	if got, err := s.Load(); err != nil || !got.IsZero() {
		t.Fatalf("Load of a missing key = %+v, %v; want the zero entry", got, err)
	}
	if err := s.Set(t.Context(), entry{Names: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(); err != nil || len(got.Names) != 1 || got.Names[0] != "a" {
		t.Errorf("Load = %+v, %v; want the saved entry", got, err)
	}
	if err := (Section[bool]{File: f, Key: "b"}).Set(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(t.Context(), entry{}); err != nil {
		t.Fatal(err)
	}
	keys, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["e"]; ok {
		t.Error("zero entry kept")
	}
	if string(keys["b"]) != "false" {
		t.Errorf("b = %s, want false kept: a bool has no IsZero method", keys["b"])
	}
}

// TestSectionLoadNamesKey checks that a value that does not decode fails
// with the file and the key in the error.
func TestSectionLoadNamesKey(t *testing.T) {
	f := File{Dir: t.TempDir(), Name: "x.json"}
	writeFile(t, f, `{"n":"not a number"}`)
	_, err := Section[int]{File: f, Key: "n"}.Load()
	if err == nil || !strings.Contains(err.Error(), "x.json: n: ") {
		t.Errorf("Load = %v, want an error naming x.json and n", err)
	}
}

// writeFile writes data as the file of f.
func writeFile(t *testing.T, f File, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, f.Name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFileLockName checks that a file's lock file is named after the file
// without its extension, and that an update takes it there.
func TestFileLockName(t *testing.T) {
	for name, want := range map[string]string{
		"config.json": "config.lock",
		"auth.json":   "auth.lock",
		"settings":    "settings.lock",
	} {
		if got := (File{Name: name}).LockName(); got != want {
			t.Errorf("LockName of %s = %q, want %q", name, got, want)
		}
	}
	dir := t.TempDir()
	if err := (Section[int]{File: File{Dir: dir, Name: "auth.json"}, Key: "n"}).Set(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if got, want := names(t, dir), []string{"auth.json", "auth.lock"}; !slices.Equal(got, want) {
		t.Errorf("directory holds %q, want %q", got, want)
	}
}
