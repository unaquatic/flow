package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/internal/pubsub"
)

// openTestDatabase opens a database in a temporary directory and returns it
// with the project whose worktree is worktree.
func openTestDatabase(t *testing.T, worktree string) (*sql.DB, database.ProjectID) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), database.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	project, err := database.Project(t.Context(), db, worktree)
	if err != nil {
		t.Fatal(err)
	}
	return db, project
}

// openSessions opens the session store of project in db, as the app opens
// it.
func openSessions(t *testing.T, db *sql.DB, project database.ProjectID) *session.Store {
	t.Helper()
	store, err := session.Open(t.Context(), db, project)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// loadSnapshot loads the snapshot extension over the project of db whose
// worktree is worktree, as the app loads it.
func loadSnapshot(t *testing.T, db *sql.DB, project database.ProjectID, worktree string) *snapshot.Extension {
	t.Helper()
	e, err := snapshot.Load(t.Context(), extension.Host{
		DB:         db,
		Project:    project,
		WorkingDir: worktree,
		DataDir:    t.TempDir(),
		Config:     &config.Config{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.(*snapshot.Extension)
}

// snapshotForAgentTest returns the snapshot extension over an isolated
// worktree with one file, whose snapshot parts start pending at once.
func snapshotForAgentTest(t *testing.T) *snapshot.Extension {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, project := openTestDatabase(t, worktree)
	s := loadSnapshot(t, db, project, worktree)
	s.IndexingDelay = 0
	return s
}

// newDisabledSnapshot returns a snapshot extension that records nothing.
func newDisabledSnapshot(t *testing.T) *snapshot.Extension {
	t.Helper()
	dir := t.TempDir()
	db, project := openTestDatabase(t, dir)
	s := loadSnapshot(t, db, project, dir)
	s.Service.Disabled = true
	return s
}

// snapshotsOf returns the snapshot extension of a's session, which
// newTestSession loads.
func snapshotsOf(t *testing.T, w Session) *snapshot.Extension {
	t.Helper()
	s, ok := extension.Find[*snapshot.Extension](w.app.extensions)
	if !ok {
		t.Fatal("the session's app has no snapshot extension")
	}
	return s
}

// appendMessage appends msg to the session or fails the test.
func appendMessage(t *testing.T, store *session.Store, id message.SessionID, msg *message.Content) {
	t.Helper()
	if err := store.AppendMessage(t.Context(), id, msg); err != nil {
		t.Fatalf("append %s: %v", msg.ID, err)
	}
}

// storedSession returns the stored session or fails the test.
func storedSession(t *testing.T, store *session.Store, id message.SessionID) *session.Session {
	t.Helper()
	sess, err := store.Session(t.Context(), id)
	if err != nil {
		t.Fatalf("session %s: %v", id, err)
	}
	return sess
}

// branchOf returns the session's active branch or fails the test.
func branchOf(t *testing.T, store *session.Store, id message.SessionID) []message.Content {
	t.Helper()
	msgs, err := store.Branch(t.Context(), id)
	if err != nil {
		t.Fatalf("branch %s: %v", id, err)
	}
	return msgs
}

// newTestSession returns session sessionID of an isolated app with a real
// session store, and the snapshot extension over a real snapshot store.
func newTestSession(t *testing.T, sessionID message.SessionID) (Session, string) {
	t.Helper()
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	db, project := openTestDatabase(t, worktree)

	a := &App{
		sessions:   openSessions(t, db, project),
		extensions: []extension.Extension{loadSnapshot(t, db, project, worktree)},
		broker:     pubsub.NewBroker[Event](),
		lockDir:    t.TempDir(),
		workerCtx:  t.Context(),
		operations: make(map[message.SessionID]*Operation),
	}
	return a.Session(sessionID), worktree
}

// readFile returns a worktree file's contents, or "" when it is absent.
func readFile(t *testing.T, worktree, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(worktree, name))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// seedTurn records one simulated turn: a prompt that ends with the workspace
// tree it was sent with, and a reply that ends with the tree its edits left.
// Delays preserve the lexical order of timestamp-based message IDs.
func seedTurn(t *testing.T, w Session, worktree, prompt string, edits map[string]string) message.ID {
	t.Helper()
	ctx := context.Background()
	time.Sleep(2 * time.Millisecond)

	sentHash, err := snapshotsOf(t, w).Service.Track(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for name, content := range edits {
		path := filepath.Join(worktree, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if content == "" {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	editedHash, err := snapshotsOf(t, w).Service.Track(ctx)
	if err != nil {
		t.Fatal(err)
	}

	user := &message.Content{
		ID:   message.NewID(),
		Role: message.RoleUser,
		Parts: []message.ContentPart{
			message.TextPart{Text: prompt},
			snapshot.SnapshotPart{Tree: sentHash},
		},
	}
	appendMessage(t, w.app.sessions, w.id, user)
	time.Sleep(2 * time.Millisecond)

	appendMessage(t, w.app.sessions, w.id, &message.Content{
		ID:   message.NewID(),
		Role: message.RoleAgent,
		Parts: []message.ContentPart{
			message.TextPart{Text: "ok"},
			snapshot.SnapshotPart{Tree: editedHash},
		},
	})

	return user.ID
}

func TestUndo_RewindsLastTurnFiles(t *testing.T) {
	w, worktree := newTestSession(t, "ses_undo")

	seedTurn(t, w, worktree, "first edit", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second edit", map[string]string{"a.txt": "v2"})

	if got := readFile(t, worktree, "a.txt"); got != "v2" {
		t.Fatalf("setup: a.txt = %q, want v2", got)
	}

	moved, err := w.undo()
	files := moved.Files
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("undo reported %d changed files, want 1: %+v", len(files), files)
	}
	if base := filepath.Base(files[0].File); base != "a.txt" {
		t.Errorf("undo reported %q, want a.txt", base)
	}
	if files[0].Status != "modified" {
		t.Errorf("a.txt status = %q, want modified", files[0].Status)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v1" {
		t.Fatalf("after undo: a.txt = %q, want v1", got)
	}

	sess := storedSession(t, w.app.sessions, w.id)
	if sess == nil || !sess.Head.MovedBack() {
		t.Fatal("expected the head to be parked behind a branch it can return to")
	}
	branch, err := w.app.sessions.Branch(t.Context(), w.id)
	if err != nil {
		t.Fatal(err)
	}
	if tree, err := snapshot.TreeAt(branch, sess.Head.ID); err != nil || moved.Checkout != tree.String() {
		t.Errorf("undo checked out %q, want the tree of the first turn, %s (%v)", moved.Checkout, tree, err)
	}
}

// TestUndoRedo_RoundTrip is the behaviour the split revert and unrevert verbs
// could not offer: undo and redo land on exactly the trees they name, one turn
// at a time, including for a file a turn created.
func TestUndoRedo_RoundTrip(t *testing.T) {
	w, worktree := newTestSession(t, "ses_round")

	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2", "added.txt": "new"})

	if _, err := w.undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v1" {
		t.Fatalf("after undo: a.txt = %q, want v1", got)
	}
	if got := readFile(t, worktree, "added.txt"); got != "" {
		t.Fatalf("undo left added.txt behind with %q", got)
	}

	if _, err := w.redo(); err != nil {
		t.Fatalf("redo: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v2" {
		t.Fatalf("after redo: a.txt = %q, want v2", got)
	}
	if got := readFile(t, worktree, "added.txt"); got != "new" {
		t.Fatalf("after redo: added.txt = %q, want new", got)
	}

	sess := storedSession(t, w.app.sessions, w.id)
	if sess.Head.MovedBack() {
		t.Fatal("redo should leave nothing ahead of the head")
	}
	if msgs := branchOf(t, w.app.sessions, w.id); len(msgs) != 4 {
		t.Fatalf("redo left %d messages on the branch, want 4", len(msgs))
	}
}

// TestUndo_StepsBackOneTurnAtATime pins the per-turn granularity that the old
// all-or-nothing unrevert lacked.
func TestUndo_StepsBackOneTurnAtATime(t *testing.T) {
	w, worktree := newTestSession(t, "ses_steps")

	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})
	seedTurn(t, w, worktree, "third", map[string]string{"a.txt": "v3"})

	for _, want := range []string{"v2", "v1", ""} {
		if _, err := w.undo(); err != nil {
			t.Fatalf("undo to %q: %v", want, err)
		}
		if got := readFile(t, worktree, "a.txt"); got != want {
			t.Fatalf("a.txt = %q, want %q", got, want)
		}
	}
	if _, err := w.undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo past the first turn = %v, want ErrNothingToUndo", err)
	}
}

// TestRedo_StepsForwardOneTurnAtATime is the mirror of
// TestUndo_StepsBackOneTurnAtATime: a run of undos is retraced turn by turn
// rather than collapsed into a single jump to the leaf.
func TestRedo_StepsForwardOneTurnAtATime(t *testing.T) {
	w, worktree := newTestSession(t, "ses_redo_steps")

	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})
	seedTurn(t, w, worktree, "third", map[string]string{"a.txt": "v3"})

	for range 3 {
		if _, err := w.undo(); err != nil {
			t.Fatalf("undo: %v", err)
		}
	}

	for _, want := range []string{"v1", "v2", "v3"} {
		if _, err := w.redo(); err != nil {
			t.Fatalf("redo to %q: %v", want, err)
		}
		if got := readFile(t, worktree, "a.txt"); got != want {
			t.Fatalf("a.txt = %q, want %q", got, want)
		}
	}

	sess := storedSession(t, w.app.sessions, w.id)
	if sess.Head.MovedBack() {
		t.Error("reaching the leaf should leave nothing ahead of the head")
	}
	if _, err := w.redo(); !errors.Is(err, ErrNothingToRedo) {
		t.Fatalf("redo past the leaf = %v, want ErrNothingToRedo", err)
	}
}

// TestCheckout_IsDirectionIndependent checks that the same destination
// produces the same workspace whether it is reached going back or coming
// forward.
func TestCheckout_IsDirectionIndependent(t *testing.T) {
	w, worktree := newTestSession(t, "ses_direction")

	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	second := seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})
	seedTurn(t, w, worktree, "third", map[string]string{"a.txt": "v3"})

	if _, err := w.checkoutAt(second); err != nil {
		t.Fatalf("checkout going back: %v", err)
	}
	backward := readFile(t, worktree, "a.txt")

	if _, err := w.undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if _, err := w.checkoutAt(second); err != nil {
		t.Fatalf("checkout coming forward: %v", err)
	}
	if forward := readFile(t, worktree, "a.txt"); forward != backward {
		t.Fatalf("same destination gave %q coming forward and %q going back", forward, backward)
	}
	if backward != "v1" {
		t.Fatalf("checkout before the second turn gave %q, want the first turn's v1", backward)
	}
}

func TestCheckout_UnknownMessage(t *testing.T) {
	w, worktree := newTestSession(t, "ses_unknown")
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})

	if _, err := w.checkoutAt("msg_nonexistent"); !errors.Is(err, session.ErrMessageNotFound) {
		t.Fatalf("checkout of an unknown message = %v, want ErrMessageNotFound", err)
	}
}

// TestCheckout_NoSnapshotRecorded covers a turn whose tracking failed: the move
// is refused rather than silently desynchronizing the files from the
// transcript.
func TestCheckout_NoSnapshotRecorded(t *testing.T) {
	w, _ := newTestSession(t, "ses_notrees")

	user := &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "hi"}, snapshot.SnapshotPart{}},
	}
	appendMessage(t, w.app.sessions, w.id, user)
	time.Sleep(2 * time.Millisecond)
	appendMessage(t, w.app.sessions, w.id, &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleAgent,
		Parts: []message.ContentPart{message.TextPart{Text: "ok"}, snapshot.SnapshotPart{}},
	})

	if _, err := w.undo(); !errors.Is(err, snapshot.ErrNoSnapshot) {
		t.Fatalf("undo = %v, want ErrNoSnapshot", err)
	}
	if _, err := w.checkoutAt(user.ID); !errors.Is(err, snapshot.ErrNoSnapshot) {
		t.Fatalf("checkout = %v, want ErrNoSnapshot", err)
	}
	if sess := storedSession(t, w.app.sessions, w.id); sess.Head.MovedBack() {
		t.Error("a refused move moved the head")
	}
}

// TestPatch_BetweenPromptAndReply checks that a reply's changes are the
// difference between the tree its prompt ended with and its own, and that a
// tree a failed snapshot left out gives no patch.
func TestPatch_BetweenPromptAndReply(t *testing.T) {
	w, worktree := newTestSession(t, "ses_patch")
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1\n"})

	branch := branchOf(t, w.app.sessions, w.id)
	prompt, reply := snapshot.TreeIn(branch[0].Parts), snapshot.TreeIn(branch[1].Parts)
	patch, err := snapshotsOf(t, w).Patch(t.Context(), prompt, reply)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !strings.Contains(patch, "+++ b/a.txt") || !strings.Contains(patch, "+v1") {
		t.Errorf("patch = %q, want a.txt added with v1", patch)
	}
	if patch, err := snapshotsOf(t, w).Patch(t.Context(), snapshot.Hash{}, reply); patch != "" || err != nil {
		t.Errorf("patch from no tree = %q, %v; want none", patch, err)
	}
}

// TestUndoRedo_Exported exercises the exported API: the operation holds the
// session and the transcript is republished to subscribers.
func TestUndoRedo_Exported(t *testing.T) {
	w, worktree := newTestSession(t, "ses_queue")

	events := w.app.broker.Subscribe(t.Context())
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})

	if _, err := w.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v1" {
		t.Fatalf("after Undo: a.txt = %q, want v1", got)
	}
	if got := len(awaitLoaded(t, events).Messages); got != 2 {
		t.Fatalf("Undo republished %d messages, want the 2 still visible", got)
	}

	if _, err := w.Redo(); err != nil {
		t.Fatalf("Redo: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v2" {
		t.Fatalf("after Redo: a.txt = %q, want v2", got)
	}
	if got := len(awaitLoaded(t, events).Messages); got != 4 {
		t.Fatalf("Redo republished %d messages, want all 4", got)
	}
}

// TestCheckout_ExportedRejectsUnknownMessage checks that the exported path
// reports failures instead of republishing.
func TestCheckout_ExportedRejectsUnknownMessage(t *testing.T) {
	w, worktree := newTestSession(t, "ses_queue_err")
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})

	moved, err := w.Checkout("msg_nonexistent")
	files := moved.Files
	if !errors.Is(err, session.ErrMessageNotFound) {
		t.Fatalf("Checkout = %v, want ErrMessageNotFound", err)
	}
	if files != nil {
		t.Fatalf("a failed checkout reported files: %+v", files)
	}
}

// activeBranch returns the message that opens fork's active branch, or the
// empty ID when no branch is active.
func activeBranch(fork Fork) message.ID {
	for _, b := range fork.Branches {
		if b.Active {
			return b.MessageID
		}
	}
	return ""
}

// awaitLoaded returns the next EventSessionLoaded, failing on timeout.
func awaitLoaded(t *testing.T, events <-chan Event) EventSessionLoaded {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if loaded, ok := ev.(EventSessionLoaded); ok {
				return loaded
			}
		case <-deadline:
			t.Fatal("timed out waiting for EventSessionLoaded")
		}
	}
}

func TestUndo_NothingToUndoOnEmptySession(t *testing.T) {
	w, _ := newTestSession(t, "ses_empty")
	if _, err := w.undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("expected ErrNothingToUndo, got %v", err)
	}
}

// TestCheckout_DisabledSnapshot checks that a disabled snapshot extension
// refuses every move, which leaves the files and the head where they are,
// and that the refusal is one the server reports as a bad request.
func TestCheckout_DisabledSnapshot(t *testing.T) {
	w, worktree := newTestSession(t, "ses_disabled")
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})
	if _, err := w.undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	snapshotsOf(t, w).Service.Disabled = true
	_, err := w.undo()
	if !errors.Is(err, snapshot.ErrDisabled) || !errors.Is(err, extension.ErrRefused) {
		t.Fatalf("undo: expected ErrDisabled, a refusal, got %v", err)
	}
	if _, err := w.redo(); !errors.Is(err, snapshot.ErrDisabled) {
		t.Fatalf("redo: expected ErrDisabled, got %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v1" {
		t.Errorf("a.txt = %q after refused moves, want v1", got)
	}
}

// TestMove_WithoutMoveHooks checks that an app with no extension that
// follows moves moves the head alone, and reports no files.
func TestMove_WithoutMoveHooks(t *testing.T) {
	w, worktree := newTestSession(t, "ses_nohooks")
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	w.app.extensions = nil
	moved, err := w.undo()
	files := moved.Files
	if err != nil || files != nil {
		t.Fatalf("undo = %v, %v; want no files and no error", files, err)
	}
	if sess := storedSession(t, w.app.sessions, w.id); !sess.Head.MovedBack() {
		t.Error("undo left the head where it was")
	}
	if got := readFile(t, worktree, "a.txt"); got != "v1" {
		t.Errorf("a.txt = %q, want v1 untouched", got)
	}
}

func TestRedo_NothingToRedo(t *testing.T) {
	w, _ := newTestSession(t, "ses_noredo")
	if _, err := w.redo(); !errors.Is(err, ErrNothingToRedo) {
		t.Fatalf("expected ErrNothingToRedo, got %v", err)
	}
}

// TestUndo_ThenAppendForks pins the behaviour that replaced the destructive
// commit: a message stored after a move back opens a branch and leaves the
// messages it was moved back past in place.
func TestUndo_ThenAppendForks(t *testing.T) {
	w, worktree := newTestSession(t, "ses_fork")
	ctx := t.Context()

	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	abandoned := seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})

	if _, err := w.undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}

	alt := &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "instead"}},
	}
	appendMessage(t, w.app.sessions, w.id, alt)

	branch := branchOf(t, w.app.sessions, w.id)
	if len(branch) != 3 || branch[2].ID != alt.ID {
		t.Fatalf("branch after forking = %d messages ending at %q, want 3 ending at the new prompt",
			len(branch), branch[len(branch)-1].ID)
	}
	if _, err := w.app.sessions.Message(ctx, w.id, abandoned); err != nil {
		t.Fatalf("forking deleted the branch it left behind: %v", err)
	}

	fork, err := w.Branches()
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	if len(fork.Branches) != 2 {
		t.Fatalf("fork has %d branches, want 2", len(fork.Branches))
	}
	if active := activeBranch(fork); active != alt.ID {
		t.Fatalf("active branch = %q, want the new prompt %q", active, alt.ID)
	}
}

// TestSwitchBranch_RestoresTheOtherBranchFiles checks that moving sideways
// between branches resets the workspace the same way undo and redo do.
func TestSwitchBranch_RestoresTheOtherBranchFiles(t *testing.T) {
	w, worktree := newTestSession(t, "ses_switch")

	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	abandoned := seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})

	if _, err := w.undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	seedTurn(t, w, worktree, "instead", map[string]string{"a.txt": "v3"})
	if got := readFile(t, worktree, "a.txt"); got != "v3" {
		t.Fatalf("setup: a.txt = %q, want v3", got)
	}

	if _, err := w.switchBranch(abandoned); err != nil {
		t.Fatalf("switch to the abandoned branch: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v2" {
		t.Fatalf("after switching: a.txt = %q, want v2", got)
	}

	fork, err := w.Branches()
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	if active := activeBranch(fork); active != abandoned {
		t.Fatalf("active branch = %q, want the one switched to, %q", active, abandoned)
	}
}
