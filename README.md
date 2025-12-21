# direct-key-handler

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

*A buffered keyboard input handler for terminal applications with VT100/ANSI escape sequence support.*
*If you use this, please support me on ko-fi:  [https://ko-fi.com/jeffday](https://ko-fi.com/F2F61JR2B4)*

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/F2F61JR2B4)

Available in both Node.js and Go.

## Features

- Buffered input prevents data loss during paste operations
- VT100/ANSI escape sequence parsing (arrow keys, function keys, modifiers)
- UTF-8/Unicode support including emoji
- Bracketed paste mode support
- Kitty keyboard protocol extensions
- Optional line assembly mode with editing
- Raw terminal mode handling

## Go

### Installation

```bash
go get github.com/phroun/direct-key-handler/keyboard
```

### Quick Start

```go
package main

import (
    "fmt"
    "os"

    "github.com/phroun/direct-key-handler/keyboard"
)

func main() {
    handler := keyboard.New(keyboard.Options{
        InputReader: os.Stdin,
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
```

### Options

```go
handler := keyboard.New(keyboard.Options{
    InputReader:    os.Stdin,      // Required: source of input bytes
    EchoWriter:     os.Stdout,     // Optional: echo typed chars (for line mode)
    KeyBufferSize:  64,            // Optional: Keys channel buffer (default: 64)
    LineBufferSize: 16,            // Optional: Lines channel buffer (default: 16)
    DebugFn:        func(s string) { log.Println(s) },  // Optional
})
```

### Line Mode

For reading complete lines with basic editing:

```go
handler.SetLineMode(true)
handler.SetEchoWriter(os.Stdout)

line := <-handler.Lines  // Blocks until Enter is pressed
fmt.Printf("You typed: %s\n", string(line))

handler.SetLineMode(false)
```

### Callbacks

```go
handler.OnKey = func(key string) {
    log.Printf("Key: %s", key)
}

handler.OnLine = func(line []byte) {
    log.Printf("Line: %s", string(line))
}

handler.OnPaste = func(content []byte) {
    log.Printf("Pasted %d bytes", len(content))
}
```

Build the sample app:

```bash
go build -o testkey ./cmd/testkey/
./testkey
```

## Node.js

### Quick Start

```javascript
const { DirectKeyboardHandler } = require('./keyboard');

async function main() {
    const handler = new DirectKeyboardHandler({
        // Options (all optional)
        // inputStream: process.stdin,
        // outputStream: process.stdout,
    });

    await handler.start();

    console.log('Press keys (Ctrl+C to exit):');

    handler.onKey((key) => {
        console.log(`Key: ${key}`);
        if (key === '^C') {
            handler.stop();
            process.exit(0);
        }
    });
}

main();
```

### Line Mode

```javascript
handler.setLineMode(true);

handler.onLine((line) => {
    console.log(`You typed: ${line}`);
});
```

## Key Output Examples

| Input | Output |
|-------|--------|
| Regular characters | `a`, `Z`, `5`, `!` |
| Control keys | `^A`, `^C`, `^Z` |
| Special keys | `Enter`, `Tab`, `Backspace`, `Escape` |
| Arrow keys | `Up`, `Down`, `Left`, `Right` |
| Navigation | `Home`, `End`, `PageUp`, `PageDown`, `Insert`, `Delete` |
| Function keys | `F1` through `F12` |
| Alt/Meta + key | `M-a`, `M-x`, `M-Enter` |
| Shift + key | `S-Tab`, `S-Up` |
| Ctrl + arrow | `C-Up`, `C-Left` |
| Combined modifiers | `S-M-a`, `C-S-Up` |

### Modifier Prefixes

| Prefix | Modifier |
|--------|----------|
| `M-` | Alt/Meta |
| `S-` | Shift |
| `C-` | Control (for special keys) |
| `s-` | Super/Command |

Note: For letter keys with Ctrl, the `^X` notation is used (e.g., `^A` for Ctrl+A).

## License

MIT

## Change Log

### 0.3.0
- Complete API redesign for both Go and Node.js
- Go: New `keyboard.New(Options)` constructor with channel-based key delivery
- Go: Added `SetLineMode()` for line assembly with editing
- Go: Added optional callbacks (`OnKey`, `OnLine`, `OnPaste`)
- Added bracketed paste mode support
- Added Kitty keyboard protocol support
- Improved modifier key handling

### 0.2.0
- Still available for Node.js, but also translated to Go.
- Initial public release

### 0.1.0
- Created using Node.js, tested mostly with iTerm2 and Cool Retro Terminal
  on MacOS.
