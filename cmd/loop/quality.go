package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

// newQualityCmd registers `loop quality` and its subcommands. Mirrors the
// gofmt philosophy from the design plan: rule status is data, the CLI
// exits 0 unless the engine itself crashes. Users gate CI by piping
// --json into jq.
func (a *app) newQualityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quality",
		Short: "Structural-quality scans (signal, rules, metrics)",
	}
	cmd.AddCommand(a.newQualityScanCmd())
	cmd.AddCommand(a.newQualityCyclesCmd())
	cmd.AddCommand(a.newQualityWhatifCmd())
	cmd.AddCommand(a.newQualityEvolutionCmd())
	cmd.AddCommand(a.newQualityC4Cmd())
	return cmd
}

func (a *app) newQualityScanCmd() *cobra.Command {
	var jsonOut bool
	var maxFiles int
	cmd := &cobra.Command{
		Use:   "scan [path]",
		Short: "Scan a workspace and print the quality signal",
		Long: `Scan walks the given directory (default: current working dir),
parses supported source files, computes the 5 structural metrics, and
prints the aggregated quality_signal plus per-rule pass/fail.

Exit code is always 0 unless the engine crashes. Rule status is data:
  loop quality scan --json | jq -e '.rules.failed | length == 0'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			root := ""
			if len(args) == 1 {
				root = args[0]
			}
			p, err := a.newQualityParser()
			if err != nil {
				return fmt.Errorf("init parser: %w", err)
			}
			return a.runQualityScan(c.Context(), c.OutOrStdout(), c.ErrOrStderr(), p, engine.Config{MaxFiles: maxFiles}, root, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON instead of the human summary")
	cmd.Flags().IntVar(&maxFiles, "max-files", 0, "abort if scannable file count exceeds this; 0 uses the default")
	return cmd
}

// scanReport is the JSON contract for `loop quality scan --json`. Kept
// separate from snapshot.Snapshot so the schema stays under our control
// (snapshot is internal persistence shape; this is the public CLI shape).
type scanReport struct {
	DirPath     string         `json:"dir_path"`
	Signal      int            `json:"signal"`
	GeoMean     float64        `json:"geo_mean"`
	FileCount   int            `json:"file_count"`
	ParseFailed int            `json:"parse_failed"`
	Metrics     []metricReport `json:"metrics"`
	Rules       rulesReport    `json:"rules"`
}

type metricReport struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Raw   float64 `json:"raw"`
}

type rulesReport struct {
	Passed []ruleReport `json:"passed"`
	Failed []ruleReport `json:"failed"`
}

type ruleReport struct {
	Name      string           `json:"name"`
	Severity  string           `json:"severity"`
	Message   string           `json:"message"`
	Citations []citationReport `json:"citations,omitempty"`
}

type citationReport struct {
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

func (a *app) runQualityScan(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, dirPath string, jsonOut bool) error {
	if dirPath == "" {
		wd, err := a.sys.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		dirPath = wd
	}

	cache := graph.NewCache()
	eng := engine.New(p, noopStore{}, cache, engine.OSFileSystem{}, cfg, nil)
	res, err := eng.Scan(ctx, "cli", "main", dirPath)
	if err != nil {
		var tooLarge *graph.RepoTooLargeError
		if errors.As(err, &tooLarge) {
			fmt.Fprintf(stderr, "repo too large to scan (%d files; limit %d). Add patterns to quality.exclude_paths or raise quality.max_files.\n", tooLarge.FileCount, tooLarge.Limit)
			return err
		}
		return fmt.Errorf("scan: %w", err)
	}

	g, _ := cache.Get("cli")
	ruleResults := rules.Run(rules.DefaultConfig(), g, res.Signal)

	report := buildScanReport(dirPath, res, ruleResults)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return writeHumanReport(out, report)
}

func buildScanReport(dirPath string, res engine.ScanResult, ruleResults []rules.Result) scanReport {
	rep := scanReport{
		DirPath:     dirPath,
		Signal:      res.Signal.Value,
		GeoMean:     res.Signal.GeoMean,
		FileCount:   res.FileCount,
		ParseFailed: res.ParseFailed,
	}
	for _, m := range res.Signal.Metrics {
		rep.Metrics = append(rep.Metrics, metricReport{Name: m.Name, Score: m.Score, Raw: m.Raw})
	}
	for _, r := range ruleResults {
		rr := ruleReport{Name: r.Name, Severity: string(r.Severity), Message: r.Message}
		for _, c := range r.Citations {
			rr.Citations = append(rr.Citations, citationReport{Path: c.Path, Note: c.Note})
		}
		if r.Severity == rules.SevFail {
			rep.Rules.Failed = append(rep.Rules.Failed, rr)
		} else {
			rep.Rules.Passed = append(rep.Rules.Passed, rr)
		}
	}
	return rep
}

func writeHumanReport(out io.Writer, r scanReport) error {
	fmt.Fprintf(out, "quality_signal: %d (geo_mean %.3f)\n", r.Signal, r.GeoMean)
	fmt.Fprintf(out, "scanned %d files (%d parse-failed) under %s\n", r.FileCount, r.ParseFailed, r.DirPath)
	fmt.Fprintln(out, "metrics:")
	for _, m := range r.Metrics {
		fmt.Fprintf(out, "  %-12s score=%.3f raw=%.3f\n", m.Name, m.Score, m.Raw)
	}
	fmt.Fprintln(out, "rules:")
	for _, ru := range r.Rules.Passed {
		fmt.Fprintf(out, "  ✓ %s — %s\n", ru.Name, ru.Message)
	}
	for _, ru := range r.Rules.Failed {
		fmt.Fprintf(out, "  ✗ %s — %s\n", ru.Name, ru.Message)
		for _, c := range ru.Citations {
			fmt.Fprintf(out, "      %s (%s)\n", c.Path, c.Note)
		}
	}
	return nil
}

// defaultNewQualityParser is the production parser-factory hook. Held
// as an app field so tests can substitute a parser-init failure.
func defaultNewQualityParser() (parser.Parser, error) {
	return parser.New(parser.DefaultSpecs())
}

// noopStore satisfies snapshot.Store without persisting anywhere — the
// CLI is one-shot and prints the result instead of caching it.
type noopStore struct{}

func (noopStore) Save(_ context.Context, _, _ string, _ metrics.Signal, _ time.Time) error {
	return nil
}
func (noopStore) Get(_ context.Context, _, _ string) (*snapshot.Snapshot, error) {
	return nil, snapshot.ErrNotFound
}
func (noopStore) GetLatest(_ context.Context, _ string) (*snapshot.Snapshot, error) {
	return nil, snapshot.ErrNotFound
}
func (noopStore) DeleteForChannel(_ context.Context, _ string) error { return nil }
