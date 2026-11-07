package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// appendMessages stores msgs in session id, each below the one before.
func appendMessages(t *testing.T, a *app.App, id message.SessionID, msgs ...message.Content) {
	t.Helper()
	for _, m := range msgs {
		if err := a.Sessions().AppendMessage(t.Context(), id, &m); err != nil {
			t.Fatal(err)
		}
	}
}

// promptMsg returns a prompt holding text.
func promptMsg(id message.ID, text string) message.Content {
	return message.Content{ID: id, Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{Text: text}}}
}

// replyMsg returns a reply holding text.
func replyMsg(id message.ID, text string) message.Content {
	return message.Content{ID: id, Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: text}}}
}

func TestTree(t *testing.T) {
	ts, a := newTestServerApp(t)
	sess := createSession(t, ts)
	appendMessages(t, a, sess.ID, promptMsg("u1", "hi"), replyMsg("a1", "hello"))

	var tree struct {
		Messages []struct {
			ID       string `json:"id"`
			ParentID string `json:"parent_id"`
			Role     string `json:"role"`
		} `json:"messages"`
		Head string `json:"head"`
	}
	if status := doJSON(t, "GET", fmt.Sprintf("%s/session/%s/tree", ts.URL, sess.ID), nil, &tree); status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if len(tree.Messages) != 2 || tree.Head != "a1" {
		t.Fatalf("tree = %+v, want two messages ending at a1", tree)
	}
	if m := tree.Messages[1]; m.ID != "a1" || m.ParentID != "u1" || m.Role != "agent" {
		t.Errorf("second message = %+v, want reply a1 below u1", m)
	}

	// An unknown session has an empty tree.
	tree.Messages, tree.Head = nil, "unset"
	if status := doJSON(t, "GET", ts.URL+"/session/ses_unknown/tree", nil, &tree); status != http.StatusOK || len(tree.Messages) != 0 || tree.Head != "" {
		t.Errorf("unknown session: status %d, tree %+v; want 200 and an empty tree", status, tree)
	}
}

// TestNavigateValidation checks the requests POST /navigate refuses before
// moving. The test server records no snapshots, so even a valid move is
// refused; the app's tests cover the moves themselves.
func TestNavigateValidation(t *testing.T) {
	ts, a := newTestServerApp(t)
	sess := createSession(t, ts)
	appendMessages(t, a, sess.ID, promptMsg("u1", "hi"), replyMsg("a1", "hello"))
	url := fmt.Sprintf("%s/session/%s/navigate", ts.URL, sess.ID)

	tests := []struct {
		name string
		body any
		want int
	}{
		{"no body", nil, http.StatusBadRequest},
		{"empty message", NavigateRequest{}, http.StatusBadRequest},
		{"snapshots disabled", NavigateRequest{MessageID: "u1"}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status := doJSON(t, "POST", url, tt.body, nil); status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
		})
	}
}
