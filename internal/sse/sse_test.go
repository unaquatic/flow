package sse

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestRead checks that events are split at blank lines, with their type and
// data lines, whatever the line endings and line lengths.
func TestRead(t *testing.T) {
	long := strings.Repeat("x", 3<<20)
	tests := []struct {
		name string
		in   string
		want []Event
	}{
		{
			name: "typed events",
			in:   "event: a\ndata: 1\n\nevent: b\ndata: 2\n\n",
			want: []Event{{Type: "a", Data: "1"}, {Type: "b", Data: "2"}},
		},
		{
			name: "data only",
			in:   "data: {\"x\":1}\n\ndata: [DONE]\n\n",
			want: []Event{{Data: `{"x":1}`}, {Data: "[DONE]"}},
		},
		{
			name: "data lines join",
			in:   "data: a\ndata: b\n\n",
			want: []Event{{Data: "a\nb"}},
		},
		{
			name: "no space after the colon, and an empty value",
			in:   "event:a\ndata:1\n\ndata\n\n",
			want: []Event{{Type: "a", Data: "1"}, {Data: ""}},
		},
		{
			name: "comments, other fields, and an event without data",
			in:   ": ping\nid: 7\nretry: 10\n\nevent: empty\n\ndata: 1\n\n",
			want: []Event{{Data: "1"}},
		},
		{
			name: "CRLF",
			in:   "event: a\r\ndata: 1\r\n\r\n",
			want: []Event{{Type: "a", Data: "1"}},
		},
		{
			name: "no blank line before the end",
			in:   "event: a\ndata: 1",
			want: []Event{{Type: "a", Data: "1"}},
		},
		{
			name: "a line longer than any buffer",
			in:   "data: " + long + "\n\n",
			want: []Event{{Data: long}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Event
			for ev, err := range Read(strings.NewReader(tt.in)) {
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				got = append(got, ev)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("events = %.80q, want %.80q", got, tt.want)
			}
		})
	}
}

// TestRead_Error checks that the events before a read failure are yielded,
// then the failure.
func TestRead_Error(t *testing.T) {
	broken := errors.New("connection reset")
	r := io.MultiReader(strings.NewReader("data: 1\n\ndata: 2\n"), errReader{broken})

	var got []Event
	var gotErr error
	for ev, err := range Read(r) {
		if err != nil {
			gotErr = err
			continue
		}
		got = append(got, ev)
	}
	if want := []Event{{Data: "1"}}; !slices.Equal(got, want) || !errors.Is(gotErr, broken) {
		t.Errorf("events, error = %q, %v, want %q, %v", got, gotErr, want, broken)
	}
}

// errReader fails every read with err.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestWrite checks the bytes Write produces, and that Read reads them back
// as the event written.
func TestWrite(t *testing.T) {
	tests := []struct {
		name string
		ev   Event
		want string
	}{
		{name: "typed JSON", ev: Event{Type: "a", Data: `{"x":1}`}, want: "event: a\ndata: {\"x\":1}\n\n"},
		{name: "untyped", ev: Event{Data: "1"}, want: "data: 1\n\n"},
		{name: "data lines", ev: Event{Data: "a\nb"}, want: "data: a\ndata: b\n\n"},
		{name: "trailing newline", ev: Event{Data: "a\n"}, want: "data: a\ndata: \n\n"},
		{name: "empty data", ev: Event{Type: "ping"}, want: "event: ping\ndata: \n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			if err := Write(&b, tt.ev); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if b.String() != tt.want {
				t.Errorf("Write wrote %q, want %q", b.String(), tt.want)
			}
			var got []Event
			for ev, err := range Read(strings.NewReader(b.String())) {
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				got = append(got, ev)
			}
			if want := []Event{tt.ev}; !slices.Equal(got, want) {
				t.Errorf("Read read back %q, want %q", got, want)
			}
		})
	}
}

// TestStart checks the status and headers of an event stream response.
func TestStart(t *testing.T) {
	rec := httptest.NewRecorder()
	Start(rec)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ContentType || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("status, headers = %d, %v, want 200 with %s and no-cache", rec.Code, rec.Header(), ContentType)
	}
}
