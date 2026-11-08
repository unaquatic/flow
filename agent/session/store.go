package session

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/database"
)

// Store persists sessions and messages in the database, which every
// project shares. A Store works on behalf of one project: the sessions it
// creates belong to that project, and it lists only that project's sessions.
//
// Sessions become visible when their first message is appended. Messages
// form a tree rooted at a session's first message. Reads are scoped to one
// branch: Branch and Path walk parent links back from a leaf, so a session
// can retain alternative continuations that no read returns.
//
// Deleting removes rows: a session with its messages and their parts, and a
// message with every message below it.
type Store struct {
	db      *sql.DB
	project database.ProjectID
}

// NewStore returns a store working on behalf of project in db, whose session
// tables [Open] has created. Unlike Open, it leaves the tables as they are.
// Closing db is up to the caller.
func NewStore(db *sql.DB, project database.ProjectID) *Store {
	return &Store{db: db, project: project}
}

// CreateSession returns a new session. The session is stored only once its
// first message is appended.
func (s *Store) CreateSession(_ context.Context) *Session {
	now := time.Now().UTC()
	return &Session{
		ID:        message.NewSessionID(),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// sessionColumns lists the session fields every session query selects, in
// the order scanSession reads them.
const sessionColumns = `id, title, head_id, forward_id, created_at, updated_at`

// ListSessions returns the project's sessions, newest first.
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sessionColumns+` FROM sessions
		WHERE project = ?
		ORDER BY updated_at DESC, id`, s.project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// Session returns a session by ID, whichever project it belongs to. The
// error wraps [fs.ErrNotExist] when the store holds no such session.
func (s *Store) Session(ctx context.Context, id message.SessionID) (*Session, error) {
	return lookupSession(id, s.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
}

// FindSession returns a session of the store's project by ID. The error
// wraps [fs.ErrNotExist] when the project has no such session.
func (s *Store) FindSession(ctx context.Context, id message.SessionID) (*Session, error) {
	return lookupSession(id, s.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+` FROM sessions
		WHERE id = ? AND project = ?`, id, s.project))
}

// Stored reports whether id is a stored session of the store's project. A
// session is stored with its first message, so a new one is not.
func (s *Store) Stored(ctx context.Context, id message.SessionID) (bool, error) {
	_, err := s.FindSession(ctx, id)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// lookupSession returns session id from row, which selects sessionColumns.
// The error wraps [fs.ErrNotExist] when row is empty.
func lookupSession(id message.SessionID, row *sql.Row) (*Session, error) {
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("session %s: %w", id, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// scanSession reads one session from r, a row of a query that selects
// sessionColumns.
func scanSession[R interface{ Scan(dest ...any) error }](r R) (Session, error) {
	var (
		sess                 Session
		head, forward        sql.Null[string]
		createdAt, updatedAt string
	)
	if err := r.Scan(&sess.ID, &sess.Title, &head, &forward, &createdAt, &updatedAt); err != nil {
		return Session{}, err
	}
	sess.Head = Head{ID: message.ID(head.V), Forward: message.ID(forward.V)}
	var err error
	if sess.CreatedAt, err = decodeTime(createdAt); err != nil {
		return Session{}, err
	}
	if sess.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// DeleteSession removes a session, and its messages and their parts with it,
// through the foreign keys. The error wraps [fs.ErrNotExist] when the store
// holds no such session.
func (s *Store) DeleteSession(ctx context.Context, id message.SessionID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return sessionFound(res, id)
}

// AppendMessage stamps msg's creation time, and appends msg and its parts
// below the session's head, in one transaction, and moves the head onto it.
// Appending while the head sits mid-tree starts a new branch. A stored
// message is not changed afterwards. Appending to a session the store does
// not hold, as a new one or a deleted one, stores the session with msg.
// Appending a message that is not chat, which AppendFork stores, fails with
// [ErrMessageType].
func (s *Store) AppendMessage(ctx context.Context, sessionID message.SessionID, msg *message.Content) error {
	if !msg.Type.IsChat() {
		return fmt.Errorf("%w: append a %s message to the conversation", ErrMessageType, msg.Type)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	head, err := currentHead(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	// Insert from a copy so a failed append leaves the caller's message alone.
	stored := *msg
	stored.ParentID = optionalID(head.ID)
	stored.CreatedAt = time.Now().UTC()
	seq, err := insertMessage(ctx, tx, sessionID, &stored)
	if err != nil {
		return err
	}
	if err := insertParts(ctx, tx, seq, msg.Parts); err != nil {
		return err
	}
	// The appended message ends its branch, so nothing lies ahead of it.
	if err := s.moveHead(ctx, tx, sessionID, Head{ID: msg.ID}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	msg.ParentID, msg.CreatedAt = stored.ParentID, stored.CreatedAt
	return nil
}

// AppendFork stores msgs as a fork below parent, a message of the session,
// each below the one before it, in one transaction, and sets each one's
// parent and creation time. A fork records an exchange that branched off
// the conversation, such as the request that titled the session and its
// reply, and its messages are of a type other than chat, which says what
// it is for: the head does not move onto it, the session's update time
// stays, and the reads of the conversation leave it out, as Branch,
// Children, Forks, Leaf, Messages and Size do. ForkChildren and Path read
// it. Deleting parent deletes it too. It fails with [ErrMessageType] when a
// message of msgs is chat, and with [ErrMessageNotFound] when the session
// holds no parent, as one not stored, or deleted, holds none.
func (s *Store) AppendFork(ctx context.Context, sessionID message.SessionID, parent message.ID, msgs ...*message.Content) error {
	for _, msg := range msgs {
		if msg.Type.IsChat() {
			return fmt.Errorf("%w: fork the conversation with a chat message", ErrMessageType)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var found bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM messages WHERE session_id = ? AND id = ?)`,
		sessionID, string(parent)).Scan(&found); err != nil {
		return err
	}
	if !found {
		return ErrMessageNotFound
	}
	stored := make([]message.Content, len(msgs))
	above := parent
	for i, msg := range msgs {
		stored[i] = *msg
		stored[i].ParentID = optionalID(above)
		stored[i].CreatedAt = time.Now().UTC()
		seq, err := insertMessage(ctx, tx, sessionID, &stored[i])
		if err != nil {
			return err
		}
		if err := insertParts(ctx, tx, seq, msg.Parts); err != nil {
			return err
		}
		above = msg.ID
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for i, msg := range msgs {
		msg.ParentID, msg.CreatedAt = stored[i].ParentID, stored[i].CreatedAt
	}
	return nil
}

// Message returns one message, or [ErrMessageNotFound].
func (s *Store) Message(ctx context.Context, sessionID message.SessionID, messageID message.ID) (*message.Content, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+messageColumns+`, `+partColumns+`
		FROM messages m
		`+partsJoinSQL+`
		WHERE m.session_id = ? AND m.id = ?
		ORDER BY mp.seq`, sessionID, string(messageID))
	if err != nil {
		return nil, err
	}
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, ErrMessageNotFound
	}
	return &msgs[0], nil
}

// DeleteMessage removes messageID and every message below it, forks too,
// and their parts with them. A head left inside the removed subtree falls
// back to the removed message's parent, and a forward marker there is
// cleared. It fails with [ErrMessageNotFound] when the session holds no such
// message.
func (s *Store) DeleteMessage(ctx context.Context, sessionID message.SessionID, messageID message.ID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var parent sql.Null[string]
	err = tx.QueryRowContext(ctx, `
		SELECT parent_id FROM messages WHERE session_id = ? AND id = ?`,
		sessionID, string(messageID)).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, deleteSubtreeSQL,
		sessionID, string(messageID), sessionID, sessionID); err != nil {
		return err
	}
	if err := repairHead(ctx, tx, sessionID, message.ID(parent.V)); err != nil {
		return err
	}
	if err := touchSession(ctx, tx, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTitle sets or clears a session title. A session is stored with its first
// message, so titling a new one fails with an error wrapping [fs.ErrNotExist],
// as does titling a session that is not stored at all, or deleted.
func (s *Store) SetTitle(ctx context.Context, sessionID message.SessionID, title string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET title = ? WHERE id = ?`, title, sessionID)
	if err != nil {
		return err
	}
	return sessionFound(res, sessionID)
}

// UpdateSession sets a session's title, as SetTitle does, and returns the
// updated session.
func (s *Store) UpdateSession(ctx context.Context, id message.SessionID, title string) (*Session, error) {
	if err := s.SetTitle(ctx, id, title); err != nil {
		return nil, err
	}
	return s.Session(ctx, id)
}

// SetHead moves the session onto another point in its message tree. Moving the
// head does not reorder the session list, because it changes what the session
// shows rather than what it contains. The error wraps [fs.ErrNotExist] when
// the session is not stored, or deleted. Otherwise the head and the forward
// marker must each be empty or name a message of the conversation: one the
// session does not hold fails with [ErrMessageNotFound], and a message of a
// fork with [ErrMessageType].
func (s *Store) SetHead(ctx context.Context, sessionID message.SessionID, h Head) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE sessions SET head_id = ?, forward_id = ? WHERE id = ?`,
		nullableID(h.ID), nullableID(h.Forward), sessionID)
	if err != nil {
		return err
	}
	if err := sessionFound(res, sessionID); err != nil {
		return err
	}
	for _, id := range []message.ID{h.ID, h.Forward} {
		if err := checkHeadAt(ctx, tx, sessionID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// checkHeadAt reports whether a head or forward marker may rest on id: the
// empty ID, or a chat message of the session.
func checkHeadAt(ctx context.Context, tx *sql.Tx, sessionID message.SessionID, id message.ID) error {
	if id == "" {
		return nil
	}
	var typ string
	err := tx.QueryRowContext(ctx, `
		SELECT type FROM messages WHERE session_id = ? AND id = ?`,
		sessionID, string(id)).Scan(&typ)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrMessageNotFound
	case err != nil:
		return err
	case !message.Type(typ).IsChat():
		return fmt.Errorf("%w: the head rests on the %s message %s", ErrMessageType, typ, id)
	}
	return nil
}

// insertMessage inserts one message row and returns its sequence number. Its
// parts, its usage among them, are stored apart by insertParts. A role that
// names no [message.Role] fails with [message.ErrUnknownRole], before the
// table refuses it.
func insertMessage(ctx context.Context, tx *sql.Tx, sessionID message.SessionID, msg *message.Content) (int64, error) {
	if _, err := message.ParseRole(msg.Role.String()); err != nil {
		return 0, fmt.Errorf("message %s: %w", msg.ID, err)
	}
	var parent message.ID
	if msg.ParentID != nil {
		parent = *msg.ParentID
	}
	created := encodeTime(msg.CreatedAt)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO messages (session_id, id, parent_id, type, role, model, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, string(msg.ID), nullableID(parent), string(cmp.Or(msg.Type, message.TypeChat)),
		msg.Role.String(), msg.Model, created, created)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// insertParts stores parts below one message, in order.
func insertParts(ctx context.Context, tx *sql.Tx, msgSeq int64, parts []message.ContentPart) error {
	for _, p := range parts {
		id, typ, data, err := encodePart(p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts (message, id, type, data) VALUES (?, ?, ?, ?)`,
			msgSeq, string(id), string(typ), string(data)); err != nil {
			return err
		}
	}
	return nil
}

// sessionFound converts a result that changed no row to an error wrapping
// [fs.ErrNotExist].
func sessionFound(res sql.Result, id message.SessionID) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("session %s: %w", id, fs.ErrNotExist)
	}
	return nil
}

// currentHead returns the session's head, which is zero for a session that
// is not stored, and so holds no messages yet.
func currentHead(ctx context.Context, tx *sql.Tx, sessionID message.SessionID) (Head, error) {
	var head, forward sql.Null[string]
	err := tx.QueryRowContext(ctx, `
		SELECT head_id, forward_id FROM sessions WHERE id = ?`, sessionID).
		Scan(&head, &forward)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Head{}, nil
	case err != nil:
		return Head{}, err
	}
	return Head{ID: message.ID(head.V), Forward: message.ID(forward.V)}, nil
}

// moveHead stamps a session and points it at h, storing the session if it is
// new, created now. A session it stores belongs to the store's project.
func (s *Store) moveHead(ctx context.Context, tx *sql.Tx, sessionID message.SessionID, h Head) error {
	now := encodeTime(time.Now())
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (id, project, created_at, updated_at, head_id, forward_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			updated_at = excluded.updated_at,
			head_id = excluded.head_id,
			forward_id = excluded.forward_id`,
		sessionID, s.project, now, now, nullableID(h.ID), nullableID(h.Forward))
	return err
}

// repairHead points a head left on a removed message at fallback, the empty
// ID standing for none, and clears a forward marker left on one. It runs in
// the transaction that removed the message, before the keys on the head are
// checked.
func repairHead(ctx context.Context, tx *sql.Tx, sessionID message.SessionID, fallback message.ID) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE sessions SET
			head_id = CASE WHEN head_id IS NULL OR EXISTS (
				SELECT 1 FROM messages WHERE session_id = sessions.id AND id = sessions.head_id
			) THEN head_id ELSE ? END,
			forward_id = CASE WHEN forward_id IS NULL OR EXISTS (
				SELECT 1 FROM messages WHERE session_id = sessions.id AND id = sessions.forward_id
			) THEN forward_id ELSE NULL END
		WHERE id = ?`,
		nullableID(fallback), sessionID)
	return err
}

// touchSession updates a stored session's timestamp.
func touchSession(ctx context.Context, tx *sql.Tx, id message.SessionID) error {
	_, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`,
		encodeTime(time.Now()), id)
	return err
}

// nullableID renders a message ID for a nullable column, where the zero ID
// means no row is referenced.
func nullableID(id message.ID) any {
	if id == "" {
		return nil
	}
	return string(id)
}

// optionalID converts the zero ID to a nil pointer.
func optionalID(id message.ID) *message.ID {
	if id == "" {
		return nil
	}
	return &id
}

// encodePart encodes one content part as the columns of its row: its ID, its
// type, and data, the JSON object of its other fields, which is the tagged
// form message.PartJSON encodes without "type" and "id". A part that arrives
// without an ID is assigned one. A part of a type neither package message
// defines nor message.RegisterPart registered fails, since nothing could
// read it back.
func encodePart(p message.ContentPart) (message.PartID, message.ContentType, []byte, error) {
	j := message.PartToJSON(p)
	if j.PartID == "" {
		return "", "", nil, fmt.Errorf("store a %s part: the type is not registered", j.Type)
	}
	tagged, err := json.Marshal(j)
	if err != nil {
		return "", "", nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(tagged, &fields); err != nil {
		return "", "", nil, fmt.Errorf("store a %s part: %w", j.Type, err)
	}
	delete(fields, "type")
	delete(fields, "id")
	data, err := json.Marshal(fields)
	if err != nil {
		return "", "", nil, err
	}
	return j.PartID, j.Type, data, nil
}

// decodePart decodes one stored content part from the columns of its row,
// as encodePart wrote them: it puts "type" and "id" back in front of the
// other fields and decodes the tagged form.
func decodePart(id message.PartID, typ message.ContentType, data []byte) (message.ContentPart, error) {
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '{' {
		return nil, fmt.Errorf("part %s: data is not a JSON object", id)
	}
	head, err := json.Marshal(struct {
		Type message.ContentType `json:"type"`
		ID   message.PartID      `json:"id"`
	}{typ, id})
	if err != nil {
		return nil, err
	}
	tagged := head[:len(head)-1] // drop the closing brace
	if rest := bytes.TrimSpace(data[1:]); rest[0] != '}' {
		tagged = append(tagged, ',')
		tagged = append(tagged, rest...)
	} else {
		tagged = append(tagged, '}')
	}
	var j message.PartJSON
	if err := json.Unmarshal(tagged, &j); err != nil {
		return nil, fmt.Errorf("part %s: %w", id, err)
	}
	return message.PartFromJSON(j), nil
}

// encodeTime converts t to the form held by a time column.
//
// Seconds, not nanoseconds: RFC 3339 omits the fraction when it is zero, so a
// nanosecond column holds values of differing widths and stops sorting
// chronologically. Truncating makes every value 20 bytes, and a session list
// is ordered by id within a second.
func encodeTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// decodeTime converts a time column back to a UTC time.
func decodeTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
