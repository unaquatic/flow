// Package snapshot is the extension that keeps the workspace in step with
// each session's history. It plugs into flow at the three points package
// extension describes:
//
//   - Content: StepPart, the workspace tree each prompt and reply ends with
//     (part.go).
//   - Behaviour: it records that tree as each prompt is sent and each turn
//     ends (track.go), and restores the files as they stood at the
//     destination of every move through the history (move.go).
//   - Terminal UI: package ui shows a snapshot being taken, and the changes
//     each reply made.
//
// Its Settings are the "snapshot" entry of the config file's extensions
// section, and its tables are those package engine stores snapshots in.
//
// The recording itself is done by package engine, which stores the files of
// the worktree as Git objects in the app's database.
package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/snapshot/internal/engine"
)

// Name names the extension: in the extensions section of the config file,
// which holds its Settings, and in the database, which records the migrations
// of its tables under it.
const Name = "snapshot"

// Settings are the extension's settings, which the config file holds as
// {"extensions": {"snapshot": {...}}}.
type Settings struct {
	// Enabled turns snapshots on or off. Nil turns them on.
	Enabled *bool `json:"enabled,omitempty"`
}

// UnmarshalJSON decodes the settings, or a bare true or false, which turns
// snapshots on or off, as the top-level "snapshot" key of config files
// written before extensions had settings of their own held it.
func (s *Settings) UnmarshalJSON(data []byte) error {
	var on bool
	if json.Unmarshal(data, &on) == nil {
		s.Enabled = &on
		return nil
	}
	type plain Settings
	return json.Unmarshal(data, (*plain)(s))
}

// On reports whether the settings turn snapshots on.
func (s Settings) On() bool { return s.Enabled == nil || *s.Enabled }

var (
	// ErrDisabled refuses a move while snapshots are disabled, since the
	// files could not follow the transcript.
	ErrDisabled error = extension.Refusal("snapshots are disabled")

	// ErrNoSnapshot refuses a move to a point that no tree was recorded for.
	ErrNoSnapshot error = extension.Refusal("no snapshot recorded for that point")
)

// Extension records the workspace in the step part each prompt and reply
// ends with, and restores it on every move through a session's history. It
// implements extension.PromptHook, extension.TurnHook, and extension.MoveHook. While its
// Service is disabled it records nothing and refuses every move. The zero
// Extension has no Service, and is disabled.
type Extension struct {
	// Service records and restores the workspace. Load creates it.
	Service *engine.Service
	// IndexingDelay is how long recording the workspace may take before the
	// step part that records it starts pending, as Track describes. Zero
	// starts it pending at once.
	IndexingDelay time.Duration
	// TrackDelay is extra time each step part's recording takes once the
	// workspace is recorded, so that the pending part can be looked at. It
	// is for previewing the UI: it only delays the part Track adds, not the
	// moves through the history. To see the part pending it must exceed
	// IndexingDelay. Load sets it from config.EnvSnapshotDelay. Zero adds
	// none.
	TrackDelay time.Duration
}

var (
	_ extension.PromptHook = (*Extension)(nil)
	_ extension.TurnHook   = (*Extension)(nil)
	_ extension.MoveHook   = (*Extension)(nil)
)

// New returns the extension over svc, with DefaultIndexingDelay.
func New(svc *engine.Service) *Extension {
	return &Extension{Service: svc, IndexingDelay: DefaultIndexingDelay}
}

// Load loads the extension into the app that host describes: it brings its
// tables in the app's database up to date, records the working directory
// into them, leaving out the data directory, and is disabled when its
// Settings turn snapshots off.
func Load(ctx context.Context, host extension.Host) (extension.Extension, error) {
	var st Settings
	if err := host.Config.Extension(Name, &st); err != nil {
		return nil, err
	}
	if err := database.Migrate(ctx, host.DB, Name, engine.Migrations); err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	svc := engine.NewService(engine.NewStore(host.DB, host.Project), host.WorkingDir, host.DataDir)
	svc.Disabled = !st.On()
	if !svc.Disabled {
		if err := svc.SchedulePrune(ctx, time.Hour); err != nil {
			slog.Info("snapshot pruning is off", "err", err)
		}
	}
	e := New(svc)
	delay, err := config.SnapshotDelay()
	if err != nil {
		slog.Warn("snapshot delay is off", "err", err)
	}
	e.TrackDelay = delay
	return e, nil
}

// Name returns Name.
func (e *Extension) Name() string { return Name }

// Disabled reports whether snapshots are disabled, as they are when the
// extension has no Service.
func (e *Extension) Disabled() bool { return e.Service == nil || e.Service.Disabled }

// Patch returns the unified diff between two recorded trees. It returns an
// empty patch when snapshots are disabled, either tree is the zero Hash,
// which a step records when tracking failed, or the trees are equal. It
// reads only recorded trees, so it stays answerable while a run is in
// progress.
func (e *Extension) Patch(ctx context.Context, from, to Hash) (string, error) {
	if e.Disabled() || from.IsZero() || to.IsZero() {
		return "", nil
	}
	return e.Service.Patch(ctx, from, to)
}
