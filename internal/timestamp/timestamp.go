// Package timestamp writes and reads the times flow stores, in one form:
// UTC, to the millisecond, with a literal Z and no zone offset, such as
// "2026-01-01T12:00:00.500Z".
//
// The fraction always has three digits, even when it is zero, which RFC 3339
// with a variable fraction leaves out: every value is 24 bytes, so stored
// times sort chronologically as text.
package timestamp

import "time"

// Layout is the form of a stored time, as time.Format takes it. Its Z is a
// literal: Format converts to UTC first, and Parse refuses any other zone.
const Layout = "2006-01-02T15:04:05.000Z"

// Format returns t in Layout: in UTC, truncated to the millisecond.
func Format(t time.Time) string {
	return t.UTC().Format(Layout)
}

// Parse returns the UTC time that s, a time in Layout, holds. It fails on a
// time of any other form, such as one with a zone offset, or without its
// milliseconds.
func Parse(s string) (time.Time, error) {
	return time.Parse(Layout, s)
}

// Time is a time that encodes as text, and so as a JSON string, in Layout,
// as Format writes it, and decodes only from that form, as Parse reads it.
type Time time.Time

// MarshalText returns t in Layout.
func (t Time) MarshalText() ([]byte, error) {
	return []byte(Format(time.Time(t))), nil
}

// UnmarshalText sets t to the time that text, in Layout, holds.
func (t *Time) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*t = Time(parsed)
	return nil
}
