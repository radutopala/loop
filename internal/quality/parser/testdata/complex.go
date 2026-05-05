package complex

// Branchy exercises if/for/switch/select/&&/|| in Go.
func Branchy(a, b int, c bool) int {
	x := 0
	if a > 0 && b > 0 {
		for i := 0; i < a; i++ {
			if c || i > 5 {
				x += i
			}
		}
	} else {
		switch a {
		case 1:
			x = 1
		case 2:
			x = 2
		default:
			x = -1
		}
	}
	return x
}

// Trivial has no decision points; should report DecisionPoints=1.
func Trivial(n int) int {
	return n + 1
}

// Manyparams takes five parameters.
func Manyparams(a, b, c, d, e int) int {
	return a + b + c + d + e
}
