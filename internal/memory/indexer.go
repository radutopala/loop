package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
)

// defaultMaxChunkChars is the default maximum content length sent to the embedding model.
// nomic-embed-text has an 8192-token context; ~5000 chars is a safe limit for dense content.
const defaultMaxChunkChars = 5000

// Store is the memory file storage interface (subset of db.Store).
type Store interface {
	UpsertMemoryFile(ctx context.Context, file *db.MemoryFile) error
	GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*db.MemoryFile, error)
	GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error)
	DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error
}

// SearchResult holds a single search result with its similarity score.
type SearchResult struct {
	FilePath   string  `json:"file_path"`
	Content    string  `json:"content,omitempty"`
	Score      float32 `json:"score"`
	ChunkIndex int     `json:"chunk_index"`
}

// Indexer handles indexing and searching memory files.
type Indexer struct {
	embedder      embeddings.Embedder
	store         Store
	logger        *slog.Logger
	maxChunkChars int
}

// NewIndexer creates a new memory indexer.
// maxChunkChars controls the maximum chunk size for embeddings; 0 uses the default (6000).
func NewIndexer(embedder embeddings.Embedder, store Store, logger *slog.Logger, maxChunkChars int) *Indexer {
	if maxChunkChars <= 0 {
		maxChunkChars = defaultMaxChunkChars
	}
	return &Indexer{
		embedder:      embedder,
		store:         store,
		logger:        logger,
		maxChunkChars: maxChunkChars,
	}
}

// readFile is a package-level variable for testing.
var readFile = os.ReadFile

// walkDir is a package-level variable for testing.
var walkDir = filepath.WalkDir

// osStat is a package-level variable for testing.
var osStat = os.Stat

// evalSymlinks is a package-level variable for testing.
var evalSymlinks = filepath.EvalSymlinks

// Index scans the memory path (directory tree or single .md file), embeds whole files,
// and stores any stale or new entries. Returns the number of files indexed.
// dirPath scopes the indexed files; empty string means global scope.
// excludePaths are absolute paths to skip during indexing (prefix match with separator check).
func (idx *Indexer) Index(ctx context.Context, memoryPath, dirPath string, excludePaths []string) (int, error) {
	info, err := osStat(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat memory path: %w", err)
	}

	// Resolve symlinks so filepath.WalkDir can descend into symlinked directories.
	resolved, err := evalSymlinks(memoryPath)
	if err != nil {
		return 0, fmt.Errorf("resolving symlinks: %w", err)
	}
	memoryPath = resolved

	// Resolve symlinks on exclude paths so they match the resolved walk paths.
	excludePaths = resolveExcludeSymlinks(excludePaths)

	if !info.IsDir() {
		if isExcluded(memoryPath, excludePaths) {
			return 0, nil
		}
		if strings.HasSuffix(memoryPath, ".md") {
			return idx.indexFile(ctx, memoryPath, dirPath)
		}
		return 0, nil
	}

	indexed := 0
	err = walkDir(memoryPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			idx.logger.Warn("walking memory dir", "path", path, "error", err)
			return nil
		}
		if isExcluded(path, excludePaths) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		n, indexErr := idx.indexFile(ctx, path, dirPath)
		if indexErr != nil {
			idx.logger.Error("indexing memory file", "file", path, "error", indexErr)
			return nil
		}
		indexed += n
		return nil
	})
	if err != nil {
		return indexed, fmt.Errorf("walking memory dir: %w", err)
	}
	return indexed, nil
}

// isExcluded checks whether path should be excluded based on excludePaths.
// Uses separator-safe prefix matching to avoid false positives
// (e.g., "/memory/drafts" won't exclude "/memory/drafts-v2").
func isExcluded(path string, excludePaths []string) bool {
	for _, ex := range excludePaths {
		if path == ex || strings.HasPrefix(path, ex+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveExcludeSymlinks resolves symlinks in exclude paths so they match
// the symlink-resolved paths from walkDir. Non-existent paths are kept as-is.
func resolveExcludeSymlinks(paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	resolved := make([]string, len(paths))
	for i, p := range paths {
		if r, err := evalSymlinks(p); err == nil {
			resolved[i] = r
		} else {
			resolved[i] = p
		}
	}
	return resolved
}

// indexFile indexes a single .md file. Small files get a single row;
// large files are split into fixed-size chunks with a header row for the hash.
func (idx *Indexer) indexFile(ctx context.Context, filePath, dirPath string) (int, error) {
	data, err := readFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading memory file: %w", err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return 0, nil
	}

	hash := contentHash(content)

	existingHash, err := idx.store.GetMemoryFileHash(ctx, filePath, dirPath)
	if err != nil {
		return 0, fmt.Errorf("checking file hash: %w", err)
	}
	if existingHash == hash {
		return 0, nil
	}

	// Stale or new — delete all rows (header + chunks) and re-index.
	if existingHash != "" {
		if err := idx.store.DeleteMemoryFile(ctx, filePath, dirPath); err != nil {
			return 0, fmt.Errorf("deleting stale file: %w", err)
		}
	}

	// Small file — single row with both hash and embedding.
	if len(content) <= idx.maxChunkChars {
		vecs, err := idx.embedder.Embed(ctx, []string{content})
		if err != nil {
			return 0, fmt.Errorf("embedding file: %w", err)
		}
		mf := &db.MemoryFile{
			FilePath:    filePath,
			ChunkIndex:  0,
			Content:     content,
			ContentHash: hash,
			Embedding:   embeddings.SerializeFloat32(vecs[0]),
			Dimensions:  idx.embedder.Dimensions(),
			DirPath:     dirPath,
		}
		if err := idx.store.UpsertMemoryFile(ctx, mf); err != nil {
			return 0, fmt.Errorf("upserting file: %w", err)
		}
		return 1, nil
	}

	// Large file — header row (hash, no embedding) + chunk rows.
	header := &db.MemoryFile{
		FilePath:    filePath,
		ChunkIndex:  0,
		ContentHash: hash,
		DirPath:     dirPath,
	}
	if err := idx.store.UpsertMemoryFile(ctx, header); err != nil {
		return 0, fmt.Errorf("upserting header: %w", err)
	}

	chunks := splitChunks(content, idx.maxChunkChars)
	for i, chunk := range chunks {
		vecs, err := idx.embedder.Embed(ctx, []string{chunk})
		if err != nil {
			return 0, fmt.Errorf("embedding chunk %d: %w", i+1, err)
		}
		mf := &db.MemoryFile{
			FilePath:   filePath,
			ChunkIndex: i + 1,
			Content:    chunk,
			Embedding:  embeddings.SerializeFloat32(vecs[0]),
			Dimensions: idx.embedder.Dimensions(),
			DirPath:    dirPath,
		}
		if err := idx.store.UpsertMemoryFile(ctx, mf); err != nil {
			return 0, fmt.Errorf("upserting chunk %d: %w", i+1, err)
		}
	}
	idx.logger.Info("indexed large file", "file", filePath, "chunks", len(chunks), "chars", len(content))
	return 1, nil
}

// splitChunks splits content into chunks of at most maxChars, breaking at line boundaries.
func splitChunks(content string, maxChars int) []string {
	var chunks []string
	for len(content) > 0 {
		if len(content) <= maxChars {
			chunks = append(chunks, content)
			break
		}
		// Find last newline within the limit.
		cut := strings.LastIndex(content[:maxChars], "\n")
		if cut <= 0 {
			cut = maxChars // no newline found, hard cut
		}
		chunks = append(chunks, content[:cut])
		content = content[cut:]
		content = strings.TrimLeft(content, "\n")
	}
	return chunks
}

// Search embeds the query and returns the top-K results ranked by cosine similarity.
// Results are scoped to the given dirPath (project-scoped) plus global files (dir_path = ”).
func (idx *Indexer) Search(ctx context.Context, dirPath, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 5
	}

	// Embed query.
	queryVecs, err := idx.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	queryVec := queryVecs[0]

	// Load files scoped to dirPath (plus global).
	dbFiles, err := idx.store.GetMemoryFilesByDirPath(ctx, dirPath)
	if err != nil {
		return nil, fmt.Errorf("loading files: %w", err)
	}

	// Score each file.
	var results []SearchResult
	for _, f := range dbFiles {
		vec := embeddings.DeserializeFloat32(f.Embedding)
		if len(vec) == 0 {
			continue
		}
		score := embeddings.CosineSimilarity(queryVec, vec)
		results = append(results, SearchResult{
			FilePath:   f.FilePath,
			Content:    f.Content,
			Score:      score,
			ChunkIndex: f.ChunkIndex,
		})
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
