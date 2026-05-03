package metrics

import (
	"sort"
	"strings"

	"github.com/radutopala/loop/internal/quality/graph"
)

// RedundancyName is the canonical key for the dead-code / redundancy metric.
const RedundancyName = "redundancy"

// RedundancyDetail is the panel-facing payload: candidate dead functions
// surfaced for the diagnostics list. The signal is a heuristic — it
// flags functions whose name never appears in any Call site — so it
// over-reports on interface satisfaction, reflection, and externally
// consumed library APIs. The panel marks entries as "candidates" rather
// than certainties.
type RedundancyDetail struct {
	// DeadFunctions is the capped list shown in the panel, sorted by
	// (Path asc, StartLine asc). Capped at redundancyHotspotCap entries;
	// the full count is in DeadCount.
	DeadFunctions []DeadFunction

	// TotalFunctions is every function definition seen in the graph,
	// including filtered entry-point and interface-method names.
	TotalFunctions int

	// DeadCount is the unfiltered total — may exceed len(DeadFunctions)
	// when capped for the panel.
	DeadCount int
}

// DeadFunction is one candidate the panel surfaces in the diagnostics
// list and the rules engine consumes.
type DeadFunction struct {
	Path      string
	Name      string
	StartLine int
	EndLine   int
}

const redundancyHotspotCap = 20

// entryNames are functions the runtime calls implicitly. Never flagged.
var entryNames = map[string]struct{}{
	"main": {},
	"init": {},
}

// entryPrefixes are test-framework and fuzz harness conventions. Any
// function whose name starts with one of these is considered reachable.
var entryPrefixes = []string{"Test", "Benchmark", "Example", "Fuzz"}

// interfaceNames are common stdlib interface method names. They're
// invoked via interface dispatch — no Call site references them by
// name — so we'd false-positive without this list. Not exhaustive;
// curated to the stdlib + serialisation surface.
var interfaceNames = map[string]struct{}{
	"String":          {},
	"Error":           {},
	"MarshalJSON":     {},
	"UnmarshalJSON":   {},
	"MarshalText":     {},
	"UnmarshalText":   {},
	"MarshalBinary":   {},
	"UnmarshalBinary": {},
	"ServeHTTP":       {},
	"Read":            {},
	"Write":           {},
	"Close":           {},
	"Format":          {},
	"Scan":            {},
	"Value":           {},
	"Len":             {},
	"Less":            {},
	"Swap":            {},
}

// isReachableByConvention reports whether a function name is implicitly
// called by the runtime, the test harness, or interface dispatch and
// therefore shouldn't appear in the dead list.
func isReachableByConvention(name string) bool {
	if _, ok := entryNames[name]; ok {
		return true
	}
	if _, ok := interfaceNames[name]; ok {
		return true
	}
	for _, p := range entryPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Redundancy estimates dead code by name-reachability: a function whose
// name never appears in any Call site, after filtering out runtime and
// interface conventions, is flagged as a candidate. Score is the share
// of definitions that *aren't* dead.
//
// Empty graphs and graphs with no functions return Score 1.0 — there's
// nothing to be dead.
func Redundancy(g *graph.Graph) Result {
	if g == nil || len(g.Nodes) == 0 {
		return Result{Name: RedundancyName, Raw: 0, Score: 1.0, Detail: RedundancyDetail{}}
	}

	callSet := make(map[string]struct{})
	totalFns := 0
	for _, n := range g.Nodes {
		totalFns += len(n.Functions)
		for _, c := range n.Calls {
			callSet[c.Name] = struct{}{}
		}
	}
	if totalFns == 0 {
		return Result{Name: RedundancyName, Raw: 0, Score: 1.0, Detail: RedundancyDetail{}}
	}

	var dead []DeadFunction
	for _, n := range g.Nodes {
		for _, f := range n.Functions {
			if isReachableByConvention(f.Name) {
				continue
			}
			if _, called := callSet[f.Name]; called {
				continue
			}
			dead = append(dead, DeadFunction{
				Path:      n.Path,
				Name:      f.Name,
				StartLine: f.StartLine,
				EndLine:   f.EndLine,
			})
		}
	}
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].Path != dead[j].Path {
			return dead[i].Path < dead[j].Path
		}
		return dead[i].StartLine < dead[j].StartLine
	})

	deadCount := len(dead)
	listed := dead
	if len(listed) > redundancyHotspotCap {
		listed = listed[:redundancyHotspotCap]
	}

	score := 1.0 - float64(deadCount)/float64(totalFns)
	return Result{
		Name:  RedundancyName,
		Raw:   float64(deadCount),
		Score: clamp01(score),
		Detail: RedundancyDetail{
			DeadFunctions:  listed,
			TotalFunctions: totalFns,
			DeadCount:      deadCount,
		},
	}
}
