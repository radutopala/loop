package dictation

import (
	"context"
	"errors"
	"sync"

	"github.com/radutopala/loop/internal/localserver"
)

// Config is the dictation configuration in effect.
type Config struct {
	Model    string
	Language string
	Threads  int
}

// Status describes whether dictation can run.
type Status struct {
	Available      bool     `json:"available"`
	Reason         string   `json:"reason"`
	Model          string   `json:"model"`
	ModelReady     bool     `json:"model_ready"`
	ModelSizeBytes int64    `json:"model_size_bytes"`
	Download       Progress `json:"download"`
	Running        bool     `json:"running"`
}

// Error is a failure the client is told about by its code.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// LocalServer is the whisper-server process.
type LocalServer interface {
	FindBinary() (string, error)
	Acquire(ctx context.Context) (string, error)
	Release()
	Running() bool
	Stop()
}

// Service runs dictation sessions.
type Service struct {
	config     func() Config
	server     LocalServer
	downloader *Downloader
	engine     *Engine
	ctx        context.Context

	mu sync.Mutex
	// model is the model file the server runs, or is about to start with.
	model string
}

// NewService returns a service. config is read on every call, so a changed
// model applies to the next session; ctx bounds model downloads.
func NewService(ctx context.Context, config func() Config, server LocalServer, downloader *Downloader, engine *Engine) *Service {
	return &Service{ctx: ctx, config: config, server: server, downloader: downloader, engine: engine}
}

// ServerArgs returns the whisper-server arguments for port, for the model
// a session is starting. It's localserver.Config.Args for the service's
// server; it runs within Begin, which holds mu.
func (s *Service) ServerArgs(port int) []string {
	return ServerArgs(s.model, s.config().Threads, port)
}

// current returns the configuration and its model, defaulting an unknown name to the
// first one.
func (s *Service) current() (Config, Model) {
	cfg := s.config()
	m, ok := LookupModel(cfg.Model)
	if !ok {
		m = Models()[0]
	}
	return cfg, m
}

// Status reports whether dictation can run.
func (s *Service) Status() Status {
	_, m := s.current()
	st := Status{
		Available:      true,
		Model:          m.Name,
		ModelReady:     s.downloader.Ready(m),
		ModelSizeBytes: m.Size,
		Download:       s.downloader.Progress(),
		Running:        s.server.Running(),
	}
	if _, err := s.server.FindBinary(); err != nil {
		st.Available, st.Reason = false, CodeBinaryMissing
	}
	return st
}

// DownloadModel starts downloading the configured model. done, when not nil,
// is closed once a started download ends.
func (s *Service) DownloadModel(done chan<- struct{}) bool {
	_, m := s.current()
	return s.downloader.Start(s.ctx, m, done)
}

// errModelMissing reports a model that isn't downloaded yet.
var errModelMissing = errors.New("model not downloaded")

// Begin starts whisper-server if needed, with the configured model, and
// returns a session in the configured language unless language is given.
// The caller runs the session and then calls the returned release.
func (s *Service) Begin(ctx context.Context, language string, emit func(Event)) (*Session, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, m := s.current()
	if !s.downloader.Ready(m) {
		return nil, nil, &Error{Code: CodeModelMissing, Err: errModelMissing}
	}
	if path := s.downloader.Path(m); path != s.model {
		s.server.Stop()
		s.model = path
	}
	base, err := s.server.Acquire(ctx)
	if errors.Is(err, localserver.ErrBinaryMissing) {
		return nil, nil, &Error{Code: CodeBinaryMissing, Err: err}
	}
	if err != nil {
		return nil, nil, &Error{Code: CodeEngineFailed, Err: err}
	}
	if language == "" {
		language = cfg.Language
	}
	transcribe := func(ctx context.Context, samples []int16, language, prompt string) (string, error) {
		return s.engine.Transcribe(ctx, base, samples, language, prompt)
	}
	return NewSession(transcribe, language, emit), s.server.Release, nil
}
