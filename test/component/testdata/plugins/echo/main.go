// Command echo is Loop's fixture plugin. It serves the Loop → plugin
// contract on $LOOP_PLUGIN_PORT: a health check, an echo tool and a ping
// action, each behind the secret Loop hands it.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	secret, err := os.ReadFile(os.Getenv("LOOP_PLUGIN_SECRET_FILE"))
	if err != nil {
		log.Fatalf("reading the secret: %v", err)
	}
	want := "Bearer " + strings.TrimSpace(string(secret))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, map[string]string{"api": "1", "version": "0.1.0"})
	})
	mux.HandleFunc("POST /tools/echo", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, http.StatusOK, map[string]any{"content": "bad input: " + err.Error(), "is_error": true})
			return
		}
		reply(w, http.StatusOK, map[string]any{"content": req.Input.Text, "is_error": false})
	})
	mux.HandleFunc("POST /actions/ping", func(w http.ResponseWriter, _ *http.Request) {
		log.Print("ping")
		w.WriteHeader(http.StatusAccepted)
	})

	addr := ":" + os.Getenv("LOOP_PLUGIN_PORT")
	log.Printf("echo plugin for channel %s listening on %s", os.Getenv("LOOP_CHANNEL_ID"), addr)
	log.Fatal(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})))
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
