package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/apiauth"
)

func (a *app) newAPIRotateTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "api:rotate-token",
		Short: "Replace the API token the desktop app and CLI use",
		Long: "Writes a new owner API token. A running daemon switches to it at once;\n" +
			"the desktop app and the CLI read it again on their next request.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.rotateAPIToken(cmd.OutOrStdout())
		},
	}
}

// rotateAPIToken asks the daemon to rotate the owner token, or rotates the
// token file itself when the daemon isn't running.
func (a *app) rotateAPIToken(out io.Writer) error {
	resp, err := a.apiClient.Post(a.resolveAPIURL()+"/api/auth/rotate", "application/json", nil)
	if err != nil {
		path, perr := apiauth.OwnerTokenPath(a.userConfigDir)
		if perr != nil {
			return perr
		}
		if _, err := apiauth.NewTokenFile(path).Rotate(); err != nil {
			return fmt.Errorf("rotating the API token: %w", err)
		}
		_, _ = fmt.Fprintln(out, "API token rotated (the daemon isn't running; it uses the new token when it starts)")
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rotating the API token: %s: %s", resp.Status, body)
	}
	_, _ = fmt.Fprintln(out, "API token rotated")
	return nil
}

func (a *app) newAppURLCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "app:url",
		Short: "Print a URL that opens the web UI signed in",
		Long: "Prints the web UI's URL with the owner API token in its fragment, for\n" +
			"running the UI in a browser (the desktop app doesn't need it). The\n" +
			"fragment isn't sent to the server; the UI keeps the token for the tab.\n" +
			"Anyone with the URL has full access to the API, so treat it as a secret.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.printAppURL(cmd.OutOrStdout(), base)
		},
	}
	cmd.Flags().StringVar(&base, "base", "http://localhost:5173/", "Web UI URL")
	return cmd
}

func (a *app) printAppURL(out io.Writer, base string) error {
	path, err := apiauth.OwnerTokenPath(a.userConfigDir)
	if err != nil {
		return err
	}
	tok, err := apiauth.NewTokenFile(path).Load()
	if err != nil {
		return errors.New("no API token yet: start the daemon (loop serve) first")
	}
	_, _ = fmt.Fprintf(out, "%s#loop_token=%s\n", base, tok)
	return nil
}
