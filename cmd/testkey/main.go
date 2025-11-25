package main

import (
	"fmt"
	"os"

	"github.com/phroun/direct-key-handler/keyboard"
)

func main() {
	keyboardHandler := keyboard.NewDirectKeyboardHandler(func(message string) {
		fmt.Fprintln(os.Stderr, message)
	})

	key := ""
	z := 0
	for key != "^C" && z < 30 {
		key = keyboardHandler.GetKey()
		z++
		fmt.Println(key)
	}
	os.Exit(1)
}
