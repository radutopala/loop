// backup.go holds the SQLiteStore method that snapshots the database.
package db

import "context"

// BackupInto writes a consistent copy of the database to path, which must
// not exist. It runs on a reader connection, so writes go on meanwhile.
func (s *SQLiteStore) BackupInto(ctx context.Context, path string) error {
	_, err := s.reader.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
