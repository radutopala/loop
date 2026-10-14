package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/backup"
)

func (a *app) newBackupRestoreCmd() *cobra.Command {
	var identityFile string
	cmd := &cobra.Command{
		Use:   "backup:restore <file>",
		Short: "Restore the database and playgrounds from a backup",
		Long: `Restore the database and playgrounds from a backup Loop wrote.

The database is replaced; the one it replaces is kept next to it as
loop.db.before-restore-<time>. Playgrounds are put back when no playground
of that name exists; existing ones are left as they are.

An encrypted backup (.zip.age) needs the private key printed when the key
was created: pass the file it's saved in with --identity, or paste it when
asked. The daemon is stopped while the database is replaced, and started
again if it was running.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.restoreBackup(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], identityFile)
		},
	}
	cmd.Flags().StringVarP(&identityFile, "identity", "i", "", "file holding the private key (AGE-SECRET-KEY-1...) of an encrypted backup")
	return cmd
}

func (a *app) restoreBackup(stdin io.Reader, stdout, stderr io.Writer, file, identityFile string) error {
	cfg, err := a.configLoad()
	if err != nil {
		return err
	}
	sys := a.newSystem()
	status, _ := a.daemonStatus(sys)
	running := status == "running"
	res, err := backup.NewRestorer().Restore(backup.RestoreOptions{
		File: file,
		Identity: func() ([]age.Identity, error) {
			return a.backupIdentity(stdin, stderr, identityFile)
		},
		DBPath:  cfg.DBPath,
		LoopDir: cfg.LoopDir,
		StopDaemon: func() error {
			if !running {
				return nil
			}
			return a.daemonStop(sys)
		},
		StartDaemon: func() error {
			if !running {
				return nil
			}
			return a.daemonStart(sys, cfg.LogFile)
		},
	})
	if res != nil {
		printRestore(stdout, res, running)
	}
	return err
}

// backupIdentity reads the private key of an encrypted backup from
// identityFile, or asks for it on stdin.
func (a *app) backupIdentity(stdin io.Reader, stderr io.Writer, identityFile string) ([]age.Identity, error) {
	var data string
	if identityFile != "" {
		b, err := a.sys.ReadFile(identityFile)
		if err != nil {
			return nil, fmt.Errorf("reading the private key: %w", err)
		}
		data = string(b)
	} else {
		_, _ = fmt.Fprint(stderr, "The backup is encrypted. Paste its private key (AGE-SECRET-KEY-1...): ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		data = line
	}
	ids, err := age.ParseIdentities(strings.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the private key: %w", err)
	}
	return ids, nil
}

func printRestore(w io.Writer, res *backup.RestoreResult, running bool) {
	m := res.Manifest
	_, _ = fmt.Fprintf(w, "Restored the backup of %s", m.CreatedAt.Local().Format(time.DateTime))
	if m.Version != "" {
		_, _ = fmt.Fprintf(w, " (Loop %s)", m.Version)
	}
	_, _ = fmt.Fprintln(w, ".")
	for _, p := range res.MovedAside {
		_, _ = fmt.Fprintf(w, "Moved the replaced database to %s\n", p)
	}
	for _, p := range res.Restored {
		_, _ = fmt.Fprintf(w, "Restored the playground %s\n", p)
	}
	for _, p := range res.Kept {
		_, _ = fmt.Fprintf(w, "Kept the existing playground %s\n", p)
	}
	if !running {
		_, _ = fmt.Fprintln(w, "The daemon wasn't running; start it with loop daemon:start.")
	}
}
