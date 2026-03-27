package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/container"
)

func (a *app) newImageRebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "image:rebuild",
		Aliases: []string{"i:rebuild", "i:r"},
		Short:   "Rebuild the Docker agent image",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := a.configLoad()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			client, err := a.newDockerClient()
			if err != nil {
				return fmt.Errorf("creating docker client: %w", err)
			}
			client.SetLoopVersion(a.version)

			fmt.Println("Removing old image and containers...")
			if err := client.RemoveImageAndContainers(context.Background(), cfg.ContainerImage); err != nil {
				fmt.Printf("  warning: %v\n", err)
			}

			containerDir := filepath.Join(cfg.LoopDir, "container")

			fmt.Println("Building image...")
			if err := client.ImageBuild(context.Background(), containerDir, cfg.ContainerImage); err != nil {
				return fmt.Errorf("building image: %w", err)
			}

			// Read and save version info from labels.
			v := container.ImageVersions{LoopVersion: a.version, BuiltAt: time.Now()}
			if labels, labelErr := client.ImageInspectLabels(context.Background(), cfg.ContainerImage); labelErr == nil && labels != nil {
				if lv := labels["loop.version"]; lv != "" {
					v.LoopVersion = lv
				}
				v.ClaudeVersion = labels["loop.claude_version"]
			}

			lifecycleMgr := container.NewImageLifecycleManager(
				client, nil, a.sys, nil,
				containerDir, cfg.ContainerImage, a.version,
				client.LatestClaudeVersion,
			)
			lifecycleMgr.SaveVersions(v)

			fmt.Printf("Done. loop=%s claude=%s\n", v.LoopVersion, v.ClaudeVersion)
			return nil
		},
	}
}

func (a *app) newImageStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "image:status",
		Aliases: []string{"i:status", "i:s"},
		Short:   "Show Docker agent image status and versions",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := a.configLoad()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			client, err := a.newDockerClient()
			if err != nil {
				return fmt.Errorf("creating docker client: %w", err)
			}

			// Check if image exists.
			ids, err := client.ImageList(context.Background(), cfg.ContainerImage)
			if err != nil {
				return fmt.Errorf("listing images: %w", err)
			}

			if len(ids) == 0 {
				fmt.Printf("Image: %s (not found)\n", cfg.ContainerImage)
				return nil
			}

			fmt.Printf("Image: %s\n", cfg.ContainerImage)

			// Try to read labels from image.
			labels, err := client.ImageInspectLabels(context.Background(), cfg.ContainerImage)
			if err == nil && labels != nil {
				if v := labels["loop.version"]; v != "" {
					fmt.Printf("  Loop version:   %s\n", v)
				}
				if v := labels["loop.claude_version"]; v != "" {
					fmt.Printf("  Claude version: %s\n", v)
				}
			}

			// Also read versions file for built_at timestamp.
			home, homeErr := a.sys.UserHomeDir()
			if homeErr == nil {
				p := filepath.Join(home, ".loop", "image-versions.json")
				data, readErr := a.sys.ReadFile(p)
				if readErr == nil {
					var v container.ImageVersions
					if json.Unmarshal(data, &v) == nil && !v.BuiltAt.IsZero() {
						fmt.Printf("  Built at:       %s\n", v.BuiltAt.Format(time.RFC3339))
					}
				}
			}

			return nil
		},
	}
}
