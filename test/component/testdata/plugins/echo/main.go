// Command echo is Loop's fixture plugin. It serves the Loop → plugin
// contract on $LOOP_PLUGIN_PORT: a health check, an echo tool, a ping action
// and a say action that asks Loop for an agent run calling the echo tool,
// each behind the secret Loop hands it.
package main

import (
	"bytes"
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
		log.Printf("echo: %s", req.Input.Text)
		reply(w, http.StatusOK, map[string]any{"content": req.Input.Text, "is_error": false})
	})
	mux.HandleFunc("POST /actions/ping", func(w http.ResponseWriter, _ *http.Request) {
		log.Print("ping")
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /actions/say", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		go startRun()
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

// startRun asks Loop for a run of the say agent, logging what Loop answers.
func startRun() {
	token, err := os.ReadFile(os.Getenv("LOOP_PLUGIN_TOKEN_FILE"))
	if err != nil {
		log.Printf("reading the token: %v", err)
		return
	}
	body := []byte(`{"agent":"say","prompt":"Call the plugin_echo tool with the text hi, then reply with what it returned."}`)
	req, err := http.NewRequest(http.MethodPost, os.Getenv("LOOP_API_URL")+"/api/plugin/v1/runs", bytes.NewReader(body))
	if err != nil {
		log.Printf("building the run request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("starting a run: %v", err)
		return
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	log.Printf("run: %s %s", resp.Status, strings.TrimSpace(out.String()))
}
