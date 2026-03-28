package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
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

			// Read version info from labels.
			loopV, claudeV := a.version, "unknown"
			if labels, labelErr := client.ImageInspectLabels(context.Background(), cfg.ContainerImage); labelErr == nil && labels != nil {
				if lv := labels["loop.version"]; lv != "" {
					loopV = lv
				}
				if cv := labels["loop.claude_version"]; cv != "" {
					claudeV = cv
				}
			}

			fmt.Printf("Done. loop=%s claude=%s\n", loopV, claudeV)
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

			return nil
		},
	}
}
