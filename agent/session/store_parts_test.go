package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// TestDeleteMessage_TakesItsParts checks that deleting a message removes
// the parts of it and of every message below it through the foreign key, and
// leaves the parts of the other messages: an orphaned part row is
// unreachable, so it would only ever be found by its absence.
func TestDeleteMessage_TakesItsParts(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	const id = "ses_parts"
	//	msg_1 -> msg_2 -> msg_3 (deleted from msg_2)
	for i, parent := range []message.ID{"", "msg_1", "msg_2"} {
		msgID := message.ID(fmt.Sprintf("msg_%d", i+1))
		if err := store.SetHead(ctx, id, Head{ID: parent}); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		msg := message.Content{ID: msgID, Role: message.RoleUser, Parts: []message.ContentPart{
			message.TextPart{ID: message.PartID("prt_" + msgID + "a"), Text: "a"},
			message.TextPart{ID: message.PartID("prt_" + msgID + "b"), Text: "b"},
		}}
		if err := store.AppendMessage(ctx, id, &msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteMessage(ctx, id, "msg_2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := partRows(t, store); n != 2 {
		t.Errorf("%d part rows left, want msg_1's 2", n)
	}
	if kept, err := store.Message(ctx, id, "msg_1"); err != nil || len(kept.Parts) != 2 {
		t.Errorf("kept message = %+v, %v; want it with both its parts", kept, err)
	}
}

// unregisteredPart is a part of a type that RegisterPart never registered,
// which the store cannot read back.
type unregisteredPart struct{}

func (unregisteredPart) PartType() message.ContentType { return "test_unregistered" }
func (unregisteredPart) PartID() message.PartID        { return "prt_unregistered" }

// TestAppendMessage_StoresTypeAndIDInColumns checks that a part's ID and
// type are stored in columns of their own, and its data holds its other
// fields alone, for a part of package message and a registered one alike,
// and that a part with no other fields reads back.
func TestAppendMessage_StoresTypeAndIDInColumns(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	id := withParts(t, store,
		message.TextPart{ID: "prt_text", Text: "hi"},
		message.TextPart{ID: "prt_empty"},
		message.UsagePart{ID: "prt_usage", Usage: message.Usage{InputTokens: 3, OutputTokens: 2}},
		treePart{ID: "prt_tree", Tree: "7a36b04ce429eb3326713c5689665b792179b206"},
	)

	rows, err := store.db.QueryContext(ctx, `SELECT id, type, data FROM message_parts ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ ID, Type, Data string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Type, &r.Data); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []row{
		{"prt_text", "text", `{"text":"hi"}`},
		{"prt_empty", "text", `{}`},
		{"prt_usage", "usage", `{"usage":{"input_tokens":3,"output_tokens":2}}`},
		{"prt_tree", "test_tree", `{"tree":"7a36b04ce429eb3326713c5689665b792179b206"}`},
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows =\n%v\nwant\n%v", got, want)
	}

	msg, err := store.Message(ctx, id, "msg_1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := partIDs(msg.Parts); !slices.Equal(got, []message.PartID{"prt_text", "prt_empty", "prt_usage", "prt_tree"}) {
		t.Fatalf("read back parts %v", got)
	}
	if empty, ok := msg.Parts[1].(message.TextPart); !ok || empty.Text != "" {
		t.Errorf("part without other fields = %#v, want an empty TextPart", msg.Parts[1])
	}
}

// TestAppendMessage_RefusesUnregisteredParts checks that a message holding
// a part of a type nothing registered is not stored, since the part could
// not be read back.
func TestAppendMessage_RefusesUnregisteredParts(t *testing.T) {
	store := newTestStore(t)
	msg := message.Content{ID: "msg_1", Role: message.RoleAgent, Parts: []message.ContentPart{unregisteredPart{}}}
	if err := store.AppendMessage(t.Context(), "ses_a", &msg); err == nil {
		t.Fatal("stored a part of an unregistered type")
	}
	if n := partRows(t, store); n != 0 {
		t.Errorf("%d part rows left by the failed append", n)
	}
	if _, err := store.Session(t.Context(), "ses_a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("session after the failed append: err = %v, want fs.ErrNotExist", err)
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
	const variant = "lab-a/model-a-fast:high"
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
