// Package evolution mines git history for the structural-coupling
// signals the panel's "Evolution" tab renders: coupling pairs (files
// that almost always change together), churn hotspots (files that
// change disproportionately often), and bus-factor risk (files with a
// single dominant author).
//
// Loop already shells out to `git` directly elsewhere — we follow the
// same pattern instead of pulling in go-git. The command is run via a
// HistoryReader so tests can substitute a fake without provisioning a
// real repo.
//
// Default scope: --since=12.months.ago --max-count=1000. Recent enough
// to reflect current team patterns, old enough to catch real coupling;
// the commit cap bounds cost on hot monorepos.
package evolution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Default knobs the surfaces use unless overridden.
const (
	DefaultSinceMonths    = 12
	DefaultMaxCommits     = 1000
	DefaultMinCoupling    = 0.5
	DefaultMaxCouplePairs = 50
	DefaultMaxHotspots    = 20
	DefaultMaxBusFactor   = 20
	DefaultMinBusFactor   = 0.8
)

// Options is the analysis-tunable knob set. Zero values mean "use the
// matching Default*" constant.
type Options struct {
	SinceMonths    int
	MaxCommits     int
	MinCoupling    float64
	MaxCouplePairs int
	MaxHotspots    int
	MaxBusFactor   int
	MinBusFactor   float64
}

func (o Options) resolved() Options {
	if o.SinceMonths == 0 {
		o.SinceMonths = DefaultSinceMonths
	}
	if o.MaxCommits == 0 {
		o.MaxCommits = DefaultMaxCommits
	}
	if o.MinCoupling == 0 {
		o.MinCoupling = DefaultMinCoupling
	}
	if o.MaxCouplePairs == 0 {
		o.MaxCouplePairs = DefaultMaxCouplePairs
	}
	if o.MaxHotspots == 0 {
		o.MaxHotspots = DefaultMaxHotspots
	}
	if o.MaxBusFactor == 0 {
		o.MaxBusFactor = DefaultMaxBusFactor
	}
	if o.MinBusFactor == 0 {
		o.MinBusFactor = DefaultMinBusFactor
	}
	return o
}

// CommitFiles is one commit's metadata as a HistoryReader returns it.
// Files are repo-relative, slash-separated paths (matching the rest of
// the engine).
type CommitFiles struct {
	Hash      string
	Author    string
	Timestamp time.Time
	Files     []string
}

// HistoryReader fetches commit-level metadata for an evolution scan.
// Production uses ExecReader (git log subprocess); tests inject fakes
// that return deterministic CommitFiles slices.
type HistoryReader interface {
	Read(ctx context.Context, dirPath string, sinceMonths, maxCommits int) ([]CommitFiles, error)
}

// CouplingPair is one pair of files that change together. CoChangeCount
// is the number of commits both files appear in; Jaccard is the
// intersection-over-union ratio (1.0 = always together, 0.0 = never).
type CouplingPair struct {
	FileA         string  `json:"file_a"`
	FileB         string  `json:"file_b"`
	CoChangeCount int     `json:"co_change_count"`
	Jaccard       float64 `json:"jaccard"`
	CrossModule   bool    `json:"cross_module"`
}

// ChurnHotspot is one file with disproportionately many commits in the
// window.
type ChurnHotspot struct {
	File          string    `json:"file"`
	ChangeCount   int       `json:"change_count"`
	LastChangedAt time.Time `json:"last_changed_at"`
}

// BusFactorRisk is one file whose changes concentrate on a single
// author. SoleAuthorRatio = author's commits / total file commits;
// surfaced when above MinBusFactor.
type BusFactorRisk struct {
	File               string    `json:"file"`
	SoleAuthor         string    `json:"sole_author"`
	SoleAuthorRatio    float64   `json:"sole_author_ratio"`
	TotalCommits       int       `json:"total_commits"`
	DaysSinceLastOther int       `json:"days_since_last_other_author"`
	LastOtherAuthorAt  time.Time `json:"last_other_author_at,omitzero"`
}

// Result is the full evolution analysis. CommitsScanned is the actual
// number of commits the reader returned (≤ MaxCommits, possibly fewer
// for shallow clones); ShallowWarning is set when the count is
// suspiciously low and the user might want to fetch full history.
type Result struct {
	CommitsScanned int             `json:"commits_scanned"`
	ShallowWarning bool            `json:"shallow_warning"`
	CouplingPairs  []CouplingPair  `json:"coupling_pairs"`
	ChurnHotspots  []ChurnHotspot  `json:"churn_hotspots"`
	BusFactor      []BusFactorRisk `json:"bus_factor"`
}

// ErrNoHistory is returned by Analyze when the reader yields zero
// commits — usually the dir isn't a git repo at all. Callers map this
// to a "not a git repo" message rather than propagating raw errors.
var ErrNoHistory = errors.New("evolution: no commits found")

// Analyze runs the full evolution pipeline against the reader's history
// for dirPath. dirPath is the workspace root the panel/MCP/CLI passes
// in; the reader interprets it (the production reader sets it as the
// git command's working directory).
func Analyze(ctx context.Context, reader HistoryReader, dirPath string, opts Options) (Result, error) {
	if reader == nil {
		return Result{}, errors.New("evolution: reader is nil")
	}
	o := opts.resolved()

	commits, err := reader.Read(ctx, dirPath, o.SinceMonths, o.MaxCommits)
	if err != nil {
		return Result{}, fmt.Errorf("read history: %w", err)
	}
	if len(commits) == 0 {
		return Result{}, ErrNoHistory
	}

	res := Result{CommitsScanned: len(commits)}
	if len(commits) < 50 {
		res.ShallowWarning = true
	}

	res.CouplingPairs = coupling(commits, o)
	res.ChurnHotspots = hotspots(commits, o)
	res.BusFactor = busFactor(commits, o)
	return res, nil
}

// coupling computes the Jaccard similarity for every file pair that
// co-occurs in at least one commit, then keeps pairs above
// MinCoupling, sorted by Jaccard descending and capped at MaxCouplePairs.
func coupling(commits []CommitFiles, o Options) []CouplingPair {
	fileCommits := make(map[string]int)
	type pairKey struct{ a, b string }
	pairCommits := make(map[pairKey]int)

	for _, c := range commits {
		for _, f := range c.Files {
			fileCommits[f]++
		}
		for i := 0; i < len(c.Files); i++ {
			for j := i + 1; j < len(c.Files); j++ {
				a, b := c.Files[i], c.Files[j]
				if a > b {
					a, b = b, a
				}
				pairCommits[pairKey{a: a, b: b}]++
			}
		}
	}

	out := make([]CouplingPair, 0, len(pairCommits))
	for k, co := range pairCommits {
		union := fileCommits[k.a] + fileCommits[k.b] - co
		j := float64(co) / float64(union)
		if j < o.MinCoupling {
			continue
		}
		out = append(out, CouplingPair{
			FileA:         k.a,
			FileB:         k.b,
			CoChangeCount: co,
			Jaccard:       j,
			CrossModule:   topLevel(k.a) != topLevel(k.b),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Jaccard != out[j].Jaccard {
			return out[i].Jaccard > out[j].Jaccard
		}
		if out[i].FileA != out[j].FileA {
			return out[i].FileA < out[j].FileA
		}
		return out[i].FileB < out[j].FileB
	})
	if len(out) > o.MaxCouplePairs {
		out = out[:o.MaxCouplePairs]
	}
	return out
}

// hotspots ranks files by commit count in the window.
func hotspots(commits []CommitFiles, o Options) []ChurnHotspot {
	count := make(map[string]int)
	last := make(map[string]time.Time)
	for _, c := range commits {
		for _, f := range c.Files {
			count[f]++
			if c.Timestamp.After(last[f]) {
				last[f] = c.Timestamp
			}
		}
	}
	out := make([]ChurnHotspot, 0, len(count))
	for f, n := range count {
		out = append(out, ChurnHotspot{File: f, ChangeCount: n, LastChangedAt: last[f]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChangeCount != out[j].ChangeCount {
			return out[i].ChangeCount > out[j].ChangeCount
		}
		return out[i].File < out[j].File
	})
	if len(out) > o.MaxHotspots {
		out = out[:o.MaxHotspots]
	}
	return out
}

// busFactor finds files whose change history concentrates on one
// author above MinBusFactor. We compute days-since-last-other-author
// from the most recent commit's timestamp (the analysis "now") rather
// than time.Now() so the metric stays deterministic across re-runs.
func busFactor(commits []CommitFiles, o Options) []BusFactorRisk {
	type fileStats struct {
		total         int
		byAuthor      map[string]int
		lastOther     time.Time
		soleCandidate string
	}
	files := make(map[string]*fileStats)
	now := time.Time{}
	for _, c := range commits {
		if c.Timestamp.After(now) {
			now = c.Timestamp
		}
		for _, f := range c.Files {
			st, ok := files[f]
			if !ok {
				st = &fileStats{byAuthor: make(map[string]int)}
				files[f] = st
			}
			st.total++
			st.byAuthor[c.Author]++
		}
	}
	for f, st := range files {
		dominant := ""
		dominantCount := 0
		for author, n := range st.byAuthor {
			if n > dominantCount || (n == dominantCount && (dominant == "" || author < dominant)) {
				dominant = author
				dominantCount = n
			}
		}
		st.soleCandidate = dominant
		_ = f
	}
	for _, c := range commits {
		for _, f := range c.Files {
			st := files[f]
			if c.Author != st.soleCandidate && c.Timestamp.After(st.lastOther) {
				st.lastOther = c.Timestamp
			}
		}
	}

	out := make([]BusFactorRisk, 0, len(files))
	for f, st := range files {
		ratio := float64(st.byAuthor[st.soleCandidate]) / float64(st.total)
		if ratio < o.MinBusFactor {
			continue
		}
		risk := BusFactorRisk{
			File:              f,
			SoleAuthor:        st.soleCandidate,
			SoleAuthorRatio:   ratio,
			TotalCommits:      st.total,
			LastOtherAuthorAt: st.lastOther,
		}
		if !st.lastOther.IsZero() {
			risk.DaysSinceLastOther = int(now.Sub(st.lastOther).Hours() / 24)
		} else {
			risk.DaysSinceLastOther = -1
		}
		out = append(out, risk)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SoleAuthorRatio != out[j].SoleAuthorRatio {
			return out[i].SoleAuthorRatio > out[j].SoleAuthorRatio
		}
		if out[i].TotalCommits != out[j].TotalCommits {
			return out[i].TotalCommits > out[j].TotalCommits
		}
		return out[i].File < out[j].File
	})
	if len(out) > o.MaxBusFactor {
		out = out[:o.MaxBusFactor]
	}
	return out
}

// topLevel returns the first slash-segment of p, matching the engine's
// module-clustering rule. Used to flag cross-module coupling pairs.
func topLevel(p string) string {
	if head, _, ok := strings.Cut(p, "/"); ok {
		return head
	}
	return p
}
