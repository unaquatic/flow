package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/snapshot/internal/engine"
	_ "github.com/unaquatic/sqlite" // registers the driver that database.Open uses
)

// mustHash parses s or fails the test.
func mustHash(t *testing.T, s string) Hash {
	t.Helper()
	h, err := engine.ParseHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// newTestExtension returns the extension over a worktree with one file, in a
// database of its own, whose snapshot parts start pending after delay.
func newTestExtension(t *testing.T, delay time.Duration) *Extension {
	t.Helper()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(t.TempDir(), database.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(t.Context(), db, Name, engine.Migrations); err != nil {
		t.Fatal(err)
	}
	project, err := database.Project(t.Context(), db, worktree)
	if err != nil {
		t.Fatal(err)
	}
	e := New(engine.NewService(engine.NewStore(db, project), worktree))
	e.IndexingDelay = delay
	return e
}

// sinkEvent is one call a hook made to a PartSink.
type sinkEvent struct {
	Ended bool
	Part  message.ContentPart
}

// recordSink returns a PartSink that appends each call to events.
func recordSink(events *[]sinkEvent) agent.PartSink {
	return agent.PartSink{
		Start: func(p message.ContentPart) { *events = append(*events, sinkEvent{Part: p}) },
		End:   func(p message.ContentPart) { *events = append(*events, sinkEvent{Ended: true, Part: p}) },
	}
}

// TestSnapshotPart_JSONRoundtrip checks that a snapshot part keeps its tree
// and whether it is pending through its tagged form: the type, then the
// fields, leaving out the zero Hash of a failed or pending snapshot.
func TestSnapshotPart_JSONRoundtrip(t *testing.T) {
	tree := mustHash(t, "7a36b04ce429eb3326713c5689665b792179b206")
	parts := []message.ContentPart{
		SnapshotPart{ID: "s1", Tree: tree},
		SnapshotPart{ID: "s2"},
		SnapshotPart{ID: "s3", Pending: true},
	}
	data, err := message.MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `[{"type":"snapshot","id":"s1","tree":"7a36b04ce429eb3326713c5689665b792179b206"},` +
		`{"type":"snapshot","id":"s2"},{"type":"snapshot","id":"s3","pending":true}]`
	if string(data) != want {
		t.Errorf("stored form\n%s\nwant\n%s", data, want)
	}
	got, err := message.UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, parts) {
		t.Fatalf("round-trip lost data: got %+v want %+v", got, parts)
	}
	if _, ok := got[0].(message.Metadata); !ok {
		t.Error("a snapshot part is not metadata, and would be sent to a provider")
	}
}

// TestSnapshotPart_MalformedTree checks that a stored tree that is not an
// object name fails to decode rather than reading as no tree.
func TestSnapshotPart_MalformedTree(t *testing.T) {
	if got, err := message.UnmarshalParts([]byte(`[{"type":"snapshot","tree":"tree_a"}]`)); err == nil {
		t.Fatalf("unmarshal = %+v, want an error", got)
	}
}

// TestTreeIn finds the tree a message ends with among its parts.
func TestTreeIn(t *testing.T) {
	tree := mustHash(t, "c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0")
	if got := TreeIn([]message.ContentPart{message.TextPart{Text: "hi"}, SnapshotPart{Tree: tree}}); got != tree {
		t.Errorf("tree = %s, want %s", got, tree)
	}
	if got := TreeIn([]message.ContentPart{message.TextPart{Text: "hi"}}); !got.IsZero() {
		t.Errorf("tree of parts without a snapshot part = %s, want zero", got)
	}
}

// TestTreeAt checks which tree a move restores: the newest the messages up
// to the head recorded, or before the first message the tree of the prompt
// that opens the branch, and that a point with none is refused.
func TestTreeAt(t *testing.T) {
	sent := mustHash(t, "7a36b04ce429eb3326713c5689665b792179b206")
	edited := mustHash(t, "c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0")
	branch := []message.Content{
		{ID: "u1", Role: message.RoleUser, Parts: []message.ContentPart{SnapshotPart{Tree: sent}}},
		{ID: "a1", Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: "thinking"}}},
		{ID: "a2", Role: message.RoleAgent, Parts: []message.ContentPart{SnapshotPart{Tree: edited}}},
	}
	for _, tt := range []struct {
		head message.ID
		want Hash
	}{
		{"", sent},
		{"u1", sent},
		{"a1", sent},
		{"a2", edited},
	} {
		if got, err := TreeAt(branch, tt.head); err != nil || got != tt.want {
			t.Errorf("TreeAt(%q) = %s, %v; want %s", tt.head, got, err, tt.want)
		}
	}
	failed := []message.Content{{ID: "u1", Role: message.RoleUser, Parts: []message.ContentPart{SnapshotPart{}}}}
	if _, err := TreeAt(failed, "u1"); !errors.Is(err, ErrNoSnapshot) || !errors.Is(err, extension.ErrRefused) {
		t.Errorf("TreeAt of a failed snapshot = %v, want ErrNoSnapshot, a refusal", err)
	}
}

// TestTrack checks that the snapshot part starts pending when recording the
// workspace takes longer than IndexingDelay, as a delay of zero makes it, and
// with its tree when recording is quicker. Either way it ends with the tree,
// under the ID it started with.
func TestTrack(t *testing.T) {
	for _, tt := range []struct {
		name    string
		delay   time.Duration
		pending bool
	}{
		{name: "slow", delay: 0, pending: true},
		{name: "quick", delay: time.Hour, pending: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var events []sinkEvent
			newTestExtension(t, tt.delay).OnTurnEnd(t.Context(), agent.Turn{}, recordSink(&events))
			if len(events) != 2 || events[0].Ended || !events[1].Ended {
				t.Fatalf("sink calls = %+v, want a start and then an end", events)
			}
			started, ended := events[0].Part.(SnapshotPart), events[1].Part.(SnapshotPart)
			if started.Pending != tt.pending || started.Pending == !started.Tree.IsZero() {
				t.Errorf("step started as %+v, want pending = %v, with a tree unless pending", started, tt.pending)
			}
			if ended.Pending || ended.Tree.IsZero() || ended.ID != started.ID {
				t.Errorf("step ended as %+v, want the part %q with its tree", ended, started.ID)
			}
		})
	}
}

// TestTrack_TrackDelay checks that TrackDelay keeps a quick snapshot's part
// pending for at least the delay, where without it the part would start with
// its tree, and that it ends with the tree after.
func TestTrack_TrackDelay(t *testing.T) {
	const delay = 300 * time.Millisecond
	e := newTestExtension(t, delay/3)
	e.TrackDelay = delay

	var events []sinkEvent
	start := time.Now()
	e.OnTurnEnd(t.Context(), agent.Turn{}, recordSink(&events))
	if took := time.Since(start); took < delay {
		t.Errorf("Track took %v, want at least %v", took, delay)
	}
	if len(events) != 2 {
		t.Fatalf("sink calls = %+v, want a start and an end", events)
	}
	started, ended := events[0].Part.(SnapshotPart), events[1].Part.(SnapshotPart)
	if !started.Pending {
		t.Errorf("step started as %+v, want it pending", started)
	}
	if ended.Pending || ended.Tree.IsZero() || ended.ID != started.ID {
		t.Errorf("step ended as %+v, want the part %q with its tree", ended, started.ID)
	}
}

// TestTrack_TrackDelayCanceled checks that a context that ends cuts
// TrackDelay short, so that a long delay cannot hold up a canceled run.
func TestTrack_TrackDelayCanceled(t *testing.T) {
	e := newTestExtension(t, 0)
	e.TrackDelay = time.Hour

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var events []sinkEvent
	start := time.Now()
	e.OnTurnEnd(ctx, agent.Turn{}, recordSink(&events))
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("Track took %v after its context ended, want it to stop waiting", took)
	}
	if len(events) != 2 || !events[1].Ended {
		t.Errorf("sink calls = %+v, want a start and an end", events)
	}
}

// TestDisabled checks that a disabled extension adds no part, refuses every
// move, and reads no patch.
func TestDisabled(t *testing.T) {
	e := newTestExtension(t, 0)
	tree, err := e.Service.Track(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	e.Service.Disabled = true

	var events []sinkEvent
	e.OnPrompt(t.Context(), extension.Prompt{}, recordSink(&events))
	if len(events) != 0 {
		t.Errorf("a disabled extension added %+v", events)
	}
	branch := []message.Content{{ID: "u1", Role: message.RoleUser, Parts: []message.ContentPart{SnapshotPart{Tree: tree}}}}
	if _, err := e.OnMove(t.Context(), extension.Move{Branch: branch, Head: "u1"}); !errors.Is(err, ErrDisabled) {
		t.Errorf("OnMove = %v, want ErrDisabled", err)
	}
	if patch, err := e.Patch(t.Context(), tree, tree); patch != "" || err != nil {
		t.Errorf("Patch = %q, %v; want none", patch, err)
	}
	if !strings.Contains(ErrDisabled.Error(), "disabled") {
		t.Errorf("ErrDisabled reads %q", ErrDisabled)
	}
}

// TestSettings checks that the settings read as their own section does, and
// as the bare true or false that older config files held, and that they turn
// snapshots on unless they say otherwise.
func TestSettings(t *testing.T) {
	for _, tt := range []struct {
		data string
		on   bool
	}{
		{`{"enabled":false}`, false},
		{`{"enabled":true}`, true},
		{`{}`, true},
		{`false`, false},
		{`true`, true},
	} {
		var s Settings
		if err := json.Unmarshal([]byte(tt.data), &s); err != nil {
			t.Fatalf("decode %s: %v", tt.data, err)
		}
		if s.On() != tt.on {
			t.Errorf("settings %s turn snapshots on = %v, want %v", tt.data, s.On(), tt.on)
		}
	}
	if !(Settings{}).On() {
		t.Error("no settings turn snapshots off")
	}
}

// TestLoad checks that loading the extension creates its tables, once, and
// follows its settings, and that settings it cannot read keep the app from
// starting.
func TestLoad(t *testing.T) {
	worktree := t.TempDir()
	db, err := database.Open(filepath.Join(t.TempDir(), database.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	project, err := database.Project(t.Context(), db, worktree)
	if err != nil {
		t.Fatal(err)
	}
	host := func(settings string) extension.Host {
		cfg := &config.Config{}
		if settings != "" {
			cfg.Extensions = map[string]json.RawMessage{Name: json.RawMessage(settings)}
		}
		return extension.Host{DB: db, Project: project, WorkingDir: worktree, DataDir: t.TempDir(), Config: cfg}
	}

	for _, tt := range []struct {
		settings string
		disabled bool
	}{
		{"", false},
		{`{"enabled":false}`, true},
		{`false`, true},
	} {
		e, err := Load(t.Context(), host(tt.settings))
		if err != nil {
			t.Fatalf("load with %q: %v", tt.settings, err)
		}
		if got := e.(*Extension).Disabled(); got != tt.disabled {
			t.Errorf("load with %q: disabled = %v, want %v", tt.settings, got, tt.disabled)
		}
	}
	var versions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM migrations WHERE id = ?`, Name).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != len(engine.Migrations) {
		t.Errorf("recorded %d migrations of the extension's tables, want %d", versions, len(engine.Migrations))
	}
	if _, err := Load(t.Context(), host(`{"enabled":"yes"}`)); err == nil {
		t.Error("load with settings it cannot read = nil error")
	}
}
