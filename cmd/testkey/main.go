package main

import (
	"fmt"
	"os"

	"github.com/phroun/direct-key-handler/keyboard"
)

func main() {
	handler := keyboard.New(keyboard.Options{
		InputReader: os.Stdin,
		EchoWriter:  nil, // No echo for raw key testing
	})

	if err := handler.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start: %v\n", err)
		os.Exit(1)
	}
	defer handler.Stop()

	fmt.Println("Press keys (Ctrl+C to exit):")

	for key := range handler.Keys {
		fmt.Printf("Key: %q\n", key)
		if key == "^C" {
			break
		}
	}
}
