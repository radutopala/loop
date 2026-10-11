package pluginsdk

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Env is what Loop tells a plugin container through its environment.
type Env struct {
	// PluginID is LOOP_PLUGIN_ID.
	PluginID string
	// ChannelID is LOOP_CHANNEL_ID, the channel the container serves.
	ChannelID string
	// ChannelDir is LOOP_CHANNEL_DIR, the channel's dir on the host. It isn't
	// mounted; read its files through the API.
	ChannelDir string
	// Port is LOOP_PLUGIN_PORT, the port to serve on.
	Port string
	// APIURL is LOOP_API_URL, Loop's API.
	APIURL string
	// TokenFile is LOOP_PLUGIN_TOKEN_FILE, the token for the Plugin API.
	TokenFile string
	// SecretFile is LOOP_PLUGIN_SECRET_FILE, the secret Loop sends.
	SecretFile string
	// DataDir is LOOP_DATA_DIR, /data unless set: the dir that outlives
	// the container.
	DataDir string
}

// LoadEnv reads the Env with getenv, such as os.Getenv.
func LoadEnv(getenv func(string) string) Env {
	e := Env{
		PluginID:   getenv("LOOP_PLUGIN_ID"),
		ChannelID:  getenv("LOOP_CHANNEL_ID"),
		ChannelDir: getenv("LOOP_CHANNEL_DIR"),
		Port:       getenv("LOOP_PLUGIN_PORT"),
		APIURL:     getenv("LOOP_API_URL"),
		TokenFile:  getenv("LOOP_PLUGIN_TOKEN_FILE"),
		SecretFile: getenv("LOOP_PLUGIN_SECRET_FILE"),
		DataDir:    getenv("LOOP_DATA_DIR"),
	}
	if e.DataDir == "" {
		e.DataDir = "/data"
	}
	return e
}

// Secret reads the secret Loop sends with every request.
func (e Env) Secret() (string, error) {
	b, err := os.ReadFile(e.SecretFile)
	if err != nil {
		return "", fmt.Errorf("reading the secret: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Client returns a Client for the Plugin API.
func (e Env) Client() *Client {
	return NewClient(e.APIURL, e.TokenFile)
}

// Listen listens on the port Loop gave the plugin.
func (e Env) Listen() (net.Listener, error) {
	return net.Listen("tcp", ":"+e.Port)
}

// ShutdownTimeout caps how long Serve waits for requests in flight.
const ShutdownTimeout = 10 * time.Second

// Serve serves h on ln until ctx ends, then waits for the requests in flight.
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	var shutdownErr error
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		sctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		shutdownErr = hs.Shutdown(sctx)
	})
	err := hs.Serve(ln)
	if stop() {
		// The server failed before ctx ended.
		return err
	}
	// Serve answered ErrServerClosed; Shutdown says whether the requests in
	// flight finished.
	<-done
	return shutdownErr
}
