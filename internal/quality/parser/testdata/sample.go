package sample

import (
	"fmt"

	bar "example.com/bar"
)

type Widget struct {
	N int
}

func (w Widget) Hello() string {
	fmt.Println("hi")
	return bar.Greet()
}

func MakeWidget() Widget {
	return Widget{N: 1}
}
