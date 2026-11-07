package session

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unaquatic/flow/agent/message"
)

// messageColumns lists the message fields every message query selects, in scan
// order, for a query that joins the messages table as m.
const messageColumns = `m.seq, m.id, m.parent_id, m.role, m.model, m.created_at`

// liveSQL is the condition, for a query that reads the messages table as m,
// that m belongs to a session that is not deleted. A deleted session keeps
// its messages, and every message query starts from rows that meet it, so
// that none returns them. The messages a recursive query reaches from there
// belong to the same session.
const liveSQL = `EXISTS (
	SELECT 1 FROM sessions s WHERE s.id = m.session_id AND s.deleted_at IS NULL
)`

// pathSQL walks parent links from one message back to its root and attaches
// each message's parts. Depth counts steps taken upwards, so ordering by it
// descending yields root-first. The walk carries only what it needs to climb;
// message fields are joined back once the path is known.
const pathSQL = `
WITH RECURSIVE path(seq, parent, depth) AS (
	SELECT m.seq, m.parent_id, 0 FROM messages m
	WHERE m.session_id = ? AND m.id = ? AND ` + liveSQL + `
	UNION ALL
	SELECT m.seq, m.parent_id, path.depth + 1 FROM messages m
	JOIN path ON m.id = path.parent
	WHERE m.session_id = ?
)
SELECT ` + messageColumns + `, mp.data
FROM path
JOIN messages m ON m.seq = path.seq
LEFT JOIN message_parts mp ON mp.message = m.seq
ORDER BY path.depth DESC, mp.seq`

// messagesSQL selects every message of a session with its parts, in the order
// the messages were stored.
const messagesSQL = `
SELECT ` + messageColumns + `, mp.data
FROM messages m
LEFT JOIN message_parts mp ON mp.message = m.seq
WHERE m.session_id = ? AND ` + liveSQL + `
ORDER BY m.seq, mp.seq`

// childrenSQL selects the direct children of one message with their parts.
const childrenSQL = `
SELECT ` + messageColumns + `, mp.data
FROM messages m
LEFT JOIN message_parts mp ON mp.message = m.seq
WHERE m.session_id = ? AND m.parent_id IS ? AND ` + liveSQL + `
ORDER BY m.seq, mp.seq`

// subtreeSQL collects a message and every message descending from it.
const subtreeSQL = `
WITH RECURSIVE subtree(id, seq) AS (
	SELECT m.id, m.seq FROM messages m WHERE m.session_id = ? AND m.id = ? AND ` + liveSQL + `
	UNION ALL
	SELECT m.id, m.seq FROM messages m
	JOIN subtree ON m.parent_id = subtree.id
	WHERE m.session_id = ?
)`

// leafSQL selects the newest childless message in a subtree.
const leafSQL = subtreeSQL + `
SELECT subtree.id FROM subtree
WHERE NOT EXISTS (
	SELECT 1 FROM messages c WHERE c.session_id = ? AND c.parent_id = subtree.id
)
ORDER BY subtree.seq DESC LIMIT 1`

// sizeSQL counts a message together with everything below it.
const sizeSQL = subtreeSQL + `
SELECT COUNT(*) FROM subtree`

// deleteSubtreeSQL removes a message together with everything below it. Parts
// follow their message through the foreign key.
const deleteSubtreeSQL = subtreeSQL + `
DELETE FROM messages
WHERE session_id = ? AND id IN (SELECT id FROM subtree)`

// newestLeafSQL selects the newest childless message in a whole session.
const newestLeafSQL = `
SELECT m.id FROM messages m
WHERE m.session_id = ? AND ` + liveSQL + ` AND NOT EXISTS (
	SELECT 1 FROM messages c WHERE c.session_id = ? AND c.parent_id = m.id
)
ORDER BY m.seq DESC LIMIT 1`

// Branch returns the messages on the session's active branch, ordered from its
// root to its head.
func (s *Store) Branch(ctx context.Context, sessionID message.SessionID) ([]message.Content, error) {
	var head message.ID
	err := s.db.QueryRowContext(ctx, `
		SELECT head_id FROM sessions WHERE id = ? AND deleted_at IS NULL`, sessionID).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Path(ctx, sessionID, head)
}

// Path returns the messages from the session's root to leaf, in order.
func (s *Store) Path(ctx context.Context, sessionID message.SessionID, leaf message.ID) ([]message.Content, error) {
	if leaf == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, pathSQL, sessionID, string(leaf), sessionID)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Messages returns every message of the session, on every branch, in the
// order they were stored. A message is stored below one that exists already,
// so each parent precedes its children.
func (s *Store) Messages(ctx context.Context, sessionID message.SessionID) ([]message.Content, error) {
	rows, err := s.db.QueryContext(ctx, messagesSQL, sessionID)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Children returns the direct children of parent in creation order.
func (s *Store) Children(ctx context.Context, sessionID message.SessionID, parent message.ID) ([]message.Content, error) {
	rows, err := s.db.QueryContext(ctx, childrenSQL, sessionID, nullableID(parent))
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Leaf returns the newest leaf below from, or [ErrMessageNotFound] when the
// session holds no message from. An empty from searches the whole session,
// and returns an empty ID when the session holds no messages.
func (s *Store) Leaf(ctx context.Context, sessionID message.SessionID, from message.ID) (message.ID, error) {
	var (
		id  message.ID
		err error
	)
	if from == "" {
		err = s.db.QueryRowContext(ctx, newestLeafSQL, sessionID, sessionID).Scan(&id)
	} else {
		err = s.db.QueryRowContext(ctx, leafSQL, sessionID, string(from), sessionID, sessionID).Scan(&id)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows) && from == "":
		return "", nil
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrMessageNotFound
	}
	return id, err
}

// Forks returns the number of children of every message in the session that
// has more than one, keyed by that message's ID. Root messages are keyed by
// the empty ID.
func (s *Store) Forks(ctx context.Context, sessionID message.SessionID) (map[message.ID]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.parent_id, COUNT(*) FROM messages m
		WHERE m.session_id = ? AND `+liveSQL+`
		GROUP BY m.parent_id HAVING COUNT(*) > 1`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	forks := make(map[message.ID]int)
	for rows.Next() {
		var (
			parent sql.Null[string]
			n      int
		)
		if err := rows.Scan(&parent, &n); err != nil {
			return nil, err
		}
		forks[message.ID(parent.V)] = n
	}
	return forks, rows.Err()
}

// Size returns the number of messages in the subtree rooted at from.
func (s *Store) Size(ctx context.Context, sessionID message.SessionID, from message.ID) (int, error) {
	if from == "" {
		return 0, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, sizeSQL, sessionID, string(from), sessionID).Scan(&n)
	return n, err
}

// messageRow is one row of a message-and-part join, which repeats a message
// once for every part it holds.
type messageRow struct {
	// Seq is the message's row sequence. The rows of one message share it.
	Seq int64
	// Message is the row's message, carrying none of its parts.
	Message message.Content
	// Part is the part this row carries, or nil when the message has none.
	Part message.ContentPart
}

// scan reads one joined row. Its targets follow messageColumns in order, with
// the part last.
func (r *messageRow) scan(rows *sql.Rows) error {
	m := &r.Message
	var (
		parent    sql.Null[string]
		role      string
		createdAt string
		part      []byte
	)
	err := rows.Scan(&r.Seq, &m.ID, &parent, &role, &m.Model, &createdAt, &part)
	if err != nil {
		return err
	}
	m.Role = message.ParseRole(role)
	m.ParentID = optionalID(message.ID(parent.V))
	if m.CreatedAt, err = decodeTime(createdAt); err != nil {
		return err
	}
	if part == nil {
		return nil
	}
	r.Part, err = decodePart(part)
	return err
}

// scanMessages decodes and closes a message-and-part join. The rows of one
// message are consecutive and ordered by part, so a row either opens the next
// message or adds a part to the one already open.
func scanMessages(rows *sql.Rows) ([]message.Content, error) {
	defer rows.Close()

	var (
		msgs []message.Content
		open int64
	)
	for rows.Next() {
		var row messageRow
		if err := row.scan(rows); err != nil {
			return nil, err
		}
		if len(msgs) == 0 || row.Seq != open {
			msgs = append(msgs, row.Message)
			open = row.Seq
		}
		if row.Part != nil {
			current := &msgs[len(msgs)-1]
			current.Parts = append(current.Parts, row.Part)
		}
	}
	return msgs, rows.Err()
}
