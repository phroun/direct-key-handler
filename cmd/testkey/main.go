package main

import (
	"fmt"
	"os"

	"github.com/phroun/direct-key-handler/keyboard"
)

func main() {
	keyboardHandler := keyboard.NewDirectKeyboardHandler(func(message string) {
		fmt.Fprint(os.Stderr, message+"\r\n")
	})

	key := ""
	z := 0
	for key != "^C" && z < 30 {
		key = keyboardHandler.GetKey()
		z++
		fmt.Print(key + "\r\n")
	}
	os.Exit(1)
}
