// Package main: quality_diagnostics.go adds the diagnostics/insight tier
// subcommands beneath `loop quality` — cycles, whatif, evolution, c4.
// Each one mirrors a single MCP tool / HTTP endpoint and shares the
// scan-then-emit pipeline with `loop quality scan`. All commands print
// human-readable text by default and accept --json for machine-readable
// output. Like the parent, they exit 0 unless the engine itself crashes.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/quality/c4"
	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/whatif"
)

// newQualityCyclesCmd lists import cycles in the codebase. Implemented
// as a one-shot scan + Tarjan rather than a snapshot read so the CLI
// stays usable without a running daemon.
func (a *app) newQualityCyclesCmd() *cobra.Command {
	return a.newScanThenEmitCmd("cycles [path]",
		"List import cycles (strongly connected components > 1)",
		"emit machine-readable JSON",
		func(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, root string, jsonOut bool) error {
			return a.runQualityCycles(ctx, out, stderr, p, cfg, root, jsonOut)
		})
}

// newScanThenEmitCmd is the shared command shape for one-shot scan
// subcommands (cycles, c4) — they all parse [path], init the parser,
// scan, then hand the cached graph to a per-command emitter.
func (a *app) newScanThenEmitCmd(use, short, jsonHelp string, run func(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, root string, jsonOut bool) error) *cobra.Command {
	var jsonOut bool
	var maxFiles int
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			root := ""
			if len(args) == 1 {
				root = args[0]
			}
			p, err := a.newQualityParser()
			if err != nil {
				return fmt.Errorf("init parser: %w", err)
			}
			return run(c.Context(), c.OutOrStdout(), c.ErrOrStderr(), p, engine.Config{MaxFiles: maxFiles}, root, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, jsonHelp)
	cmd.Flags().IntVar(&maxFiles, "max-files", 0, "abort if scannable file count exceeds this; 0 uses the default")
	return cmd
}

type cyclesReport struct {
	DirPath            string     `json:"dir_path"`
	Cycles             [][]string `json:"cycles"`
	LargestCycleSize   int        `json:"largest_cycle_size"`
	TotalNodesInCycles int        `json:"total_nodes_in_cycles"`
}

func (a *app) runQualityCycles(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, dirPath string, jsonOut bool) error {
	g, resolved, err := a.scanForGraph(ctx, stderr, p, cfg, dirPath)
	if err != nil {
		return err
	}
	res := metrics.Cycles(g)
	detail, _ := res.Detail.(metrics.CyclesDetail)
	rep := cyclesReport{
		DirPath:            resolved,
		Cycles:             detail.Cycles,
		LargestCycleSize:   detail.LargestCycleSize,
		TotalNodesInCycles: detail.TotalNodesInCycles,
	}
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if len(rep.Cycles) == 0 {
		fmt.Fprintf(out, "No import cycles in %s.\n", rep.DirPath)
		return nil
	}
	fmt.Fprintf(out, "Found %d cycle(s); %d files in cycles (largest: %d):\n",
		len(rep.Cycles), rep.TotalNodesInCycles, rep.LargestCycleSize)
	for i, cyc := range rep.Cycles {
		fmt.Fprintf(out, "  %d.\n", i+1)
		for _, f := range cyc {
			fmt.Fprintf(out, "       %s\n", f)
		}
	}
	return nil
}

// newQualityWhatifCmd takes a JSON mutation list (file via --file or stdin)
// and prints the predicted signal delta. The mutation grammar matches
// whatif.Mutation: {"op": "delete|move|split", "path": "...",
// "new_module": "...", "parts": N}.
func (a *app) newQualityWhatifCmd() *cobra.Command {
	var jsonOut bool
	var maxFiles int
	var mutationFile string
	cmd := &cobra.Command{
		Use:   "whatif [path]",
		Short: "Simulate refactor mutations and print predicted signal delta",
		Long: `Mutation list comes from --file (or stdin if --file is "-").
Each mutation is one JSON object with keys: op (delete | move | split),
path (file to mutate), new_module (move only), parts (split only).
Pass an array for batched mutations.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if mutationFile == "" {
				return errors.New("--file is required (use - for stdin)")
			}
			root := ""
			if len(args) == 1 {
				root = args[0]
			}
			p, err := a.newQualityParser()
			if err != nil {
				return fmt.Errorf("init parser: %w", err)
			}
			muts, err := a.readMutations(c.InOrStdin(), mutationFile)
			if err != nil {
				return fmt.Errorf("reading mutations: %w", err)
			}
			return a.runQualityWhatif(c.Context(), c.OutOrStdout(), c.ErrOrStderr(), p, engine.Config{MaxFiles: maxFiles}, root, muts, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")
	cmd.Flags().IntVar(&maxFiles, "max-files", 0, "abort if scannable file count exceeds this; 0 uses the default")
	cmd.Flags().StringVar(&mutationFile, "file", "", "path to JSON mutation list (use - for stdin)")
	return cmd
}

func (a *app) readMutations(stdin io.Reader, source string) ([]whatif.Mutation, error) {
	var data []byte
	var err error
	if source == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = a.sys.ReadFile(source)
	}
	if err != nil {
		return nil, err
	}
	// Accept either an array or a single object — same convenience as kubectl.
	var arr []whatif.Mutation
	if jsonErr := json.Unmarshal(data, &arr); jsonErr == nil {
		return arr, nil
	}
	var single whatif.Mutation
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, fmt.Errorf("decoding mutations: %w", err)
	}
	return []whatif.Mutation{single}, nil
}

func (a *app) runQualityWhatif(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, dirPath string, muts []whatif.Mutation, jsonOut bool) error {
	g, _, err := a.scanForGraph(ctx, stderr, p, cfg, dirPath)
	if err != nil {
		return err
	}
	res, err := whatif.Simulate(g, muts)
	if err != nil {
		return fmt.Errorf("simulate: %w", err)
	}
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	fmt.Fprintf(out, "Signal: %d → %d (%+d)\n",
		res.BaselineSignal, res.PredictedSignal, res.DeltaSignal)
	fmt.Fprintln(out, "Predicted metrics:")
	for _, m := range res.PredictedMetrics {
		fmt.Fprintf(out, "  %-12s score=%.3f raw=%.3f\n", m.Name, m.Score, m.Raw)
	}
	return nil
}

// newQualityEvolutionCmd mines the workdir's git history for coupling,
// churn, and bus-factor signals. Requires `git` on PATH and the
// directory to be a git repo.
func (a *app) newQualityEvolutionCmd() *cobra.Command {
	var jsonOut bool
	var sinceMonths, maxCommits int
	cmd := &cobra.Command{
		Use:   "evolution [path]",
		Short: "Mine git history for coupling, churn, and bus-factor signals",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			root := ""
			if len(args) == 1 {
				root = args[0]
			}
			return a.runQualityEvolution(c.Context(), c.OutOrStdout(), root, evolution.Options{
				SinceMonths: sinceMonths,
				MaxCommits:  maxCommits,
			}, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")
	cmd.Flags().IntVar(&sinceMonths, "since-months", 0, "history window in months; 0 uses the default (12)")
	cmd.Flags().IntVar(&maxCommits, "max-commits", 0, "cap on commits to scan; 0 uses the default (1000)")
	return cmd
}

func (a *app) runQualityEvolution(ctx context.Context, out io.Writer, dirPath string, opts evolution.Options, jsonOut bool) error {
	if dirPath == "" {
		wd, err := a.sys.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		dirPath = wd
	}
	res, err := evolution.Analyze(ctx, a.newEvolutionReader(), dirPath, opts)
	if err != nil {
		if errors.Is(err, evolution.ErrNoHistory) {
			return fmt.Errorf("no git history under %s — is this a git repo?", dirPath)
		}
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	fmt.Fprintf(out, "Scanned %d commits in %s", res.CommitsScanned, dirPath)
	if res.ShallowWarning {
		fmt.Fprint(out, " (shallow clone — fewer than expected; consider git fetch --unshallow)")
	}
	fmt.Fprintln(out, ".")
	if len(res.CouplingPairs) > 0 {
		fmt.Fprintln(out, "Coupling pairs:")
		for _, p := range res.CouplingPairs {
			cross := ""
			if p.CrossModule {
				cross = " [cross-module]"
			}
			fmt.Fprintf(out, "  %s ⇄ %s — j=%.2f, %d co-changes%s\n",
				p.FileA, p.FileB, p.Jaccard, p.CoChangeCount, cross)
		}
	}
	if len(res.ChurnHotspots) > 0 {
		fmt.Fprintln(out, "Churn hotspots:")
		for _, h := range res.ChurnHotspots {
			fmt.Fprintf(out, "  %s — %d changes\n", h.File, h.ChangeCount)
		}
	}
	if len(res.BusFactor) > 0 {
		fmt.Fprintln(out, "Bus-factor risks:")
		for _, r := range res.BusFactor {
			fmt.Fprintf(out, "  %s — %s owns %.0f%% (%d commits)\n",
				r.File, r.SoleAuthor, r.SoleAuthorRatio*100, r.TotalCommits)
		}
	}
	return nil
}

// newQualityC4Cmd emits the cached graph as a Mermaid component diagram.
func (a *app) newQualityC4Cmd() *cobra.Command {
	return a.newScanThenEmitCmd("c4 [path]",
		"Emit a C4 component diagram (Mermaid) for the workspace",
		"emit machine-readable JSON wrapping the Mermaid block",
		func(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, root string, jsonOut bool) error {
			return a.runQualityC4(ctx, out, stderr, p, cfg, root, jsonOut)
		})
}

func (a *app) runQualityC4(ctx context.Context, out, stderr io.Writer, p parser.Parser, cfg engine.Config, dirPath string, jsonOut bool) error {
	g, _, err := a.scanForGraph(ctx, stderr, p, cfg, dirPath)
	if err != nil {
		return err
	}
	d := c4.Emit(g)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}
	fmt.Fprintln(out, d.Mermaid)
	return nil
}

// scanForGraph runs an ephemeral scan keyed on "cli" and returns the
// cached graph. Shared by the cycles, whatif, and c4 subcommands so the
// scan-then-read pattern stays consistent.
func (a *app) scanForGraph(ctx context.Context, stderr io.Writer, p parser.Parser, cfg engine.Config, dirPath string) (*graph.Graph, string, error) {
	if dirPath == "" {
		wd, err := a.sys.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("resolving working directory: %w", err)
		}
		dirPath = wd
	}
	cache := graph.NewCache()
	eng := engine.New(p, noopStore{}, cache, engine.OSFileSystem{}, cfg, nil)
	if _, err := eng.Scan(ctx, "cli", "main", dirPath); err != nil {
		var tooLarge *graph.RepoTooLargeError
		if errors.As(err, &tooLarge) {
			fmt.Fprintf(stderr, "repo too large to scan (%d files; limit %d). Add patterns to quality.exclude_paths or raise quality.max_files.\n", tooLarge.FileCount, tooLarge.Limit)
			return nil, dirPath, err
		}
		return nil, dirPath, fmt.Errorf("scan: %w", err)
	}
	g, _ := cache.Get("cli")
	return g, dirPath, nil
}
