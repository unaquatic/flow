package timestamp

import (
	"encoding/json"
	"testing"
	"time"
)

// TestFormat_IsFixedWidth pins the property that stored times sort on: every
// value has the same width, as RFC 3339 with a variable fraction would not,
// since it leaves the fraction out when it is zero.
func TestFormat_IsFixedWidth(t *testing.T) {
	whole := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for name, at := range map[string]time.Time{
		"whole second":   whole,
		"half second":    whole.Add(500 * time.Millisecond),
		"one nanosecond": whole.Add(time.Nanosecond),
		"zero time":      {},
	} {
		if got := Format(at); len(got) != len(Layout) {
			t.Errorf("%s: Format(%v) = %q, want %d bytes", name, at, got, len(Layout))
		}
	}
}

// TestFormat_KeepsMilliseconds checks that a time is written in UTC, to the
// millisecond, cutting what is finer, and reads back as that time, in UTC.
func TestFormat_KeepsMilliseconds(t *testing.T) {
	at := time.Date(2026, 1, 1, 21, 0, 0, 0, time.FixedZone("UTC+9", 9*60*60)).Add(1500*time.Microsecond + 999*time.Nanosecond)
	const want = "2026-01-01T12:00:00.001Z"
	if got := Format(at); got != want {
		t.Errorf("Format(%v) = %q, want %q", at, got, want)
	}
	back, err := Parse(want)
	if err != nil || !back.Equal(at.Truncate(time.Millisecond)) || back.Location() != time.UTC {
		t.Errorf("Parse(%q) = %v, %v; want %v in UTC", want, back, err, at.Truncate(time.Millisecond))
	}
}

// TestParse_RefusesOtherForms checks that a time is read only in Layout: in
// UTC, with a literal Z and no zone offset, and three digits of
// milliseconds.
func TestParse_RefusesOtherForms(t *testing.T) {
	for _, s := range []string{
		"2026-01-01T21:00:00.001+09:00",
		"2026-01-01T12:00:00.001+00:00",
		"2026-01-01T12:00:00Z",
		"2026-01-01T12:00:00.1Z",
		"2026-01-01T12:00:00.000001Z",
		"2026-01-01T12:00:00.001",
	} {
		if got, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", s, got)
		}
	}
}

// TestTime_JSON checks that a Time is a JSON string in Layout, the zero time
// too, that it decodes back to the time it holds, and that it refuses a
// string of another form.
func TestTime_JSON(t *testing.T) {
	type doc struct {
		At   Time `json:"at"`
		Zero Time `json:"zero"`
	}
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(500 * time.Millisecond)
	data, err := json.Marshal(doc{At: Time(at)})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"at":"2026-01-01T12:00:00.500Z","zero":"0001-01-01T00:00:00.000Z"}`
	if string(data) != want {
		t.Errorf("JSON = %s, want %s", data, want)
	}
	var back doc
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !time.Time(back.At).Equal(at) || !time.Time(back.Zero).IsZero() {
		t.Errorf("decoded = %v, %v; want %v and the zero time", time.Time(back.At), time.Time(back.Zero), at)
	}
	if err := json.Unmarshal([]byte(`{"at":"2026-01-01T12:00:00Z"}`), &back); err == nil {
		t.Error("decoding a time without milliseconds succeeded, want an error")
	}
}
