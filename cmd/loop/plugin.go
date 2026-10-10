package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/distribution/reference"
	"github.com/spf13/cobra"
)

func (a *app) newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install, upgrade and remove plugins",
		Long: "Install, upgrade and remove plugins. The daemon must run with\n" +
			"plugins.enabled set in the global config.",
	}
	cmd.AddCommand(a.newPluginListCmd(), a.newPluginInstallCmd(), a.newPluginUpgradeCmd(), a.newPluginRemoveCmd(), a.newPluginDevCmd())
	return cmd
}

func (a *app) newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := a.listPlugins(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(list) == 0 {
				_, _ = fmt.Fprintln(out, "No plugins installed")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tVERSION\tSTATUS\tIMAGE")
			for _, p := range list {
				image := p.Image
				if p.Dev {
					image += " (dev)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Version, p.Status, image)
			}
			return w.Flush()
		},
	}
}

func (a *app) newPluginInstallCmd() *cobra.Command {
	var dev, yes bool
	cmd := &cobra.Command{
		Use:   "install <image>",
		Short: "Install or upgrade a plugin from its image",
		Long: "Pulls the image, shows what the plugin asks for and installs it once you\n" +
			"approve. --dev installs a local image without pulling it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.installPlugin(cmd, args[0], dev, yes)
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "Install a local image without pulling it")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve the plugin's capabilities without asking")
	return cmd
}

func (a *app) newPluginUpgradeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "upgrade <id> [tag]",
		Short: "Upgrade a plugin to a tag of its image",
		Long: "Pulls the tag (latest by default) of the plugin's image and installs it.\n" +
			"New capabilities need your approval; its containers restart on the new\n" +
			"image when next used.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag := "latest"
			if len(args) == 2 {
				tag = args[1]
			}
			list, err := a.listPlugins(cmd.Context())
			if err != nil {
				return err
			}
			i := slices.IndexFunc(list, func(p pluginJSON) bool { return p.ID == args[0] })
			if i < 0 {
				return fmt.Errorf("plugin %s is not installed", args[0])
			}
			if list[i].Dev {
				return fmt.Errorf("plugin %s is a dev install; install the new image with plugin install --dev", args[0])
			}
			named, err := reference.ParseNormalizedNamed(list[i].Image)
			if err != nil {
				return fmt.Errorf("reading the plugin's image: %w", err)
			}
			return a.installPlugin(cmd, named.Name()+":"+tag, false, yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve new capabilities without asking")
	return cmd
}

func (a *app) newPluginRemoveCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a plugin and its containers",
		Long:  "Removes a plugin and its containers. --purge also removes its data.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/plugins/" + url.PathEscape(args[0])
			if purge {
				path += "?purge=true"
			}
			if err := a.pluginRequest(cmd.Context(), http.MethodDelete, path, nil, http.StatusNoContent, nil); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also remove the plugin's data")
	return cmd
}

func (a *app) newPluginDevCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "dev <dir>",
		Short: "Build a plugin from its source and install it",
		Long: "Builds the Dockerfile in dir with the local docker CLI and installs the\n" +
			"image as a dev plugin. Run it again after a change to rebuild and reinstall.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			// An absolute dir names the image after the plugin's dir, even for ".".
			if abs, err := filepath.Abs(dir); err == nil {
				dir = abs
			}
			tag := devPluginTag(dir)
			if err := a.runDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "build", "-t", tag, dir); err != nil {
				return fmt.Errorf("building %s: %w", dir, err)
			}
			return a.installPlugin(cmd, tag, true, yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve the plugin's capabilities without asking")
	return cmd
}

// execRunner runs name with args, writing its output to stdout and stderr.
func execRunner(name string) func(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	return func(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Run()
	}
}

// devPluginTag is the local image tag loop plugin dev builds dir as.
func devPluginTag(dir string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '-'
	}, filepath.Base(dir))
	if name = strings.Trim(name, ".-_"); name == "" {
		name = "plugin"
	}
	return "loop-plugin-dev/" + name + ":latest"
}

// pluginJSON is an installed plugin as the API lists it.
type pluginJSON struct {
	ID      string `json:"id"`
	Image   string `json:"image"`
	Version string `json:"version"`
	Dev     bool   `json:"dev"`
	Status  string `json:"status"`
}

// pluginCandidateJSON is a pulled plugin image as the API shows it for
// review.
type pluginCandidateJSON struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Image           string   `json:"image"`
	Digest          string   `json:"digest"`
	Capabilities    []string `json:"capabilities"`
	NewCapabilities []string `json:"new_capabilities"`
	PreviousVersion string   `json:"previous_version"`
}

func (a *app) listPlugins(ctx context.Context) ([]pluginJSON, error) {
	var out struct {
		Plugins []pluginJSON `json:"plugins"`
	}
	if err := a.pluginRequest(ctx, http.MethodGet, "/api/plugins", nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return out.Plugins, nil
}

// installPlugin inspects image, asks the owner to approve what it asks for
// unless yes, then installs exactly the image they reviewed.
func (a *app) installPlugin(cmd *cobra.Command, image string, dev, yes bool) error {
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	var c pluginCandidateJSON
	if err := a.pluginRequest(ctx, http.MethodPost, "/api/plugins/inspect", map[string]any{"image": image, "dev": dev}, http.StatusOK, &c); err != nil {
		return err
	}
	printPluginCandidate(out, &c)
	// An upgrade asking for nothing new needs no approval.
	if !yes && (c.PreviousVersion == "" || len(c.NewCapabilities) > 0) {
		ok, err := confirm(cmd.InOrStdin(), out, "Install? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("not installed")
		}
	}
	body := map[string]any{"image": image, "dev": dev, "digest": c.Digest, "approve": c.Capabilities}
	if err := a.pluginRequest(ctx, http.MethodPost, "/api/plugins", body, http.StatusOK, &c); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Installed %s %s\n", c.ID, c.Version)
	return nil
}

func printPluginCandidate(out io.Writer, c *pluginCandidateJSON) {
	_, _ = fmt.Fprintf(out, "%s %s (%s)\n", c.Name, c.Version, c.ID)
	_, _ = fmt.Fprintf(out, "Image: %s\n", c.Image)
	if c.PreviousVersion != "" {
		_, _ = fmt.Fprintf(out, "Upgrades %s\n", c.PreviousVersion)
	}
	if len(c.Capabilities) == 0 {
		_, _ = fmt.Fprintln(out, "Capabilities: none")
	} else {
		_, _ = fmt.Fprintln(out, "Capabilities:")
		for _, cap := range c.Capabilities {
			if c.PreviousVersion != "" && slices.Contains(c.NewCapabilities, cap) {
				cap += " (new)"
			}
			_, _ = fmt.Fprintf(out, "  %s\n", cap)
		}
	}
	_, _ = fmt.Fprintln(out, "Its containers can reach the network.")
}

// confirm asks question and reports whether the answer is yes.
func confirm(in io.Reader, out io.Writer, question string) (bool, error) {
	_, _ = fmt.Fprint(out, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the answer: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// pluginRequest calls the daemon's plugin route at path with body as JSON,
// expects status want, and decodes the answer into out when it's set.
func (a *app) pluginRequest(ctx context.Context, method, path string, body map[string]any, want int, out any) error {
	var r io.Reader
	if body != nil {
		// The bodies are maps of strings, bools and string slices, which
		// always encode.
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.resolveAPIURL()+path, r)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling the plugin API: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}
	if resp.StatusCode != want {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parsing the answer: %w", err)
	}
	return nil
}
