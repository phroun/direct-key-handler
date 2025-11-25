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

## Change Log

### 0.2.0
- Still available for Node.js, but also translated to Go.
- Initial public release

### 0.1.0
- Created using Node.js, tested mostly with iTerm2 and Cool Retro Terminal
  on MacOS.
