# direct-key-handler

A buffered keyboard input handler for terminal applications with VT100/ANSI escape sequence support.

Available in both Node.js and Go.

## Features

- Buffered input prevents data loss during paste operations
- VT100/ANSI escape sequence parsing (arrow keys, function keys, modifiers)
- UTF-8/Unicode support including emoji
- Raw terminal mode handling

## Go

```go
import "github.com/phroun/direct-key-handler/keyboard"

handler := keyboard.NewDirectKeyboardHandler(nil)

for {
    key := handler.GetKey()
    fmt.Print(key + "\r\n")
    if key == "^C" {
        break
    }
}

handler.StopListening()
```

Build the sample app:

```bash
go build -o testkey ./cmd/testkey/
./testkey
```

## Node.js

```javascript
const { DirectKeyboardHandler } = require('./direct-keyboard-handler');

const handler = new DirectKeyboardHandler();

async function main() {
    let key = await handler.getKey();
    console.log(key);
    handler.stopListening();
}

main();
```

## Key Output Examples

| Input | Output |
|-------|--------|
| a | `a` |
| Ctrl+C | `^C` |
| Escape | `esc` |
| Up Arrow | `up` |
| Alt+x | `M-x` |
| Shift+Tab | `S-tab` |
| F1 | `F1` |
| Ctrl+Up | `^up` |

## License

MIT
