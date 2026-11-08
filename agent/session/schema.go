package session

// Migrations are the schema upgrades of the tables sessions are stored in,
// in version order, which [Open] applies with database.Migrate under [Name].
// An applied migration is never edited: databases have already recorded it,
// so a change to the tables, or to what their values mean, is always a new
// entry, even an empty one (""): it stops older builds still running from
// writing to them.
var Migrations = []string{
	migrationV1,
	migrationV2,
}

// migrationV2 stores a message's provider apart from its model, in a
// provider column, and its model without it, in "model:option:effort" form
// with the model's whole catalog ID, as agent.Variant.Ref writes it, such as
// lab-a/model-a:fast:xhigh. Both are empty when the model is not known.
//
// It moves the first segment of each model in the older
// "provider/model:option:effort" form to the provider column. That form
// left out the model's lab when the lab was the provider, as
// provider-a/model-a, so a model left without a lab gets the provider as
// its lab back. The column has a default only because SQLite adds no NOT
// NULL column without one; every insert names it.
const migrationV2 = `
ALTER TABLE messages ADD COLUMN provider TEXT NOT NULL DEFAULT '';

UPDATE messages
SET provider = substr(model, 1, instr(model, '/') - 1),
    model = CASE
        WHEN instr(substr(model, instr(model, '/') + 1), '/') > 0 THEN substr(model, instr(model, '/') + 1)
        ELSE model
    END
WHERE instr(model, '/') > 0;
`

// migrationV1 creates the tables.
//
// A session's row is written with its first message, at created_at, so a new
// session has none. Sessions belong to a project, whose table is package
// database's.
//
// Messages form a tree through parent_id, and a session reads back the path
// from its root to head_id; forward_id is the leaf a redo returns to. NULL
// stands for none in all three: a root message has no parent, and a session
// that shows no messages, or has nothing to redo, has no head or forward. A
// message's model is the variant that produced it, or that a prompt was sent
// to, in "provider/model:option:effort" form, such as
// provider-a/model-a:fast:xhigh: the catalog variant's option, which
// prices its usage, is left out for the default variant, and the effort when
// the provider chose it. Telling the option and the effort from the rest of
// the model ID takes the catalog. migrationV2 moves the provider to a column
// of its own.
//
// Foreign keys hold every link: a message's session, its parent, which is a
// message of the same session, and a session's head and forward, which are
// messages of the session. Sessions and messages reference each other, and a
// session's row is written after its first message, so the keys are checked
// when the transaction commits.
//
// A message's type says what it is for, as message.Type names it: 'chat'
// for the session's conversation, or another type, such as 'title', for the
// forks that AppendFork stores below a message. A type is lowercase ASCII
// letters, digits and underscores, led by a letter, and a role is one of the
// names message.Role writes; the table refuses any other value. The head
// never rests on a fork, and the reads of the conversation leave forks out,
// but deleting the message one hangs from deletes it too. A message's
// updated_at is when it last changed; a stored message does not change, so
// it is its created_at.
//
// Deleting removes rows. Deleting a session removes its messages, whose
// parts follow them, through the foreign keys. DeleteMessage removes a
// message and every message below it itself, rather than through a key on
// parent_id: SQLite cascades one level of a tree at a time, up to a depth
// far below that of a long conversation.
//
// Parts live in their own rows, ordered by seq, because they are the only
// part of a message whose shape varies. A part's ID and type, as
// message.ContentType names it, such as 'text', 'tool' or an extension's
// 'snapshot', are columns of their own, and data is a JSON object of its
// other fields, which the type decides: the tagged form message.PartJSON
// encodes, without "type" and "id". A part is stored with its message and
// does not change, so its times are its message's. The token usage of a
// reply is one of them, so messages holds no counts.
//
// Session and message times are RFC 3339 to whole seconds. The nanosecond
// form omits the fraction when it is zero, which makes values of differing
// width and stops the column sorting chronologically. Only sessions carries
// defaults, because only sessions is written by inserts that name some of
// its columns.
const migrationV1 = `
CREATE TABLE sessions (
	id           TEXT    PRIMARY KEY,
	project      INTEGER NOT NULL REFERENCES projects (id),
	title        TEXT    NOT NULL DEFAULT '',
	created_at   TEXT    NOT NULL,
	updated_at   TEXT    NOT NULL,
	head_id      TEXT,
	forward_id   TEXT,
	FOREIGN KEY (id, head_id) REFERENCES messages (session_id, id) DEFERRABLE INITIALLY DEFERRED,
	FOREIGN KEY (id, forward_id) REFERENCES messages (session_id, id) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX sessions_project ON sessions (project, updated_at);

CREATE TABLE messages (
	seq        INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
	id         TEXT NOT NULL,
	parent_id  TEXT,
	type       TEXT NOT NULL CHECK (type GLOB '[a-z]*' AND type NOT GLOB '*[^a-z0-9_]*'),
	role       TEXT NOT NULL CHECK (role IN ('agent', 'user', 'system')),
	model      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (session_id, id),
	FOREIGN KEY (session_id, parent_id) REFERENCES messages (session_id, id) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX messages_parent ON messages (session_id, parent_id);

CREATE TABLE message_parts (
	seq     INTEGER PRIMARY KEY,
	message INTEGER NOT NULL REFERENCES messages (seq) ON DELETE CASCADE,
	id      TEXT    NOT NULL CHECK (id <> ''),
	type    TEXT    NOT NULL CHECK (type GLOB '[a-z]*' AND type NOT GLOB '*[^a-z0-9_]*'),
	data    TEXT    NOT NULL CHECK (json_type(data) = 'object'),
	UNIQUE (message, id)
);

CREATE INDEX message_parts_message ON message_parts (message, seq);
`
