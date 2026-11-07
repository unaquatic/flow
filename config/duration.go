package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration wraps time.Duration with string-or-seconds JSON decoding.
type Duration struct {
	time.Duration
}

// MarshalJSON encodes the duration using time.Duration.String.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// UnmarshalJSON decodes a time.ParseDuration string or a number of seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		d.Duration = parsed
		return nil
	}

	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration must be a string (\"3s\") or number (seconds)")
	}
	d.Duration = time.Duration(n * float64(time.Second))
	return nil
}
