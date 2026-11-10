package ui

import (
	"testing"

	"github.com/unaquatic/flow/agent/message"
)

// TestInitSessionUpdated checks that the first start of a release that an
// update installed tells of it right under the session info, above the
// messages of the session it resumes, and only once.
func TestInitSessionUpdated(t *testing.T) {
	s, a := newTestService(t)
	id := storePrompts(t, a, "Run the tests")
	s.Updated = "v1.2.3"
	s.InitSession(t.Context(), id)

	var status, updated, prompt int = -1, -1, -1
	for i, m := range s.state.Messages {
		switch {
		case m.Notice != nil && len(m.Notice.Lines) == 1 && m.Notice.Lines[0].PlainText() == "Updated to v1.2.3":
			updated = i
		case m.Notice != nil && status < 0:
			status = i
		case m.Role == message.RoleUser && prompt < 0:
			prompt = i
		}
	}
	if !(status >= 0 && status < updated && updated < prompt) {
		t.Errorf("session info at %d, update at %d, prompt at %d; want them in that order", status, updated, prompt)
	}
	if s.Updated != "" {
		t.Errorf("Updated = %q after the start, want it cleared", s.Updated)
	}
}
