// memory.go holds SQLiteStore methods for the memory_files table.
package db

import (
	"context"
	"database/sql"
)

func (s *SQLiteStore) UpsertMemoryFile(ctx context.Context, file *MemoryFile) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_files (file_path, chunk_index, content, content_hash, embedding, dimensions, dir_path, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(file_path, chunk_index, dir_path) DO UPDATE SET
		   content = excluded.content,
		   content_hash = excluded.content_hash,
		   embedding = excluded.embedding,
		   dimensions = excluded.dimensions,
		   updated_at = excluded.updated_at`,
		file.FilePath, file.ChunkIndex, file.Content, file.ContentHash, file.Embedding, file.Dimensions, file.DirPath, s.nowFunc(),
	)
	return err
}

func (s *SQLiteStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*MemoryFile, error) { //nolint:dupl
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, file_path, chunk_index, content, content_hash, embedding, dimensions, dir_path, updated_at
		 FROM memory_files WHERE (dir_path = ? OR dir_path = '') AND dimensions > 0`,
		dirPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*MemoryFile
	for rows.Next() {
		f := &MemoryFile{}
		if err := rows.Scan(&f.ID, &f.FilePath, &f.ChunkIndex, &f.Content, &f.ContentHash, &f.Embedding, &f.Dimensions, &f.DirPath, &f.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *SQLiteStore) GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM memory_files WHERE file_path = ? AND dir_path = ? AND chunk_index = 0`,
		filePath, dirPath,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func (s *SQLiteStore) DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory_files WHERE file_path = ? AND dir_path = ?`, filePath, dirPath)
	return err
}

func (s *SQLiteStore) ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]MemoryFileInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT file_path, dir_path FROM memory_files WHERE (dir_path = ? OR dir_path = '') ORDER BY file_path ASC`,
		dirPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []MemoryFileInfo
	for rows.Next() {
		var f MemoryFileInfo
		if err := rows.Scan(&f.FilePath, &f.DirPath); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
