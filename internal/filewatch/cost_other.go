//go:build !(darwin || freebsd || netbsd || openbsd || dragonfly)

package filewatch

// dirCost is what watching a directory costs here: one watch, whatever it holds.
func dirCost(string) int { return 1 }
