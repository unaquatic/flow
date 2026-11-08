package main

import "testing"

// TestParseOrigin checks that --cors takes an origin as a browser names a
// page's, with a trailing slash dropped, and refuses a URL that is not one.
func TestParseOrigin(t *testing.T) {
	for _, tt := range []struct {
		in, want string
	}{
		{"https://app.example", "https://app.example"},
		{"https://app.example/", "https://app.example"},
		{"http://localhost:3000", "http://localhost:3000"},
	} {
		if got, err := parseOrigin(tt.in); err != nil || got != tt.want {
			t.Errorf("parseOrigin(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"app.example", "ftp://app.example", "https://app.example/path", "https://app.example?x=1", "https://user@app.example", "https://"} {
		if got, err := parseOrigin(in); err == nil {
			t.Errorf("parseOrigin(%q) = %q, want an error", in, got)
		}
	}
}
