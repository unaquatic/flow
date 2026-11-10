package database

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// ownedV1 is the first step of the tables of owner "owner" in the guard
// tests.
const ownedV1 = `CREATE TABLE owned (x INTEGER)`

// openMigrated opens the database at path, as a process running a build that
// knows steps of owner "owner" does, and migrates it.
func openMigrated(t *testing.T, path string, steps ...string) *sql.DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(t.Context(), db, "owner", steps); err != nil {
		t.Fatalf("migrate owner to v%d: %v", len(steps), err)
	}
	return db
}

// count returns the number of rows in owned.
func count(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM owned`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestGuard_StopsOlderBuildAfterUpgrade checks that once a newer build
// upgrades tables that an older build, still running, has migrated, the
// older one can neither write nor read them, whether the upgrade changes the
// tables or only what their values mean, while the newer one goes on.
func TestGuard_StopsOlderBuildAfterUpgrade(t *testing.T) {
	for name, step := range map[string]string{
		"tables change": `ALTER TABLE owned ADD COLUMN y INTEGER`,
		"meaning only":  ``,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			older := openMigrated(t, path, ownedV1)
			if _, err := older.Exec(`INSERT INTO owned (x) VALUES (1)`); err != nil {
				t.Fatalf("insert before the upgrade: %v", err)
			}
			prepared, err := older.Prepare(`INSERT INTO owned (x) VALUES (?)`)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()

			newer := openMigrated(t, path, ownedV1, step)

			attempts := map[string]func() error{
				"exec": func() error {
					_, err := older.Exec(`INSERT INTO owned (x) VALUES (2)`)
					return err
				},
				"prepared": func() error {
					_, err := prepared.Exec(3)
					return err
				},
				"transaction": func() error {
					tx, err := older.Begin()
					if err == nil {
						tx.Rollback()
					}
					return err
				},
				"query": func() error {
					var n int
					return older.QueryRow(`SELECT COUNT(*) FROM owned`).Scan(&n)
				},
			}
			for name, attempt := range attempts {
				if err := attempt(); !errors.Is(err, ErrNewerSchema) {
					t.Errorf("older %s after the upgrade: err = %v, want ErrNewerSchema", name, err)
				}
			}
			if got := count(t, newer); got != 1 {
				t.Errorf("owned holds %d rows, want only the 1 written before the upgrade", got)
			}
			if _, err := newer.Exec(`INSERT INTO owned (x) VALUES (4)`); err != nil {
				t.Errorf("newer insert: %v", err)
			}
		})
	}
}

// TestGuard_KeepsSameBuildWorking checks that a build goes on using the
// database while another process of the same build writes to it, and while
// one migrates tables of an owner it does not know, such as an extension it
// does not load.
func TestGuard_KeepsSameBuildWorking(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	one := openMigrated(t, path, ownedV1)
	two := openMigrated(t, path, ownedV1)

	if _, err := two.Exec(`INSERT INTO owned (x) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), two, "other", []string{`CREATE TABLE other (x INTEGER)`, ``}); err != nil {
		t.Fatal(err)
	}
	tx, err := one.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO owned (x) VALUES (2)`); err != nil {
		t.Fatalf("insert in transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := one.Exec(`INSERT INTO owned (x) VALUES (3)`); err != nil {
		t.Errorf("insert: %v", err)
	}
	if got := count(t, one); got != 3 {
		t.Errorf("owned holds %d rows, want 3", got)
	}
}

// TestGuard_UpgradeWaitsForOpenTransaction checks that an upgrade waits for
// a transaction the older build has open, which so commits all its writes
// in the shape it checked, and that the older build stops after it.
func TestGuard_UpgradeWaitsForOpenTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	older := openMigrated(t, path, ownedV1)
	newer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { newer.Close() })

	tx, err := older.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO owned (x) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var upgradeErr error
	wg.Go(func() {
		upgradeErr = Migrate(t.Context(), newer, "owner", []string{ownedV1, ``})
	})
	// The upgrade waits for the write lock the transaction holds from its
	// start, so these writes land before it whenever it began.
	if _, err := tx.Exec(`INSERT INTO owned (x) VALUES (2)`); err != nil {
		t.Errorf("insert while the upgrade waits: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Errorf("commit while the upgrade waits: %v", err)
	}
	wg.Wait()
	if upgradeErr != nil {
		t.Fatalf("upgrade: %v", upgradeErr)
	}

	if _, err := older.Exec(`INSERT INTO owned (x) VALUES (3)`); !errors.Is(err, ErrNewerSchema) {
		t.Errorf("older insert after the upgrade: err = %v, want ErrNewerSchema", err)
	}
	if got := count(t, newer); got != 2 {
		t.Errorf("owned holds %d rows, want the 2 of the transaction", got)
	}
}
