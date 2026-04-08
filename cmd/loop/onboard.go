package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/config"
	containerimage "github.com/radutopala/loop/internal/container/image"
)

func (a *app) newOnboardGlobalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "onboard:global",
		Aliases: []string{"o:global", "setup"},
		Short:   "Initialize global Loop configuration at ~/.loop/",
		Long:    "Copies config.example.json to ~/.loop/config.json for first-time setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			ownerID, _ := cmd.Flags().GetString("owner-id")
			return a.onboardGlobal(force, ownerID)
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite existing config")
	cmd.Flags().String("owner-id", "", "Set RBAC owner user ID (exits bootstrap mode)")
	return cmd
}

func (a *app) newOnboardLocalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "onboard:local",
		Aliases: []string{"o:local", "init"},
		Short:   "Register Loop MCP server in the current project",
		Long:    "Writes .mcp.json with the loop MCP server for Claude Code integration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			apiURL, _ := cmd.Flags().GetString("api-url")
			ownerID, _ := cmd.Flags().GetString("owner-id")
			platform, _ := cmd.Flags().GetString("platform")
			return a.onboardLocal(apiURL, ownerID, platform)
		},
	}
	cmd.Flags().String("api-url", "http://localhost:8222", "Loop API base URL")
	cmd.Flags().String("owner-id", "", "Set RBAC owner user ID in project config")
	cmd.Flags().String("platform", "", "Only register channel for this platform (e.g. local)")
	return cmd
}

func (a *app) onboardGlobal(force bool, ownerID string) error {
	home, err := a.sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	loopDir := filepath.Join(home, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	// Check if config already exists
	if _, err := a.sys.Stat(configPath); err == nil {
		if !force {
			return fmt.Errorf("config already exists at %s (use --force to overwrite)", configPath)
		}
	}

	// Create ~/.loop directory if it doesn't exist
	if err := a.sys.MkdirAll(loopDir, 0755); err != nil {
		return fmt.Errorf("creating loop directory: %w", err)
	}

	// Prepare config content — optionally inject owner permissions
	configData := config.ExampleConfig
	if ownerID != "" {
		commented := []byte(`  // RBAC permissions: owners can do everything (including allow/deny); members can trigger and manage tasks.
  // If all config and DB permissions are empty, everyone is treated as owner (bootstrap mode).
  //"permissions": {
  //  "owners":  { "users": ["U12345678"], "roles": ["1234567890123456789"] },
  //  "members": { "users": [], "roles": [] }
  //},`)
		uncommented := []byte(fmt.Sprintf(`  "permissions": {
    "owners":  { "users": ["%s"], "roles": [] },
    "members": { "users": [], "roles": [] }
  },`, ownerID))
		configData = bytes.Replace(configData, commented, uncommented, 1)
	}

	// Write embedded example config
	if err := a.sys.WriteFile(configPath, configData, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	// Create default .bashrc for container shell aliases
	bashrcPath := filepath.Join(loopDir, ".bashrc")
	if _, err := a.sys.Stat(bashrcPath); err != nil {
		bashrcContent := []byte("# Shell aliases and config sourced inside Loop containers.\n# Add your aliases here — this file is bind-mounted as ~/.bashrc.\n")
		if err := a.sys.WriteFile(bashrcPath, bashrcContent, 0644); err != nil {
			return fmt.Errorf("writing .bashrc: %w", err)
		}
	}

	// Flush embedded container files
	containerDir := filepath.Join(loopDir, "container")
	if err := a.sys.MkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("creating container directory: %w", err)
	}
	if err := a.sys.WriteFile(filepath.Join(containerDir, "Dockerfile"), containerimage.Dockerfile, 0644); err != nil {
		return fmt.Errorf("writing container Dockerfile: %w", err)
	}
	if err := a.sys.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), containerimage.ChromeDockerfile, 0644); err != nil {
		return fmt.Errorf("writing chrome Dockerfile: %w", err)
	}
	if err := a.sys.WriteFile(filepath.Join(containerDir, "chrome-entrypoint.sh"), containerimage.ChromeEntrypoint, 0644); err != nil {
		return fmt.Errorf("writing chrome entrypoint: %w", err)
	}
	if err := a.sys.WriteFile(filepath.Join(containerDir, "entrypoint.sh"), containerimage.Entrypoint, 0644); err != nil {
		return fmt.Errorf("writing container entrypoint: %w", err)
	}
	setupPath := filepath.Join(containerDir, "setup.sh")
	if _, err := a.sys.Stat(setupPath); err != nil {
		if err := a.sys.WriteFile(setupPath, containerimage.Setup, 0644); err != nil {
			return fmt.Errorf("writing container setup script: %w", err)
		}
	}

	// Write Slack app manifest
	if err := a.sys.WriteFile(filepath.Join(loopDir, "slack-manifest.json"), config.SlackManifest, 0644); err != nil {
		return fmt.Errorf("writing Slack manifest: %w", err)
	}

	// Dump embedded templates directory
	templatesDir := filepath.Join(loopDir, "templates")
	if err := a.sys.MkdirAll(templatesDir, 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}
	if err := a.dumpTemplates(templatesDir); err != nil {
		return err
	}

	// Dump embedded shortcuts directory
	shortcutsDir := filepath.Join(loopDir, "shortcuts")
	if err := a.sys.MkdirAll(shortcutsDir, 0755); err != nil {
		return fmt.Errorf("creating shortcuts directory: %w", err)
	}
	if err := a.dumpShortcuts(shortcutsDir); err != nil {
		return err
	}

	// Dump embedded playground examples
	playgroundDir := filepath.Join(loopDir, "playground")
	if err := a.dumpPlaygroundExamples(playgroundDir); err != nil {
		return err
	}

	fmt.Printf("✓ Created config at %s\n", configPath)
	fmt.Println("\nNext steps:")
	fmt.Println("1. Edit config.json and add your platform credentials (Discord or Slack)")
	fmt.Println("   For Slack: create an app from ~/.loop/slack-manifest.json (see README)")
	fmt.Println("2. Run 'loop serve' to start the bot")
	fmt.Println("3. Customize the Dockerfile at ~/.loop/container/ if needed")

	return nil
}

// dumpTemplates writes all embedded template files to the target directory,
// skipping files that already exist (so user edits are preserved).
func (a *app) dumpTemplates(dir string) error {
	return a.dumpEmbeddedFiles(a.templatesFS, "templates", dir, "template")
}

// dumpShortcuts writes all embedded shortcut files to the target directory,
// skipping files that already exist (so user edits are preserved).
func (a *app) dumpShortcuts(dir string) error {
	return a.dumpEmbeddedFiles(a.shortcutsFS, "shortcuts", dir, "shortcut")
}

// dumpEmbeddedFiles copies files from an embedded FS subdirectory to a target
// directory on disk, skipping files that already exist.
func (a *app) dumpEmbeddedFiles(fsys fs.ReadFileFS, subdir, dir, label string) error {
	entries, err := fs.ReadDir(fsys, subdir)
	if err != nil {
		return fmt.Errorf("reading embedded %ss: %w", label, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := a.sys.Stat(dst); err == nil {
			continue // don't overwrite existing
		}
		data, err := fsys.ReadFile(subdir + "/" + e.Name())
		if err != nil {
			return fmt.Errorf("reading embedded %s %s: %w", label, e.Name(), err)
		}
		if err := a.sys.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("writing %s %s: %w", label, e.Name(), err)
		}
	}
	return nil
}

// dumpPlaygroundExamples writes embedded example playgrounds to ~/.loop/playground/,
// skipping directories that already exist (so user edits are preserved).
func (a *app) dumpPlaygroundExamples(dir string) error {
	if err := a.sys.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating playground directory: %w", err)
	}
	examplesFS := a.playgroundExamplesFS
	examples, err := fs.ReadDir(examplesFS, "examples")
	if err != nil {
		return fmt.Errorf("reading embedded playground examples: %w", err)
	}
	for _, example := range examples {
		if !example.IsDir() {
			continue
		}
		exampleDir := filepath.Join(dir, example.Name())
		if _, err := a.sys.Stat(exampleDir); err == nil {
			continue // don't overwrite existing
		}
		if err := a.sys.MkdirAll(exampleDir, 0755); err != nil {
			return fmt.Errorf("creating playground example %s: %w", example.Name(), err)
		}
		files, err := fs.ReadDir(examplesFS, "examples/"+example.Name())
		if err != nil {
			return fmt.Errorf("reading playground example %s: %w", example.Name(), err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			data, err := fs.ReadFile(examplesFS, "examples/"+example.Name()+"/"+f.Name())
			if err != nil {
				return fmt.Errorf("reading playground file %s/%s: %w", example.Name(), f.Name(), err)
			}
			if err := a.sys.WriteFile(filepath.Join(exampleDir, f.Name()), data, 0644); err != nil {
				return fmt.Errorf("writing playground file %s/%s: %w", example.Name(), f.Name(), err)
			}
		}
	}
	return nil
}

func (a *app) onboardLocal(apiURL, ownerID, platform string) error {
	dir, err := a.sys.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	mcpPath := filepath.Join(dir, ".mcp.json")

	// Read existing .mcp.json if it exists, merge into it
	existing := make(map[string]any)
	if data, err := a.sys.ReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing .mcp.json: %w", err)
		}
	}

	// Ensure mcpServers key exists
	servers, _ := existing["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}

	// Build loop server entry (always rebuild to pick up config changes).
	_, alreadyRegistered := servers["loop"]
	args := []string{"mcp", "--dir", dir, "--api-url", apiURL, "--platform", "local", "--log", filepath.Join(dir, ".loop", "mcp.log")}
	if cfg, err := a.configLoad(); err == nil && cfg.Memory.Enabled {
		args = append(args, "--memory")
	}
	servers["loop"] = map[string]any{
		"command": "loop",
		"args":    args,
	}
	existing["mcpServers"] = servers

	mcpJSON, _ := json.MarshalIndent(existing, "", "  ")
	if err := a.sys.WriteFile(mcpPath, append(mcpJSON, '\n'), 0644); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	if alreadyRegistered {
		fmt.Printf("Updated loop MCP server in %s\n", mcpPath)
	} else {
		fmt.Printf("Added loop MCP server to %s\n", mcpPath)
	}
	fmt.Println("\nMake sure 'loop serve' or 'loop daemon:start' is running.")

	// Write project config example if .loop/config.json doesn't exist
	loopDir := filepath.Join(dir, ".loop")
	projectConfigPath := filepath.Join(loopDir, "config.json")
	if _, err := a.sys.Stat(projectConfigPath); os.IsNotExist(err) {
		if err := a.sys.MkdirAll(loopDir, 0755); err != nil {
			return fmt.Errorf("creating .loop directory: %w", err)
		}
		projectData := config.ProjectExampleConfig
		if ownerID != "" {
			commented := []byte(`  // Permissions override for this project (replaces global permissions when set)
  //"permissions": {
  //  "owners":  { "users": [], "roles": [] },
  //  "members": { "users": [], "roles": [] }
  //},`)
			uncommented := []byte(fmt.Sprintf(`  "permissions": {
    "owners":  { "users": ["%s"], "roles": [] },
    "members": { "users": [], "roles": [] }
  },`, ownerID))
			projectData = bytes.Replace(projectData, commented, uncommented, 1)
		}
		if err := a.sys.WriteFile(projectConfigPath, projectData, 0644); err != nil {
			return fmt.Errorf("writing project config: %w", err)
		}
		fmt.Printf("Created project config at %s\n", projectConfigPath)
	}

	// Create templates directory for project-level prompt_path templates
	templatesDir := filepath.Join(loopDir, "templates")
	if err := a.sys.MkdirAll(templatesDir, 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}

	// Create shortcuts directory for project-level prompt_path shortcuts
	shortcutsDir := filepath.Join(loopDir, "shortcuts")
	if err := a.sys.MkdirAll(shortcutsDir, 0755); err != nil {
		return fmt.Errorf("creating shortcuts directory: %w", err)
	}

	// Register channels — single platform or all configured platforms
	if platform != "" {
		channelID, err := a.ensureChannelFn(apiURL, dir, platform)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not register channel (is 'loop serve' running?): %v\n", err)
		} else {
			fmt.Printf("Channel ready (%s): %s\n", platform, channelID)
		}
	} else {
		results, err := a.ensureAllChannelsFn(apiURL, dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not register channels (is 'loop serve' running?): %v\n", err)
		} else {
			for _, r := range results {
				if r.Created {
					fmt.Printf("Channel created (%s): %s\n", r.Platform, r.ChannelID)
				} else {
					fmt.Printf("Channel exists (%s): %s\n", r.Platform, r.ChannelID)
				}
			}
		}
	}

	return nil
}
