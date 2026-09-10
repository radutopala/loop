package browsercookies

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, already used by internal/db
)

// openStoreCopy copies a browser's cookie store to a temporary directory and
// opens the copy.
//
// The copy is not an optimisation, it is the whole point: the browser holds
// its store open and loop must never write to — or lock — a file the user's
// own browser depends on. The copy is opened read-write so SQLite can replay
// a copied -wal, which a read-only handle refuses to do.
func openStoreCopy(store string) (*sql.DB, func(), error) {
	tmp, err := os.MkdirTemp("", "loop-cookies-")
	if err != nil {
		return nil, nil, fmt.Errorf("creating temp dir for cookie store: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	dst := filepath.Join(tmp, filepath.Base(store))
	if err := copyFile(store, dst); err != nil {
		cleanup()
		return nil, nil, err
	}
	// The write-ahead log and shared-memory sidecars hold cookies the browser
	// has not checkpointed yet. Missing ones are normal, not an error.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = copyFile(store+suffix, dst+suffix)
	}

	// sql.Open only checks that the driver name is registered, which the
	// blank import above guarantees; the file itself is opened lazily, so a
	// bad copy surfaces on the first query rather than here.
	db, _ := sql.Open("sqlite", dst)
	return db, func() { _ = db.Close(); cleanup() }, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening cookie store: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating cookie store copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copying cookie store: %w", err)
	}
	return out.Close()
}
