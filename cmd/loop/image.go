package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

func (a *app) newImageRebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "image:rebuild",
		Aliases: []string{"i:rebuild", "i:r"},
		Short:   "Rebuild the Docker agent image",
		RunE: func(_ *cobra.Command, _ []string) error {
			apiURL := a.resolveAPIURL()

			fmt.Println("Starting image rebuild...")
			resp, err := http.Post(apiURL+"/api/image/rebuild", "application/json", nil)
			if err != nil {
				return fmt.Errorf("calling rebuild API: %w", err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusConflict {
				return fmt.Errorf("a build is already in progress")
			}
			if resp.StatusCode != http.StatusAccepted {
				return fmt.Errorf("rebuild failed: %s", resp.Status)
			}

			// Poll status until build completes.
			for {
				time.Sleep(2 * time.Second)
				status, err := a.fetchImageStatus(apiURL)
				if err != nil {
					return err
				}
				switch status.Status.State {
				case "building":
					fmt.Printf("  building (%s)...\n", status.Status.Phase)
				case "completed":
					fmt.Printf("Done. loop=%s claude=%s\n", status.Versions.LoopVersion, status.Versions.ClaudeVersion)
					return nil
				case "failed":
					return fmt.Errorf("build failed: %s", status.Status.Error)
				default:
					fmt.Printf("Done. loop=%s claude=%s\n", status.Versions.LoopVersion, status.Versions.ClaudeVersion)
					return nil
				}
			}
		},
	}
}

func (a *app) newImageStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "image:status",
		Aliases: []string{"i:status", "i:s"},
		Short:   "Show Docker agent image status and versions",
		RunE: func(_ *cobra.Command, _ []string) error {
			apiURL := a.resolveAPIURL()

			status, err := a.fetchImageStatus(apiURL)
			if err != nil {
				return err
			}

			fmt.Printf("Status: %s\n", status.Status.State)
			if status.Versions.LoopVersion != "" {
				fmt.Printf("  Loop version:   %s\n", status.Versions.LoopVersion)
			}
			if status.Versions.ClaudeVersion != "" {
				fmt.Printf("  Claude version: %s\n", status.Versions.ClaudeVersion)
			}
			if status.Versions.BuiltAt != "" {
				fmt.Printf("  Built at:       %s\n", status.Versions.BuiltAt)
			}
			if status.UpdateAvailable != nil {
				fmt.Printf("  Update:         %s -> %s (%s)\n",
					status.UpdateAvailable.CurrentVersion,
					status.UpdateAvailable.LatestVersion,
					status.UpdateAvailable.Component)
			}
			return nil
		},
	}
}

type imageStatusJSON struct {
	Status struct {
		State string `json:"state"`
		Phase string `json:"phase"`
		Error string `json:"error"`
	} `json:"status"`
	Versions struct {
		LoopVersion   string `json:"loop_version"`
		ClaudeVersion string `json:"claude_version"`
		BuiltAt       string `json:"built_at"`
	} `json:"versions"`
	UpdateAvailable *struct {
		CurrentVersion string `json:"current_version"`
		LatestVersion  string `json:"latest_version"`
		Component      string `json:"component"`
	} `json:"update_available"`
}

func (a *app) resolveAPIURL() string {
	cfg, err := a.configLoad()
	if err != nil {
		return "http://localhost:8222"
	}
	addr := cfg.APIAddr
	if addr == "" {
		addr = ":8222"
	}
	if addr[0] == ':' {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

func (a *app) fetchImageStatus(apiURL string) (*imageStatusJSON, error) {
	resp, err := http.Get(apiURL + "/api/image/status")
	if err != nil {
		return nil, fmt.Errorf("calling image status API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("image status failed: %s — %s", resp.Status, string(body))
	}
	var status imageStatusJSON
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("parsing image status: %w", err)
	}
	return &status, nil
}
