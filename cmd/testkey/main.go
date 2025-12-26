package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/phroun/direct-key-handler/keyboard"
)

// Kitty keyboard protocol escape sequences
const (
	// Enable Kitty keyboard protocol with all flags
	// Flags: 1=disambiguate escape codes, 2=report event types, 4=report alternate keys, 8=report all keys as escape codes, 16=report associated text
	kittyEnable  = "\x1b[>1u"      // Basic mode (disambiguate escape codes)
	kittyEnhance = "\x1b[>31u"     // Full mode (all flags)
	kittyDisable = "\x1b[<u"       // Pop/disable

	// Mouse reporting
	mouseEnableSGR    = "\x1b[?1006h" // SGR mouse mode
	mouseEnableBasic  = "\x1b[?1000h" // Basic mouse tracking
	mouseEnableMotion = "\x1b[?1002h" // Button event + motion tracking
	mouseDisable      = "\x1b[?1000l\x1b[?1002l\x1b[?1006l"
)

func main() {
	kittyMode := flag.Bool("kitty", false, "Enable Kitty keyboard protocol")
	kittyFull := flag.Bool("kitty-full", false, "Enable Kitty keyboard protocol with all flags")
	mouseMode := flag.Bool("mouse", false, "Enable mouse reporting (SGR mode)")
	flag.Parse()

	handler := keyboard.New(keyboard.Options{
		InputReader: os.Stdin,
		EchoWriter:  nil, // No echo for raw key testing
	})

	// Enable terminal modes before starting
	if *kittyMode || *kittyFull {
		if *kittyFull {
			fmt.Print(kittyEnhance)
			fmt.Println("Kitty keyboard protocol enabled (full mode - all flags)")
		} else {
			fmt.Print(kittyEnable)
			fmt.Println("Kitty keyboard protocol enabled (basic mode)")
		}
	}

	if *mouseMode {
		fmt.Print(mouseEnableBasic + mouseEnableMotion + mouseEnableSGR)
		fmt.Println("Mouse reporting enabled (SGR mode)")
	}

	if err := handler.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start: %v\n", err)
		// Clean up terminal modes on error
		if *kittyMode || *kittyFull {
			fmt.Print(kittyDisable)
		}
		if *mouseMode {
			fmt.Print(mouseDisable)
		}
		os.Exit(1)
	}

	// Ensure cleanup on exit
	defer func() {
		handler.Stop()
		if *kittyMode || *kittyFull {
			fmt.Print(kittyDisable)
		}
		if *mouseMode {
			fmt.Print(mouseDisable)
		}
	}()

	fmt.Println("Press keys (Ctrl+C to exit):")

	for key := range handler.Keys {
		fmt.Printf("Key: %q\n", key)
		if key == "^C" {
			break
		}
	}
}
