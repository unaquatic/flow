package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"slices"
	"sync"
)

// Guard is the driver of the databases that [Open] opens. It wraps the
// SQLite driver so that a process stops using the database once another
// process, which runs a newer build, has upgraded tables after this process
// opened them. Without it the older process would go on writing rows in the
// shape and meaning its build knows.
//
// [Migrate] tells the guard which version of each owner's tables this build
// knows. Each connection then compares those versions with the ones the
// migrations table records at the start of every transaction and before
// every statement it runs outside one. Once an owner's tables are ahead,
// every transaction and statement fails with [ErrNewerSchema], until the
// process exits; recorded versions only grow.
//
// A transaction is checked after BEGIN. Open begins transactions IMMEDIATE,
// so the check runs under the write lock and no upgrade can land between it
// and the transaction's writes. A statement outside a transaction is checked
// just before it runs, which leaves an upgrade a moment to land in between:
// writes that must not race an upgrade run in a transaction.
//
// A connection reads the migrations table again only when PRAGMA
// data_version reports a commit by another connection or Migrate has
// recorded a version since, so a check is otherwise one pragma.
type Guard struct {
	// Inner is the SQLite driver the guard wraps.
	Inner driver.Driver
	// DSN is the data source name the guard opens connections with.
	DSN string

	mu sync.Mutex
	// known holds, by owner ID, the number of steps this build knows.
	known map[string]int
	// generation counts the versions recorded in known, so that a
	// connection notices one recorded after its last check.
	generation uint64
	// newer is the error of the first check that found an owner ahead.
	newer error
}

var (
	_ driver.Driver    = (*Guard)(nil)
	_ driver.Connector = (*Guard)(nil)
)

// Open implements [driver.Driver]: it opens a connection of Inner to name
// and guards it.
func (g *Guard) Open(name string) (driver.Conn, error) {
	c, err := g.Inner.Open(name)
	if err != nil {
		return nil, err
	}
	inner, ok := c.(Conn)
	if !ok {
		return nil, errors.Join(fmt.Errorf("database: driver connection %T lacks context methods", c), c.Close())
	}
	return &GuardConn{Guard: g, Inner: inner}, nil
}

// Connect implements [driver.Connector]: it opens a guarded connection to
// DSN.
func (g *Guard) Connect(context.Context) (driver.Conn, error) { return g.Open(g.DSN) }

// Driver implements [driver.Connector]. It returns g, which is how
// [Migrate] finds the guard of a database.
func (g *Guard) Driver() driver.Driver { return g }

// Know records that this build knows version of the tables owned by id,
// which Migrate has brought db to.
func (g *Guard) Know(id string, version int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.known == nil {
		g.known = make(map[string]int)
	}
	g.known[id] = version
	g.generation++
}

// State returns the generation of the known versions and the error of a
// check that found an owner ahead, if one has.
func (g *Guard) State() (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.generation, g.newer
}

// Compare checks recorded, the latest version of each owner that the
// migrations table records, against the versions this build knows. It fails
// with [ErrNewerSchema] for the first owner, by ID, that is ahead, and so
// does every later check. Owners this build has not migrated, such as an
// extension it does not load, are not compared.
func (g *Guard) Compare(recorded map[string]int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.newer != nil {
		return g.newer
	}
	for _, id := range slices.Sorted(maps.Keys(recorded)) {
		if known, ok := g.known[id]; ok && recorded[id] > known {
			g.newer = fmt.Errorf("database: %s tables are at v%d, this program knows up to v%d; restart it to run the newer build: %w",
				id, recorded[id], known, ErrNewerSchema)
			return g.newer
		}
	}
	return nil
}

// Conn is a SQLite driver connection, with the optional methods of
// package database/sql/driver that the guard passes on.
type Conn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// GuardConn is a connection that [Guard] checks. Like every driver
// connection, it is used by one goroutine at a time.
type GuardConn struct {
	Guard *Guard
	Inner Conn
	// InTx reports whether a transaction is open on the connection, which
	// its statements need no check of their own.
	InTx bool

	// checked reports whether a check passed, at the data version and
	// generation below.
	checked     bool
	dataVersion int64
	generation  uint64
}

var _ Conn = (*GuardConn)(nil)

// Check fails with [ErrNewerSchema] if the tables of an owner this build
// knows are at a version past the one it knows.
func (c *GuardConn) Check(ctx context.Context) error {
	generation, err := c.Guard.State()
	if err != nil {
		return err
	}
	if generation == 0 {
		// No owner is known yet, so there is nothing to compare, and
		// the migrations table may not exist.
		return nil
	}
	var version int64
	for row, err := range c.Rows(ctx, `PRAGMA data_version`) {
		if err != nil {
			return fmt.Errorf("database: check schema: %w", err)
		}
		version, _ = row[0].(int64)
	}
	if c.checked && version == c.dataVersion && generation == c.generation {
		return nil
	}
	// Read after data_version, so that a commit in between makes the next
	// check read again rather than go unseen.
	recorded := make(map[string]int)
	for row, err := range c.Rows(ctx, `SELECT id, MAX(version) FROM migrations GROUP BY id`) {
		if err != nil {
			return fmt.Errorf("database: check schema: %w", err)
		}
		id, _ := row[0].(string)
		v, _ := row[1].(int64)
		recorded[id] = int(v)
	}
	if err := c.Guard.Compare(recorded); err != nil {
		return err
	}
	c.checked, c.dataVersion, c.generation = true, version, generation
	return nil
}

// Rows runs query, which takes no arguments, on the wrapped connection
// without a check, and yields the values of each row, which the next row
// overwrites. A failure is yielded last, with nil values.
func (c *GuardConn) Rows(ctx context.Context, query string) iter.Seq2[[]driver.Value, error] {
	return func(yield func([]driver.Value, error) bool) {
		rows, err := c.Inner.QueryContext(ctx, query, nil)
		if err != nil {
			yield(nil, err)
			return
		}
		values := make([]driver.Value, len(rows.Columns()))
		for {
			err := rows.Next(values)
			if err == io.EOF {
				break
			}
			if err != nil {
				yield(nil, errors.Join(err, rows.Close()))
				return
			}
			if !yield(values, nil) {
				rows.Close()
				return
			}
		}
		if err := rows.Close(); err != nil {
			yield(nil, err)
		}
	}
}

// CheckStatement checks the connection before a statement runs, unless the
// statement runs in a transaction, which BeginTx has checked.
func (c *GuardConn) CheckStatement(ctx context.Context) error {
	if c.InTx {
		return nil
	}
	return c.Check(ctx)
}

// Close implements [driver.Conn].
func (c *GuardConn) Close() error { return c.Inner.Close() }

// Begin implements [driver.Conn].
//
// Deprecated: database/sql calls BeginTx.
func (c *GuardConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx implements [driver.ConnBeginTx]. It checks the connection once the
// transaction holds its lock, and rolls the transaction back if the check
// fails.
func (c *GuardConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Inner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := c.Check(ctx); err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	c.InTx = true
	return &GuardTx{Conn: c, Inner: tx}, nil
}

// Prepare implements [driver.Conn].
func (c *GuardConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext implements [driver.ConnPrepareContext]. The statement is
// checked each time it runs.
func (c *GuardConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	s, err := c.Inner.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	inner, ok := s.(Stmt)
	if !ok {
		return nil, errors.Join(fmt.Errorf("database: driver statement %T lacks context methods", s), s.Close())
	}
	return &GuardStmt{Conn: c, Inner: inner}, nil
}

// ExecContext implements [driver.ExecerContext].
func (c *GuardConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.CheckStatement(ctx); err != nil {
		return nil, err
	}
	return c.Inner.ExecContext(ctx, query, args)
}

// QueryContext implements [driver.QueryerContext].
func (c *GuardConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.CheckStatement(ctx); err != nil {
		return nil, err
	}
	return c.Inner.QueryContext(ctx, query, args)
}

// Ping implements [driver.Pinger].
func (c *GuardConn) Ping(ctx context.Context) error { return c.Inner.Ping(ctx) }

// ResetSession implements [driver.SessionResetter].
func (c *GuardConn) ResetSession(ctx context.Context) error { return c.Inner.ResetSession(ctx) }

// IsValid implements [driver.Validator].
func (c *GuardConn) IsValid() bool { return c.Inner.IsValid() }

// GuardTx is a transaction on a [GuardConn].
type GuardTx struct {
	Conn  *GuardConn
	Inner driver.Tx
}

// Commit implements [driver.Tx].
func (t *GuardTx) Commit() error {
	t.Conn.InTx = false
	return t.Inner.Commit()
}

// Rollback implements [driver.Tx].
func (t *GuardTx) Rollback() error {
	t.Conn.InTx = false
	return t.Inner.Rollback()
}

// Stmt is a SQLite driver statement, with the optional methods of package
// database/sql/driver that the guard passes on.
type Stmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

// GuardStmt is a prepared statement on a [GuardConn], checked each time it
// runs outside a transaction.
type GuardStmt struct {
	Conn  *GuardConn
	Inner Stmt
}

var _ Stmt = (*GuardStmt)(nil)

// Close implements [driver.Stmt].
func (s *GuardStmt) Close() error { return s.Inner.Close() }

// NumInput implements [driver.Stmt].
func (s *GuardStmt) NumInput() int { return s.Inner.NumInput() }

// Exec implements [driver.Stmt].
//
// Deprecated: database/sql calls ExecContext.
func (s *GuardStmt) Exec(args []driver.Value) (driver.Result, error) {
	if err := s.Conn.CheckStatement(context.Background()); err != nil {
		return nil, err
	}
	return s.Inner.Exec(args)
}

// Query implements [driver.Stmt].
//
// Deprecated: database/sql calls QueryContext.
func (s *GuardStmt) Query(args []driver.Value) (driver.Rows, error) {
	if err := s.Conn.CheckStatement(context.Background()); err != nil {
		return nil, err
	}
	return s.Inner.Query(args)
}

// ExecContext implements [driver.StmtExecContext].
func (s *GuardStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.Conn.CheckStatement(ctx); err != nil {
		return nil, err
	}
	return s.Inner.ExecContext(ctx, args)
}

// QueryContext implements [driver.StmtQueryContext].
func (s *GuardStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.Conn.CheckStatement(ctx); err != nil {
		return nil, err
	}
	return s.Inner.QueryContext(ctx, args)
}
