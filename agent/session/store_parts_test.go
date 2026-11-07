package session

import (
	"context"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
)

// treePart is a part of a type an extension registers, as the snapshot
// extension registers the workspace tree a message ends with.
type treePart struct {
	ID   message.PartID `json:"id,omitempty"`
	Tree string         `json:"tree,omitempty"`
}

func (treePart) PartType() message.ContentType       { return "test_tree" }
func (p treePart) PartID() message.PartID            { return p.ID }
func (p treePart) WithID(id message.PartID) treePart { p.ID = id; return p }
func (treePart) Metadata()                           {}

func init() { message.RegisterPart[treePart]("test_tree") }

// withParts appends one message carrying parts and returns its session.
func withParts(t *testing.T, store *Store, parts ...message.ContentPart) message.SessionID {
	t.Helper()
	const id = "ses_parts"
	msg := message.Content{ID: "msg_1", Role: message.RoleAgent, Parts: parts}
	if err := store.AppendMessage(context.Background(), id, &msg); err != nil {
		t.Fatalf("append: %v", err)
	}
	return id
}

// partRows counts the stored parts of a whole database.
func partRows(t *testing.T, store *Store) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM message_parts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAppendMessage_RoundTripsPartsInOrder(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	usage := message.Usage{InputTokens: 1200, CachedInputTokens: 1024, OutputTokens: 300, TotalTokens: 1500}
	const tree = "7a36b04ce429eb3326713c5689665b792179b206"
	id := withParts(t, store,
		message.TextPart{ID: "prt_1", Text: "first"},
		message.ReasoningPart{ID: "prt_2", Text: "second"},
		message.UsagePart{ID: "prt_3", Usage: usage},
		treePart{ID: "prt_4", Tree: tree},
	)

	msg, err := store.Message(ctx, id, "msg_1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []message.PartID{"prt_1", "prt_2", "prt_3", "prt_4"}
	if got := partIDs(msg.Parts); !slices.Equal(got, want) {
		t.Fatalf("part order = %v, want %v", got, want)
	}
	if text, ok := msg.Parts[0].(message.TextPart); !ok || text.Text != "first" {
		t.Errorf("parts[0] = %#v, want the stored TextPart", msg.Parts[0])
	}
	if got := msg.Usage(); got != usage {
		t.Errorf("usage = %+v, want %+v", got, usage)
	}
	if got, _ := message.LastPart[treePart](msg.Parts); got.Tree != tree {
		t.Errorf("registered part = %+v, want its tree %s", got, tree)
	}

	branch, err := store.Branch(ctx, id)
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if got := partIDs(branch[0].Parts); !slices.Equal(got, want) {
		t.Errorf("branch part order = %v, want %v", got, want)
	}
}

// TestDeleteMessage_TakesItsParts covers the foreign key: an orphaned part row
// is unreachable, so it would only ever be found by its absence.
func TestDeleteMessage_TakesItsParts(t *testing.T) {
	store := newTestStore(t)
	id := withParts(t, store, message.TextPart{ID: "prt_1", Text: "doomed"})
	if partRows(t, store) != 1 {
		t.Fatalf("seeded %d part rows, want 1", partRows(t, store))
	}
	if err := store.DeleteMessage(context.Background(), id, "msg_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := partRows(t, store); n != 0 {
		t.Errorf("%d part rows outlived their message", n)
	}
}

func partIDs(parts []message.ContentPart) []message.PartID {
	out := make([]message.PartID, len(parts))
	for i, p := range parts {
		out[i] = p.PartID()
	}
	return out
}

// TestAppendMessage_RoundTripsVariant checks that a message keeps the model
// variant it ran as: its provider, model, mode and reasoning effort, in one.
func TestAppendMessage_RoundTripsVariant(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const id = "ses_variant"
	const variant = "openai/gpt-5.6-fast:high"
	msg := message.Content{
		ID: "msg_1", Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: "hi"}},
		Model: variant,
	}
	if err := store.AppendMessage(ctx, id, &msg); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := store.Message(ctx, id, "msg_1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Model != variant {
		t.Errorf("model = %q, want %q", got.Model, variant)
	}
}
