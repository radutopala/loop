package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultOllamaURL        = "http://localhost:11434"
	defaultOllamaModel      = "nomic-embed-text"
	defaultOllamaDims       = 768
	ollamaContainerName     = "loop-ollama"
	ollamaVolumeName        = "loop-ollama"
	ollamaImage             = "ollama/ollama:latest"
	ollamaStartupWait       = 30 * time.Second
	ollamaStartupPollDelay  = 500 * time.Millisecond
	ollamaIdleTimeout       = 5 * time.Minute
	ollamaMarkerFile        = "ollama-last-used"
	ollamaIdleCheckInterval = 1 * time.Minute
)

// HTTPClient abstracts HTTP calls for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Commander abstracts shell command execution for testability.
type Commander interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// OllamaEmbedder generates embeddings using Ollama running in a Docker container.
type OllamaEmbedder struct {
	model             string
	url               string
	dims              int
	httpClient        HTTPClient
	commander         Commander
	logger            *slog.Logger
	startupWait       time.Duration
	loopDir           string
	idleCheckInterval time.Duration
}

// OllamaOption configures an OllamaEmbedder.
type OllamaOption func(*OllamaEmbedder)

// WithOllamaURL sets the Ollama API URL.
func WithOllamaURL(url string) OllamaOption {
	return func(e *OllamaEmbedder) { e.url = url }
}

// WithOllamaModel sets the embedding model name.
func WithOllamaModel(model string) OllamaOption {
	return func(e *OllamaEmbedder) { e.model = model }
}

// WithOllamaHTTPClient sets the HTTP client.
func WithOllamaHTTPClient(c HTTPClient) OllamaOption {
	return func(e *OllamaEmbedder) { e.httpClient = c }
}

// WithOllamaCommander sets the command executor.
func WithOllamaCommander(c Commander) OllamaOption {
	return func(e *OllamaEmbedder) { e.commander = c }
}

// WithOllamaLogger sets the logger.
func WithOllamaLogger(l *slog.Logger) OllamaOption {
	return func(e *OllamaEmbedder) { e.logger = l }
}

// WithOllamaStartupWait overrides the container startup wait duration (for testing).
func WithOllamaStartupWait(d time.Duration) OllamaOption {
	return func(e *OllamaEmbedder) { e.startupWait = d }
}

// WithOllamaLoopDir sets the loop directory for the idle-timeout marker file.
func WithOllamaLoopDir(dir string) OllamaOption {
	return func(e *OllamaEmbedder) { e.loopDir = dir }
}

// WithOllamaIdleCheckInterval overrides the idle check interval (for testing).
func WithOllamaIdleCheckInterval(d time.Duration) OllamaOption {
	return func(e *OllamaEmbedder) { e.idleCheckInterval = d }
}

// NewOllamaEmbedder creates an Ollama embedder that manages a Docker container.
func NewOllamaEmbedder(opts ...OllamaOption) *OllamaEmbedder {
	e := &OllamaEmbedder{
		model:             defaultOllamaModel,
		url:               defaultOllamaURL,
		dims:              defaultOllamaDims,
		httpClient:        http.DefaultClient,
		commander:         &ExecCommander{},
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		startupWait:       ollamaStartupWait,
		idleCheckInterval: ollamaIdleCheckInterval,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *OllamaEmbedder) Dimensions() int { return e.dims }

// Embed starts the Ollama container if needed, ensures the model is pulled,
// and returns embeddings for the given texts.
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := e.ensureRunning(ctx); err != nil {
		return nil, fmt.Errorf("ollama ensure running: %w", err)
	}
	if err := e.ensureModel(ctx); err != nil {
		return nil, fmt.Errorf("ollama ensure model: %w", err)
	}

	body, _ := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: texts})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama decode response: %w", err)
	}

	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}

	e.touchMarkerFile()
	return result.Embeddings, nil
}

// touchMarkerFile updates the idle-timeout marker file so the daemon knows
// the embedder was recently used.
func (e *OllamaEmbedder) touchMarkerFile() {
	if e.loopDir == "" {
		return
	}
	path := filepath.Join(e.loopDir, ollamaMarkerFile)
	_ = os.WriteFile(path, nil, 0o644)
}

// RunIdleMonitor periodically checks the marker file and stops the Ollama
// container when it has been idle for longer than ollamaIdleTimeout.
// Blocks until ctx is cancelled. Run this as a goroutine in the daemon.
func (e *OllamaEmbedder) RunIdleMonitor(ctx context.Context) {
	if e.loopDir == "" {
		return
	}
	ticker := time.NewTicker(e.idleCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.checkIdle()
		}
	}
}

// checkIdle stops the Ollama container if the marker file indicates no recent usage.
func (e *OllamaEmbedder) checkIdle() {
	markerPath := filepath.Join(e.loopDir, ollamaMarkerFile)
	info, err := os.Stat(markerPath)
	if err != nil {
		return // no marker file, nothing to do
	}
	if time.Since(info.ModTime()) < ollamaIdleTimeout {
		return // recently used
	}
	// Check if container is actually running before trying to remove.
	out, err := e.commander.Run(context.Background(), "docker", "inspect", "--format", "{{.State.Running}}", ollamaContainerName)
	if err != nil || string(bytes.TrimSpace(out)) != "true" {
		return // not running
	}
	e.logger.Info("stopping idle ollama container")
	_, _ = e.commander.Run(context.Background(), "docker", "rm", "-f", ollamaContainerName)
}

// ensureRunning starts the Ollama Docker container if it is not already running.
func (e *OllamaEmbedder) ensureRunning(ctx context.Context) error {
	// Check if container exists and is running.
	out, err := e.commander.Run(ctx, "docker", "inspect", "--format", "{{.State.Running}}", ollamaContainerName)
	if err == nil && string(bytes.TrimSpace(out)) == "true" {
		return nil
	}

	// Remove stale container if it exists.
	_, _ = e.commander.Run(ctx, "docker", "rm", "-f", ollamaContainerName)

	// Start new container with named volume for model persistence.
	e.logger.Info("starting ollama container")
	if _, err := e.commander.Run(ctx, "docker", "run", "-d",
		"--name", ollamaContainerName,
		"-v", ollamaVolumeName+":/root/.ollama",
		"-p", "11434:11434",
		ollamaImage,
	); err != nil {
		return fmt.Errorf("docker run ollama: %w", err)
	}

	// Wait for Ollama to become responsive.
	return e.waitForReady(ctx)
}

// waitForReady polls the Ollama API until it responds or the timeout is reached.
func (e *OllamaEmbedder) waitForReady(ctx context.Context) error {
	deadline := time.Now().Add(e.startupWait)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/", nil)
		if err != nil {
			return err
		}
		resp, err := e.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		sleepFunc(ollamaStartupPollDelay)
	}
	return fmt.Errorf("ollama did not become ready within %s", ollamaStartupWait)
}

// sleepFunc is injectable for testing.
var sleepFunc = time.Sleep

// ensureModel pulls the configured model if not already present.
func (e *OllamaEmbedder) ensureModel(ctx context.Context) error {
	// Check if model already exists to avoid docker exec overhead on every call.
	out, err := e.commander.Run(ctx, "docker", "exec", ollamaContainerName, "ollama", "list")
	if err == nil && strings.Contains(string(out), e.model) {
		return nil
	}
	e.logger.Info("pulling ollama model", "model", e.model)
	if _, err := e.commander.Run(ctx, "docker", "exec", ollamaContainerName, "ollama", "pull", e.model); err != nil {
		return fmt.Errorf("ollama pull %s: %w", e.model, err)
	}
	return nil
}

// Stop removes the Ollama container. Call during daemon shutdown.
func (e *OllamaEmbedder) Stop() {
	e.logger.Info("removing ollama container")
	_, _ = e.commander.Run(context.Background(), "docker", "rm", "-f", ollamaContainerName)
}

// ExecCommander implements Commander using os/exec.
type ExecCommander struct{}

// Run executes a command and returns its combined stdout/stderr output.
func (c *ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
