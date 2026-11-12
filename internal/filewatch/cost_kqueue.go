//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package filewatch

// dirCost is what watching dir costs on kqueue: a descriptor for the
// directory and one for each entry in it.
func dirCost(dir string) int { return 1 + countEntries(dir, MaxCost) }
