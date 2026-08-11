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
| Special keys | `Return`, `Enter`, `Tab`, `Space`, `Backspace`, `Escape` |
| Arrow keys | `Up`, `Down`, `Left`, `Right` |
| Navigation | `Home`, `End`, `PageUp`, `PageDown`, `Insert`, `Delete` |
| Function keys | `F1` through `F12` |
| Alt/Meta + key | `M-a`, `M-x`, `M-Enter` |
| Shift + key | `S-Tab`, `S-Up` |
| Ctrl + arrow | `C-Up`, `C-Left` |
| Combined modifiers | `S-M-a`, `C-S-Up` |

### Modifier Prefixes

Prefixes are emitted in one fixed order — the order below, which follows the
order macOS renders modifiers (⌃⌥⇧⌘) extended with the ones a Mac keyboard has
no cap for. A consumer that sorts before matching does not care, but one that
compares strings should not have to know which producer it is listening to.

| Prefix | Modifier | Kitty bit |
|--------|----------|-----------|
| `C-` | Control (for special keys) | 4 |
| `G-` | Glyph — AltGr / ISO_Level3_Shift (see below) | 256 (private) |
| `M-` | Meta, as induced by the PC Alt key | 2 |
| `m-` | Meta proper — a separate key most keyboards do not have | 32 |
| `S-` | Shift | 1 |
| `s-` | Super/Command | 8 |
| `H-` | Hyper | 16 |

Note: For letter keys with Ctrl, the `^X` notation is used (e.g., `^A` for Ctrl+A).

There is no `A-`. The PC Alt key induces Meta, and `M-` is what that is called
here — `m-` is the genuinely separate modifier, one of the four a Space Cadet
keyboard had its own key for.

Caps Lock (64) and Num Lock (128) are reported by the protocol but are
deliberately **not** turned into prefixes: folding them into a key name would
make every keystroke typed with Caps Lock on miss its binding.

## Key names

Every named key has a `Key` constant, and `defaultKeyNames` gives each one the
spelling above. An application that binds against a different vocabulary passes
its own table rather than translating afterwards:

```go
h := keyboard.New(keyboard.Options{
    KeyNames: map[keyboard.Key]string{
        keyboard.KeyEscape:    "esc",
        keyboard.KeyBackspace: "back",
        keyboard.KeyDelete:    "fdel",
        keyboard.KeyPageUp:    "pgup",
    },
})
```

Renaming applies through modifiers and event suffixes, so the override above
also covers `M-esc`, `S-pgup` and `pgup:Release`. Only the names you give are
changed; the rest keep their defaults. `Key.DefaultName()` and `AllKeys()` let
you enumerate the vocabulary — to build a settings screen, or to translate back
to the defaults at a boundary.

The full default set:

| Group | Names |
|-------|-------|
| Editing | `Escape`, `Tab`, `Space`, `Backspace`, `Return`, `Enter` |
| Navigation | `Insert`, `Delete`, `Home`, `End`, `PageUp`, `PageDown` |
| Arrows | `Up`, `Down`, `Left`, `Right` |
| Locks and system | `CapsLock`, `ScrollLock`, `NumLock`, `PrintScreen`, `Pause`, `Menu` |
| Function | `F1` … `F20` |

`Return` is the home-row key (byte 13); `Enter` is the keypad's, which only a
terminal speaking the Kitty protocol can report separately. They are two
physical keys and are reported as two — an application that wants them
interchangeable says so.

## The Glyph modifier

AltGr (ISO_Level3_Shift) composes a character, and terminals disagree about
whether to report the modifier alongside it — so a key that types `€` may be
bindable, or may be indistinguishable from someone typing `€` directly.

Glyph is a private Kitty modifier bit (value 256, sent as 257) a host can use to
say "this character was composed". A key event carrying it is reported as a `G-`
chord — `G-€` — instead of an anonymous typed character, so a keymap can claim
it while an application that ignores `G-` still inserts the glyph. Co-held
standard modifiers are preserved (`G-S-€`).

There is no option to turn this on: the host opts in by sending the bit, and a
terminal that never sends it is unaffected. The bit is private deliberately —
the protocol defines no standard one, and the implementations that do report
AltGr disagree about how.

## License

MIT

## Change Log

### 0.3.16

- **Hyper and Meta are decoded.** The kitty protocol reports eight modifier
  bits; two were dropped on the floor since the original Go translation, so a
  Hyper chord arrived indistinguishable from the unmodified key. Bit 16 is now
  `H-` (Hyper) and bit 32 is `m-` (Meta proper, as distinct from the `M-` that
  the PC Alt key induces).
- **Modifier prefixes are emitted in canonical order** — `C- G- M- m- S- s- H-`
  — rather than the order the bits happened to be tested in. A chord that was
  emitted `S-M-Left` is now `M-S-Left`. Consumers that sort before matching are
  unaffected; one that compares raw strings will see the change.
- Dropped `A-` from `namePrefixes`. Nothing ever emitted it, and the PC Alt key
  induces Meta, which is spelled `M-`.

### 0.3.14

- Added the `Key` enum and `Options.KeyNames`, so an application receives its
  own key vocabulary instead of translating one afterwards. Renaming applies
  through modifier prefixes and event suffixes. `Key.DefaultName()` and
  `AllKeys()` enumerate the defaults.
- **Return and Enter were swapped, and are now the right way round.** Byte 13
  (the home-row key) is `Return`; the keypad's key, which only the Kitty
  protocol reports separately, is `Enter`. Previously these were reversed.
- Line assembly resolves its keys by identity rather than spelling, so renaming
  a key no longer changes how the line editor behaves.

### 0.3.13

- Added the private Glyph modifier: a host that composes an AltGr /
  ISO_Level3_Shift character can report it as a bindable `G-` chord.

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
