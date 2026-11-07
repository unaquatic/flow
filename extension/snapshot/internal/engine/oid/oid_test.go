package oid

import (
	"encoding/json"
	"strings"
	"testing"
)

// helloBlob is the Git name of a blob holding "hello\n".
const helloBlob = "ce013625030ba8dba906f756967f9e9ca394464a"

// mustParse parses s or fails the test.
func mustParse(t *testing.T, s string) Hash {
	t.Helper()
	h, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestParse(t *testing.T) {
	if got := mustParse(t, helloBlob).String(); got != helloBlob {
		t.Errorf("round trip = %s, want %s", got, helloBlob)
	}
	for _, bad := range []string{"", "ce01", helloBlob + "00", strings.Replace(helloBlob, "c", "g", 1)} {
		t.Run(bad, func(t *testing.T) {
			if _, err := Parse(bad); err == nil {
				t.Errorf("Parse(%q) succeeded", bad)
			}
		})
	}
}

func TestHashScan(t *testing.T) {
	want := mustParse(t, helloBlob)
	var h Hash
	if err := h.Scan(want[:]); err != nil || h != want {
		t.Errorf("Scan = %s, %v; want %s", h, err, want)
	}

	tests := map[string]any{
		"short":  want[:19],
		"long":   append(want[:], 0),
		"string": want.String(),
		"nil":    nil,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			var h Hash
			if err := h.Scan(src); err == nil {
				t.Errorf("Scan(%#v) succeeded", src)
			}
		})
	}
}

// TestHashJSON checks that a Hash is a hexadecimal JSON string, that an
// omitzero field leaves the zero Hash out, and that a malformed name fails
// to decode.
func TestHashJSON(t *testing.T) {
	type record struct {
		Tree Hash `json:"tree,omitzero"`
	}
	tests := []struct {
		name string
		in   record
		want string
	}{
		{"set", record{Tree: mustParse(t, helloBlob)}, `{"tree":"` + helloBlob + `"}`},
		{"zero", record{}, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Errorf("Marshal = %s, want %s", data, tt.want)
			}
			var out record
			if err := json.Unmarshal(data, &out); err != nil || out != tt.in {
				t.Errorf("Unmarshal = %+v, %v; want %+v", out, err, tt.in)
			}
		})
	}

	var out record
	if err := json.Unmarshal([]byte(`{"tree":"tree_a"}`), &out); err == nil {
		t.Errorf("Unmarshal of a malformed name succeeded: %+v", out)
	}
}
