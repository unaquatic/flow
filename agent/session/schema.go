package session

// Migrations are the schema upgrades of the tables sessions are stored in,
// in version order, which [Open] applies with database.Migrate under [Name].
// An applied migration is never edited: databases have already recorded it,
// so a change to the tables is always a new entry.
var Migrations = []string{
	migrationV1,
}

// migrationV1 creates the tables.
//
// A session's row is written with its first message, at created_at, so a new
// session has none. Deleting a session sets deleted_at and keeps the session
// and its messages, which no read returns: a deleted session reads as one
// that does not exist, though its ID stays taken. sessions_project indexes
// only the sessions that are not deleted, which are the only ones listed.
// Sessions belong to a project, whose table is package database's.
//
// Messages form a tree through parent_id, and a session reads back the path
// from its root to head_id; forward_id is the leaf a redo returns to. A
// message's model is the variant that produced it, or that a prompt was sent
// to, in "provider/model:option:effort" form, such as
// anthropic/claude-opus-5-5:fast:xhigh: the catalog variant's option, which
// prices its usage, is left out for the default variant, and the effort when
// the provider chose it. Telling the option and the effort from the rest of
// the model ID takes the catalog.
//
// Parts live in their own rows, ordered by seq, because they are the only
// part of a message whose shape varies. The token usage of a reply is one of
// them, so messages holds no counts.
//
// Session and message times are RFC 3339 to whole seconds. The nanosecond
// form omits the fraction when it is zero, which makes values of differing
// width and stops the column sorting chronologically. Only sessions carries
// defaults, because only sessions is written by inserts that name some of its
// columns.
const migrationV1 = `
CREATE TABLE sessions (
	id           TEXT    PRIMARY KEY,
	project      INTEGER NOT NULL REFERENCES projects (id),
	title        TEXT    NOT NULL DEFAULT '',
	created_at   TEXT    NOT NULL,
	updated_at   TEXT    NOT NULL,
	deleted_at   TEXT,
	head_id      TEXT    NOT NULL DEFAULT '',
	forward_id   TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX sessions_project ON sessions (project, updated_at) WHERE deleted_at IS NULL;

CREATE TABLE messages (
	seq        INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	id         TEXT NOT NULL,
	parent_id  TEXT,
	role       TEXT NOT NULL,
	model      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (session_id, id)
);

CREATE INDEX messages_parent ON messages (session_id, parent_id);

CREATE TABLE message_parts (
	seq     INTEGER PRIMARY KEY,
	message INTEGER NOT NULL REFERENCES messages (seq) ON DELETE CASCADE,
	id      TEXT NOT NULL,
	data    TEXT NOT NULL,
	UNIQUE (message, id)
);

CREATE INDEX message_parts_message ON message_parts (message, seq);
`
