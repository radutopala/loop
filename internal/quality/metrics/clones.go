package metrics

import (
	"math/bits"
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// ClonesName is the canonical key for the clone-cluster sub-metric. It
// is folded into RedundancyName at the signal level (see redundancy.go);
// callers that want the standalone score use ComputeClones directly.
const ClonesName = "clones"

// ClonesConfig carries the knobs that control clone matching: minimum
// function size to consider, and the SimHash hamming-distance ceiling
// for two functions to land in the same cluster. Defaults are tuned
// for "near-duplicate" detection — exact copies always cluster, light
// renames usually cluster, refactors that change shape do not.
type ClonesConfig struct {
	// MinLOC is the floor on Body.LOC; smaller functions are skipped to
	// avoid clustering trivial getters/setters.
	MinLOC int

	// MaxDistance is the max hamming distance between two SimHash
	// fingerprints in the same cluster. 0 = exact, 64 = anything.
	MaxDistance int
}

// DefaultClonesConfig returns the production knobs.
func DefaultClonesConfig() ClonesConfig {
	return ClonesConfig{
		MinLOC:      5,
		MaxDistance: 3,
	}
}

// ClonesDetail is the panel-facing payload: clusters of similar
// functions and the duplicated-LOC summary that drives the score.
type ClonesDetail struct {
	// Clusters is the capped cluster list, sorted by total LOC desc,
	// tie-broken by smallest path. Each cluster's Members are sorted by
	// (Path, StartLine).
	Clusters []CloneCluster

	// DuplicatedLOC is the total LOC across all clusters' members minus
	// one representative per cluster (the first occurrence is "the
	// original"; the rest are duplicates). Used as the score numerator.
	DuplicatedLOC int

	// TotalLOC sums Body.LOC across every clone-eligible function (passes
	// MinLOC filter) so the panel can render duplicated/total as a ratio.
	TotalLOC int

	// ClusterCount is the unfiltered total — may exceed len(Clusters)
	// when capped.
	ClusterCount int
}

// CloneCluster is one set of similar functions.
type CloneCluster struct {
	Members []CloneMember

	// LOC is the cluster's total LOC across all members.
	LOC int

	// MaxDistance is the largest pairwise Hamming distance observed
	// within the cluster. 0 means exact-shape duplicates.
	MaxDistance int
}

// CloneMember is one function in a cluster.
type CloneMember struct {
	Path      string
	Name      string
	StartLine int
	EndLine   int
	LOC       int
}

const (
	clonesClusterCap = 50
	clonesMemberCap  = 20
)

// ComputeClones reduces clone-similarity scoring across the graph to a
// single Result. Algorithm:
//  1. SimHash each eligible function's shingles to a 64-bit fingerprint.
//  2. Bucket fingerprints by 16-bit prefix to make pair search ~O(n·k).
//  3. Within each bucket, group fps with Hamming distance ≤ MaxDistance
//     using union-find.
//  4. Drop singleton clusters; sort by total LOC desc.
//
// Score = 1 - DuplicatedLOC / max(1, TotalLOC). Score 1.0 means no
// duplication detected; lower means more code is repeating.
func ComputeClones(g *graph.Graph, cfg ClonesConfig) Result {
	if g == nil || len(g.Nodes) == 0 {
		return Result{Name: ClonesName, Raw: 0, Score: 1.0, Detail: ClonesDetail{}}
	}

	candidates := collectCloneCandidates(g, cfg.MinLOC)
	if len(candidates) == 0 {
		return Result{Name: ClonesName, Raw: 0, Score: 1.0, Detail: ClonesDetail{}}
	}

	totalLOC := 0
	for _, c := range candidates {
		totalLOC += c.loc
	}

	clusters := clusterClones(candidates, cfg.MaxDistance)
	duplicated := 0
	for _, cl := range clusters {
		// One member is "the original"; the rest are duplicates of it.
		extras := len(cl.Members) - 1
		if extras > 0 {
			// Rough attribution: per-cluster LOC minus the smallest
			// member (treated as the original) over-counts when sizes
			// differ; using cluster total - max member is more honest.
			maxMember := 0
			for _, m := range cl.Members {
				if m.LOC > maxMember {
					maxMember = m.LOC
				}
			}
			duplicated += cl.LOC - maxMember
		}
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].LOC != clusters[j].LOC {
			return clusters[i].LOC > clusters[j].LOC
		}
		return clusters[i].Members[0].Path < clusters[j].Members[0].Path
	})
	clusterCount := len(clusters)
	if len(clusters) > clonesClusterCap {
		clusters = clusters[:clonesClusterCap]
	}
	for i := range clusters {
		if len(clusters[i].Members) > clonesMemberCap {
			clusters[i].Members = clusters[i].Members[:clonesMemberCap]
		}
	}

	score := 1.0
	if totalLOC > 0 {
		score = 1.0 - float64(duplicated)/float64(totalLOC)
	}
	return Result{
		Name:  ClonesName,
		Raw:   float64(duplicated),
		Score: clamp01(score),
		Detail: ClonesDetail{
			Clusters:      clusters,
			DuplicatedLOC: duplicated,
			TotalLOC:      totalLOC,
			ClusterCount:  clusterCount,
		},
	}
}

// cloneCandidate is one function that passed the MinLOC filter and
// produced a fingerprint — internal scratch space the bucketing /
// clustering code consumes.
type cloneCandidate struct {
	path      string
	name      string
	startLine int
	endLine   int
	loc       int
	fp        uint64
}

func collectCloneCandidates(g *graph.Graph, minLOC int) []cloneCandidate {
	var out []cloneCandidate
	for _, n := range g.Nodes {
		for _, f := range n.Functions {
			if f.Body == nil || f.Body.LOC < minLOC {
				continue
			}
			if len(f.Body.Shingles) == 0 {
				continue
			}
			out = append(out, cloneCandidate{
				path:      n.Path,
				name:      f.Name,
				startLine: f.StartLine,
				endLine:   f.EndLine,
				loc:       f.Body.LOC,
				fp:        simHash(f.Body.Shingles),
			})
		}
	}
	return out
}

// simHash computes a 64-bit SimHash fingerprint over a multiset of
// uint64 shingle hashes. Each set bit of each shingle adds +1 to the
// corresponding axis, each unset bit subtracts 1; the final fingerprint
// is the sign vector. Two near-duplicate functions produce SimHashes
// with a small Hamming distance; unrelated functions produce roughly
// uncorrelated fingerprints (~32-bit Hamming distance).
func simHash(shingles []uint64) uint64 {
	var axes [64]int
	for _, s := range shingles {
		for i := range 64 {
			if s&(1<<uint(i)) != 0 {
				axes[i]++
			} else {
				axes[i]--
			}
		}
	}
	var fp uint64
	for i := range 64 {
		if axes[i] > 0 {
			fp |= 1 << uint(i)
		}
	}
	return fp
}

// clusterClones groups candidates whose SimHash fingerprints are within
// maxDistance via union-find. Bucketing by 16-bit prefix keeps the inner
// pairwise scan to ~O(n·k) instead of full O(n²).
func clusterClones(candidates []cloneCandidate, maxDistance int) []CloneCluster {
	parent := make([]int, len(candidates))
	rank := make([]int, len(candidates))
	for i := range parent {
		parent[i] = i
	}

	// Bucket by 16-bit prefixes — this guarantees that fingerprints which
	// differ only in low bits land in the same bucket. We rotate the
	// fingerprint by 16 bits four times to cover all prefix offsets, so
	// a near-duplicate pair lands in at least one shared bucket as long
	// as their distance ≤ 16 (more than enough for production maxDist=3).
	for shift := 0; shift < 64; shift += 16 {
		buckets := make(map[uint64][]int)
		for i, c := range candidates {
			rot := (c.fp >> uint(shift)) | (c.fp << uint(64-shift))
			key := rot >> 48 // top 16 bits
			buckets[key] = append(buckets[key], i)
		}
		for _, members := range buckets {
			if len(members) < 2 {
				continue
			}
			for i := range members {
				for j := i + 1; j < len(members); j++ {
					a, b := members[i], members[j]
					if hamming(candidates[a].fp, candidates[b].fp) <= maxDistance {
						unionFind(parent, rank, a, b)
					}
				}
			}
		}
	}

	clusters := make(map[int][]int)
	for i := range candidates {
		root := findRoot(parent, i)
		clusters[root] = append(clusters[root], i)
	}

	out := make([]CloneCluster, 0, len(clusters))
	for _, idxs := range clusters {
		if len(idxs) < 2 {
			continue
		}
		members := make([]CloneMember, len(idxs))
		clusterLOC := 0
		maxDist := 0
		for i, idx := range idxs {
			c := candidates[idx]
			members[i] = CloneMember{
				Path:      c.path,
				Name:      c.name,
				StartLine: c.startLine,
				EndLine:   c.endLine,
				LOC:       c.loc,
			}
			clusterLOC += c.loc
		}
		for i := range idxs {
			for j := i + 1; j < len(idxs); j++ {
				if d := hamming(candidates[idxs[i]].fp, candidates[idxs[j]].fp); d > maxDist {
					maxDist = d
				}
			}
		}
		sort.Slice(members, func(i, j int) bool {
			if members[i].Path != members[j].Path {
				return members[i].Path < members[j].Path
			}
			return members[i].StartLine < members[j].StartLine
		})
		out = append(out, CloneCluster{Members: members, LOC: clusterLOC, MaxDistance: maxDist})
	}
	return out
}

func hamming(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

func findRoot(parent []int, i int) int {
	for parent[i] != i {
		parent[i] = parent[parent[i]]
		i = parent[i]
	}
	return i
}

func unionFind(parent, rank []int, a, b int) {
	ra, rb := findRoot(parent, a), findRoot(parent, b)
	if ra == rb {
		return
	}
	switch {
	case rank[ra] < rank[rb]:
		parent[ra] = rb
	case rank[ra] > rank[rb]:
		parent[rb] = ra
	default:
		parent[rb] = ra
		rank[ra]++
	}
}
