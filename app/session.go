package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/internal/flock"
)

var (
	// ErrSessionBusy reports that another process is changing the
	// session.
	ErrSessionBusy = errors.New("session is in use by another process")

	// ErrInvalidSessionID reports a session ID that holds characters other
	// than ASCII letters, digits, '-' and '_'. Such an ID cannot name the
	// session's lock file, so the session cannot be changed.
	ErrInvalidSessionID = errors.New("invalid session ID")
)

// sessionLockWait is how long lockSession waits for a busy session lock. It
// covers a brief probe by BusySessions, not another process's operation,
// which lockSession reports at once as ErrSessionBusy.
const sessionLockWait = 100 * time.Millisecond

// Sessions returns the store of the project's sessions and messages.
func (a *App) Sessions() *session.Store {
	return a.sessions
}

// DeleteSession stops the session's operation in progress, waits for it to
// end, removes the session while holding it, with its messages and their
// parts, as [session.Store.DeleteSession] does, and publishes
// EventSessionDeleted. Nothing is deleted, and the error is
// ErrSessionBusy, when another process is changing the session, or ErrBusy
// when another operation starts on it first. The error wraps
// [fs.ErrNotExist] when no session sessionID is stored.
func (a *App) DeleteSession(ctx context.Context, sessionID message.SessionID) error {
	if op := a.Operation(sessionID); op != nil {
		a.Session(sessionID).Stop()
		select {
		case <-op.Done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_, release, err := a.hold(ctx, sessionID)
	if err != nil {
		return err
	}
	defer release()
	if err := a.sessions.DeleteSession(ctx, sessionID); err != nil {
		return err
	}
	s := a.Session(sessionID)
	s.publish(EventSessionDeleted{EventBase: s.eventBase()})
	return nil
}

// SetTitle sets or clears the session's title, publishes the session as
// EventSessionUpdated, and returns it as it is stored now. A session is
// stored with its first message, so titling a new one fails with an error
// wrapping [fs.ErrNotExist], as does titling one that is not stored at all,
// or deleted.
func (s Session) SetTitle(title string) (*session.Session, error) {
	sess, err := s.app.sessions.UpdateSession(s.ctx, s.id, title)
	if err != nil {
		return nil, err
	}
	s.publish(s.sessionUpdated(sess))
	return sess, nil
}

// publishStored publishes the session as it is stored now, as
// EventSessionUpdated.
func (s Session) publishStored() error {
	sess, err := s.app.sessions.Session(s.ctx, s.id)
	if err != nil {
		return err
	}
	s.publish(s.sessionUpdated(sess))
	return nil
}

// sessionUpdated returns the EventSessionUpdated that reports sess, the
// session's stored record.
func (s Session) sessionUpdated(sess *session.Session) EventSessionUpdated {
	return EventSessionUpdated{EventBase: s.eventBase(), Title: sess.Title, CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt}
}

// BusySessions returns the set of busy sessions: every session with an
// operation in progress, and every other session of the project that another
// process is changing.
func (a *App) BusySessions(ctx context.Context) (map[message.SessionID]bool, error) {
	a.mu.Lock()
	busy := make(map[message.SessionID]bool, len(a.operations))
	for id := range a.operations {
		busy[id] = true
	}
	a.mu.Unlock()

	sessions, err := a.sessions.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if !busy[sess.ID] && a.sessionLocked(sess.ID) {
			busy[sess.ID] = true
		}
	}
	return busy, nil
}

// sessionLockPath returns the path of the lock file of session id. It reports
// ErrInvalidSessionID for an ID that is not safe as part of a file name.
func (a *App) sessionLockPath(id message.SessionID) (string, error) {
	unsafe := func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_')
	}
	if id == "" || strings.ContainsFunc(string(id), unsafe) {
		return "", fmt.Errorf("%w: %s", ErrInvalidSessionID, id)
	}
	return filepath.Join(a.lockDir, string(id)+".lock"), nil
}

// lockSession takes the exclusive lock of session id, so that no other
// process changes the session until unlock is called. Locks conflict
// between open files, so it also excludes other lockSession calls of this
// process. It reports ErrSessionBusy when another holder keeps the lock for
// longer than sessionLockWait.
//
// Lock files are never removed, not even with their session: removing one
// that another process has open could let two processes hold the lock at once.
func (a *App) lockSession(ctx context.Context, id message.SessionID) (unlock func(), err error) {
	path, err := a.sessionLockPath(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(a.lockDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, sessionLockWait)
	defer cancel()
	if err := flock.Lock(waitCtx, f, true); err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			err = ErrSessionBusy
		}
		return nil, errors.Join(err, f.Close())
	}
	return func() {
		if err := errors.Join(flock.Unlock(f), f.Close()); err != nil {
			slog.Error("unlock session", "session", id, "err", err)
		}
	}, nil
}

// sessionLocked reports whether an operation of this process or another holds
// the lock of session id. It probes with a shared lock that it releases at
// once.
func (a *App) sessionLocked(id message.SessionID) bool {
	path, err := a.sessionLockPath(id)
	if err != nil {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		// A session without a lock file has never been locked.
		return false
	}
	defer f.Close()
	ok, err := flock.TryLock(f, false)
	if err != nil {
		slog.Error("probe session lock", "session", id, "err", err)
		return false
	}
	if !ok {
		return true
	}
	if err := flock.Unlock(f); err != nil {
		slog.Error("unlock session probe", "session", id, "err", err)
	}
	return false
}
