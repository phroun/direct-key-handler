// Package keyboard provides raw keyboard input handling with escape sequence parsing.
// It handles VT100/ANSI escape sequences, UTF-8 characters, bracketed paste,
// and line assembly for terminal input.
package keyboard

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// PasteChunk represents an incremental chunk of bracketed paste content
type PasteChunk struct {
	Content []byte // The chunk content
	IsFinal bool   // True if this is the final chunk
}

// Handler handles raw keyboard input, parsing escape sequences
// and providing both key events and line assembly.
type Handler struct {
	mu sync.Mutex

	// Input source
	inputReader io.Reader     // Raw input source (any io.Reader)
	rawBytes    chan []byte   // Channel for raw byte chunks
	stopChan    chan struct{} // Signal to stop reading

	// Output channels (plain Go channels)
	Keys  chan string  // Parsed key events ("a", "M-a", "F1", etc.)
	Lines chan []byte  // Assembled lines

	// Callbacks (optional, called in addition to channel sends)
	OnKey        func(key string)     // Called on each key event
	OnLine       func(line []byte)    // Called on each completed line
	OnPaste      func(content []byte) // Called on bracketed paste content (complete)
	OnPasteChunk func(chunk PasteChunk) // Called on incremental paste chunks

	// Terminal handling (only used if input is os.Stdin and is a terminal)
	terminalFd        int         // File descriptor if we're managing terminal mode
	originalTermState *term.State // Original state to restore
	managesTerminal   bool        // True if we put terminal in raw mode

	// State
	running        bool
	inLineReadMode bool // True when line assembly is active

	// Line assembly state - stores raw bytes for proper I/O semantics
	currentLine []byte
	// Track UTF-8 character boundaries for backspace (number of bytes per char)
	charByteLengths []int

	// Escape sequence buffer
	escBuffer []byte
	inEscape  bool

	// UTF-8 multi-byte character buffer
	utf8Buffer    []byte
	utf8Remaining int // bytes remaining to complete current UTF-8 char

	// Bracketed paste state
	inPaste          bool
	pasteBuffer      []byte // Buffer for detecting end sequence (kept small for chunking)
	fullPasteContent []byte // Accumulator for full paste content (for OnPaste callback)
	pasteChunkSize   int    // Size of chunks to emit during paste (default: 1024)

	// macOS Option key decoding
	decodeMacOSOption bool // When true, decode macOS Option+key chars to M-key notation

	// Echo output (where to echo typed characters)
	echoWriter io.Writer

	// Debug callback (optional)
	debugFn func(string)
}

// Options configures the Handler
type Options struct {
	// InputReader is the source of raw bytes (required)
	InputReader io.Reader

	// EchoWriter is where to echo typed characters during line mode (optional)
	EchoWriter io.Writer

	// KeyBufferSize is the size of the Keys channel buffer (default: 64)
	KeyBufferSize int

	// LineBufferSize is the size of the Lines channel buffer (default: 16)
	LineBufferSize int

	// PasteChunkSize is the size of chunks emitted during bracketed paste (default: 1024)
	// Only used when OnPasteChunk callback is set
	PasteChunkSize int

	// DecodeMacOSOption enables decoding of macOS Option+key Unicode characters
	// to M-key notation (e.g., ∂ → M-d, Ø → M-O). Default: true on Darwin, false otherwise
	DecodeMacOSOption *bool

	// DebugFn is called with debug messages (optional)
	DebugFn func(string)

	// ManageTerminal controls whether to put stdin in raw mode.
	// Only applies if InputReader is os.Stdin and is a terminal.
	// Default: true
	ManageTerminal *bool
}

// New creates a new keyboard Handler.
func New(opts Options) *Handler {
	keyBufSize := opts.KeyBufferSize
	if keyBufSize <= 0 {
		keyBufSize = 64
	}
	lineBufSize := opts.LineBufferSize
	if lineBufSize <= 0 {
		lineBufSize = 16
	}
	pasteChunkSize := opts.PasteChunkSize
	if pasteChunkSize <= 0 {
		pasteChunkSize = DefaultPasteChunkSize
	}

	manageTerminal := true
	if opts.ManageTerminal != nil {
		manageTerminal = *opts.ManageTerminal
	}

	// Default to true on Darwin (macOS), false otherwise
	decodeMacOSOption := runtime.GOOS == "darwin"
	if opts.DecodeMacOSOption != nil {
		decodeMacOSOption = *opts.DecodeMacOSOption
	}

	h := &Handler{
		inputReader:       opts.InputReader,
		rawBytes:          make(chan []byte, 64),
		stopChan:          make(chan struct{}),
		Keys:              make(chan string, keyBufSize),
		Lines:             make(chan []byte, lineBufSize),
		echoWriter:        opts.EchoWriter,
		debugFn:           opts.DebugFn,
		terminalFd:        -1,
		pasteChunkSize:    pasteChunkSize,
		decodeMacOSOption: decodeMacOSOption,
	}

	// Check if input is a terminal file descriptor
	if manageTerminal {
		if f, ok := opts.InputReader.(interface{ Fd() uintptr }); ok {
			fd := int(f.Fd())
			if term.IsTerminal(fd) {
				h.terminalFd = fd
				h.managesTerminal = true
			}
		}
	}

	return h
}

// Start begins reading from input and processing keys.
func (h *Handler) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return fmt.Errorf("handler already running")
	}

	// Put terminal in raw mode only if we're managing it
	if h.managesTerminal {
		state, err := term.MakeRaw(h.terminalFd)
		if err != nil {
			return fmt.Errorf("failed to enable raw mode: %w", err)
		}
		h.originalTermState = state
		h.debug("Terminal set to raw mode")
	}

	h.running = true

	// Start the read goroutine
	go h.readLoop()

	// Start the processing goroutine
	go h.processLoop()

	h.debug("Handler started")
	return nil
}

// Stop stops reading and restores terminal state.
func (h *Handler) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return nil
	}

	// Signal stop
	close(h.stopChan)
	h.running = false

	// Restore terminal state if we changed it
	if h.managesTerminal && h.originalTermState != nil {
		if err := term.Restore(h.terminalFd, h.originalTermState); err != nil {
			return fmt.Errorf("failed to restore terminal: %w", err)
		}
		h.originalTermState = nil
		h.debug("Terminal restored to original mode")
	}

	h.debug("Handler stopped")
	return nil
}

// SetLineMode enables or disables line assembly mode.
// When enabled, keys go to line assembly and completed lines are sent to Lines channel.
// When disabled, all keys go directly to Keys channel.
func (h *Handler) SetLineMode(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inLineReadMode = enabled
	if enabled {
		h.currentLine = nil
		h.charByteLengths = nil
	}
}

// IsLineMode returns true if line assembly mode is active.
func (h *Handler) IsLineMode() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inLineReadMode
}

// SetEchoWriter sets the writer for echoing typed characters.
func (h *Handler) SetEchoWriter(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.echoWriter = w
}

// IsRunning returns true if the handler is currently running.
func (h *Handler) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// ManagesTerminal returns true if this handler is managing terminal raw mode.
func (h *Handler) ManagesTerminal() bool {
	return h.managesTerminal
}

// SetDecodeMacOSOption enables or disables decoding of macOS Option+key
// Unicode characters to M-key notation (e.g., ∂ → M-d).
func (h *Handler) SetDecodeMacOSOption(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.decodeMacOSOption = enabled
}

// DecodeMacOSOption returns true if macOS Option character decoding is enabled.
func (h *Handler) DecodeMacOSOption() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.decodeMacOSOption
}

// Escape sequence bindings - maps escape sequences to key names
var escBindings = map[string]string{
	// Arrow keys
	"\x1b[A": "Up",
	"\x1b[B": "Down",
	"\x1b[C": "Right",
	"\x1b[D": "Left",

	// Arrow keys with modifiers
	"\x1b[1;2A": "S-Up",
	"\x1b[1;2B": "S-Down",
	"\x1b[1;2C": "S-Right",
	"\x1b[1;2D": "S-Left",
	"\x1b[1;3A": "M-Up",
	"\x1b[1;3B": "M-Down",
	"\x1b[1;3C": "M-Right",
	"\x1b[1;3D": "M-Left",
	"\x1b[1;5A": "C-Up",
	"\x1b[1;5B": "C-Down",
	"\x1b[1;5C": "C-Right",
	"\x1b[1;5D": "C-Left",

	// Function keys
	"\x1bOP": "F1",
	"\x1bOQ": "F2",
	"\x1bOR": "F3",
	"\x1bOS": "F4",
	"\x1b[15~": "F5",
	"\x1b[17~": "F6",
	"\x1b[18~": "F7",
	"\x1b[19~": "F8",
	"\x1b[20~": "F9",
	"\x1b[21~": "F10",
	"\x1b[23~": "F11",
	"\x1b[24~": "F12",

	// Navigation keys
	"\x1b[H": "Home",
	"\x1b[F": "End",
	"\x1b[1~": "Home",
	"\x1b[4~": "End",
	"\x1b[2~": "Insert",
	"\x1b[3~": "Delete",
	"\x1b[5~": "PageUp",
	"\x1b[6~": "PageDown",

	// Alternate arrow key sequences (some terminals)
	"\x1bOA": "Up",
	"\x1bOB": "Down",
	"\x1bOC": "Right",
	"\x1bOD": "Left",
}

// Control key names
var controlKeys = map[byte]string{
	0:   "^@", // Ctrl-Space or Ctrl-@
	1:   "^A",
	2:   "^B",
	3:   "^C",
	4:   "^D",
	5:   "^E",
	6:   "^F",
	7:   "^G",
	8:   "Backspace", // Ctrl-H
	9:   "Tab",       // Ctrl-I
	10:  "^J",        // Ctrl-J (LF) - distinct from Enter
	11:  "^K",
	12:  "^L",
	13:  "Enter",     // Ctrl-M (CR)
	14:  "^N",
	15:  "^O",
	16:  "^P",
	17:  "^Q",
	18:  "^R",
	19:  "^S",
	20:  "^T",
	21:  "^U",
	22:  "^V",
	23:  "^W",
	24:  "^X",
	25:  "^Y",
	26:  "^Z",
	27:  "Escape", // Escape itself (handled specially)
	28:  "^\\",
	29:  "^]",
	30:  "^^",
	31:  "^_",
	127: "Backspace", // DEL
}

// macOSOptionChars maps Unicode characters produced by macOS Option+key to M-key notation
// This is for US keyboard layout
var macOSOptionChars = map[rune]string{
	// Lowercase Option+letter
	'å': "M-a", // Option+a
	'∫': "M-b", // Option+b
	'ç': "M-c", // Option+c
	'∂': "M-d", // Option+d
	'´': "M-e", // Option+e (dead key - acute accent)
	'ƒ': "M-f", // Option+f
	'©': "M-g", // Option+g
	'˙': "M-h", // Option+h
	'ˆ': "M-i", // Option+i (dead key - circumflex)
	'∆': "M-j", // Option+j
	'˚': "M-k", // Option+k
	'¬': "M-l", // Option+l
	'µ': "M-m", // Option+m
	'˜': "M-n", // Option+n (dead key - tilde)
	'ø': "M-o", // Option+o
	'π': "M-p", // Option+p
	'œ': "M-q", // Option+q
	'®': "M-r", // Option+r
	'ß': "M-s", // Option+s
	'†': "M-t", // Option+t
	'¨': "M-u", // Option+u (dead key - diaeresis)
	'√': "M-v", // Option+v
	'∑': "M-w", // Option+w
	'≈': "M-x", // Option+x
	'¥': "M-y", // Option+y
	'Ω': "M-z", // Option+z

	// Uppercase Option+Shift+letter (use M-X for uppercase, not M-S-x)
	'Å': "M-A", // Option+Shift+a
	'ı': "M-B", // Option+Shift+b
	'Ç': "M-C", // Option+Shift+c
	'Î': "M-D", // Option+Shift+d
	// Option+Shift+E produces ´ (same as Option+e) - handled above
	'Ï': "M-F", // Option+Shift+f
	'˝': "M-G", // Option+Shift+g
	'Ó': "M-H", // Option+Shift+h
	// Option+Shift+I produces ˆ (same as Option+i) - handled above
	'Ô': "M-J", // Option+Shift+j
	'\uF8FF': "M-K", // Option+Shift+k (Apple logo, private use area)
	'Ò': "M-L", // Option+Shift+l
	'Â': "M-M", // Option+Shift+m
	// Option+Shift+N produces ˜ (same as Option+n) - handled above
	'Ø': "M-O", // Option+Shift+o
	'∏': "M-P", // Option+Shift+p
	'Œ': "M-Q", // Option+Shift+q
	'‰': "M-R", // Option+Shift+r
	'Í': "M-S", // Option+Shift+s
	'ˇ': "M-T", // Option+Shift+t
	// Option+Shift+U produces ¨ (same as Option+u) - handled above
	'◊': "M-V", // Option+Shift+v
	'„': "M-W", // Option+Shift+w
	'˛': "M-X", // Option+Shift+x
	'Á': "M-Y", // Option+Shift+y
	'¸': "M-Z", // Option+Shift+z

	// Option+number
	'¡': "M-1", // Option+1
	'™': "M-2", // Option+2
	'£': "M-3", // Option+3
	'¢': "M-4", // Option+4
	'∞': "M-5", // Option+5
	'§': "M-6", // Option+6
	'¶': "M-7", // Option+7
	'•': "M-8", // Option+8
	'ª': "M-9", // Option+9
	'º': "M-0", // Option+0

	// Option+symbol
	'–': "M--",  // Option+minus (en dash)
	'≠': "M-=",  // Option+equals
	'"': "M-[",  // Option+[ (left double quote)
	'\u2019': "M-]",  // Option+] (right single quote)
	'«': "M-\\", // Option+backslash
	'…': "M-;",  // Option+semicolon
	'æ': "M-'",  // Option+quote
	'≤': "M-,",  // Option+comma
	'≥': "M-.",  // Option+period
	'÷': "M-/",  // Option+slash
	'`': "M-`",  // Option+backtick (same as backtick on some layouts)
}

// readLoop continuously reads raw bytes from input
func (h *Handler) readLoop() {
	buf := make([]byte, 256)
	for {
		select {
		case <-h.stopChan:
			return
		default:
			n, err := h.inputReader.Read(buf)
			if err != nil {
				h.debug(fmt.Sprintf("Read error: %v", err))
				return
			}
			if n > 0 {
				// Make a copy to send
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case h.rawBytes <- data:
				case <-h.stopChan:
					return
				}
			}
		}
	}
}

// processLoop processes raw bytes into key events
func (h *Handler) processLoop() {
	escTimeout := time.NewTimer(0)
	if !escTimeout.Stop() {
		<-escTimeout.C
	}

	for {
		select {
		case <-h.stopChan:
			return

		case data := <-h.rawBytes:
			for _, b := range data {
				h.processByte(b, escTimeout)
			}

		case <-escTimeout.C:
			// Escape sequence timeout - try Alt sequence parsing before giving up
			if h.inEscape && len(h.escBuffer) > 0 {
				seq := string(h.escBuffer)
				// Try Alt+key parsing (ESC followed by character)
				if key, ok := h.parseAltSequence(seq); ok {
					h.emitKey(key)
					h.escBuffer = nil
					h.inEscape = false
				} else {
					h.emitEscapeBuffer()
				}
			}
		}
	}
}

// Bracketed paste sequences
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// pasteEndBufferSize is the number of bytes to keep buffered during paste
// to avoid splitting the end sequence (\x1b[201~ is 6 bytes, we buffer 7 to be safe)
const pasteEndBufferSize = 7

// DefaultPasteChunkSize is the default size for paste chunks (1KB)
const DefaultPasteChunkSize = 1024

// processByte handles a single byte of input
func (h *Handler) processByte(b byte, escTimeout *time.Timer) {
	// Handle bracketed paste mode
	if h.inPaste {
		h.pasteBuffer = append(h.pasteBuffer, b)
		h.fullPasteContent = append(h.fullPasteContent, b)

		// Check if paste buffer ends with the end sequence
		if len(h.pasteBuffer) >= len(bracketedPasteEnd) {
			tail := string(h.pasteBuffer[len(h.pasteBuffer)-len(bracketedPasteEnd):])
			if tail == bracketedPasteEnd {
				// End of paste - extract remaining content (without the end sequence)
				remainingContent := h.pasteBuffer[:len(h.pasteBuffer)-len(bracketedPasteEnd)]
				// Full content is everything accumulated minus the end sequence
				fullContent := h.fullPasteContent[:len(h.fullPasteContent)-len(bracketedPasteEnd)]
				h.inPaste = false
				h.pasteBuffer = nil
				h.fullPasteContent = nil
				h.debug(fmt.Sprintf("Paste end, %d bytes", len(fullContent)))
				// Emit final chunk if callback is set (only the remaining buffered content)
				if h.OnPasteChunk != nil {
					h.OnPasteChunk(PasteChunk{Content: remainingContent, IsFinal: true})
				}
				// emitPaste receives full content for OnPaste callback and key emission
				h.emitPaste(fullContent)
				return
			}
		}

		// Emit incremental chunks when we have enough data
		// We emit when buffer >= chunkSize + pasteEndBufferSize (to keep 7 bytes for end detection)
		if h.OnPasteChunk != nil && len(h.pasteBuffer) >= h.pasteChunkSize+pasteEndBufferSize {
			// Emit a full chunk, keeping pasteEndBufferSize bytes buffered
			chunk := make([]byte, h.pasteChunkSize)
			copy(chunk, h.pasteBuffer[:h.pasteChunkSize])
			h.pasteBuffer = h.pasteBuffer[h.pasteChunkSize:]
			h.OnPasteChunk(PasteChunk{Content: chunk, IsFinal: false})
		}
		return
	}

	if h.inEscape {
		h.escBuffer = append(h.escBuffer, b)

		// Check if we have a complete escape sequence
		seq := string(h.escBuffer)

		// Check for bracketed paste start
		if seq == bracketedPasteStart {
			h.debug("Bracketed paste start detected")
			h.inEscape = false
			h.escBuffer = nil
			h.inPaste = true
			h.pasteBuffer = nil
			h.fullPasteContent = nil
			escTimeout.Stop()
			return
		}

		if key, ok := escBindings[seq]; ok {
			h.emitKey(key)
			h.escBuffer = nil
			h.inEscape = false
			escTimeout.Stop()
			return
		}

		// Check if this could be a prefix of a valid sequence
		if h.couldBeEscapePrefix(seq) {
			// Reset timeout - wait for more bytes
			escTimeout.Reset(50 * time.Millisecond)
			return
		}

		// Try dynamic parsing for CSI sequences with modifiers
		if key, ok := h.parseModifiedCSI(seq); ok {
			// Mouse events return "" but emit keys internally
			if key != "" {
				h.emitKey(key)
			}
			h.escBuffer = nil
			h.inEscape = false
			escTimeout.Stop()
			return
		}

		// Try Alt+key parsing (ESC followed by character)
		if key, ok := h.parseAltSequence(seq); ok {
			h.emitKey(key)
			h.escBuffer = nil
			h.inEscape = false
			escTimeout.Stop()
			return
		}

		// Not a valid sequence - emit as individual keys
		h.emitEscapeBuffer()
		return
	}

	// Check for escape start
	if b == 0x1b {
		h.inEscape = true
		h.escBuffer = []byte{b}
		escTimeout.Reset(50 * time.Millisecond)
		return
	}

	// Handle control characters
	if b < 32 || b == 127 {
		if key, ok := controlKeys[b]; ok {
			h.emitKey(key)
		} else {
			h.emitKey(fmt.Sprintf("^%c", b+64))
		}
		return
	}

	// Regular printable character or start of UTF-8 sequence
	if b < 128 {
		h.emitKey(string(b))
		return
	}

	// UTF-8 multi-byte character handling
	// Check if we're continuing an existing UTF-8 sequence
	if h.utf8Remaining > 0 {
		// Continuation byte should be 10xxxxxx (0x80-0xBF)
		if b >= 0x80 && b <= 0xBF {
			h.utf8Buffer = append(h.utf8Buffer, b)
			h.utf8Remaining--
			if h.utf8Remaining == 0 {
				// Complete UTF-8 sequence - emit the character
				h.emitKey(string(h.utf8Buffer))
				h.utf8Buffer = nil
			}
		} else {
			// Invalid continuation - emit buffer as-is and reset
			for _, bb := range h.utf8Buffer {
				h.emitKey(string(rune(bb)))
			}
			h.utf8Buffer = nil
			h.utf8Remaining = 0
			// Process this byte as a new sequence
			h.processByte(b, escTimeout)
		}
		return
	}

	// Start of new UTF-8 sequence - determine length from lead byte
	if b >= 0xC0 && b <= 0xDF {
		// 2-byte sequence: 110xxxxx
		h.utf8Buffer = []byte{b}
		h.utf8Remaining = 1
	} else if b >= 0xE0 && b <= 0xEF {
		// 3-byte sequence: 1110xxxx
		h.utf8Buffer = []byte{b}
		h.utf8Remaining = 2
	} else if b >= 0xF0 && b <= 0xF7 {
		// 4-byte sequence: 11110xxx
		h.utf8Buffer = []byte{b}
		h.utf8Remaining = 3
	} else {
		// Invalid UTF-8 lead byte or bare continuation byte - emit as-is
		h.emitKey(string(rune(b)))
	}
}

// couldBeEscapePrefix checks if seq could be a prefix of a valid escape sequence
func (h *Handler) couldBeEscapePrefix(seq string) bool {
	for key := range escBindings {
		if len(seq) < len(key) && key[:len(seq)] == seq {
			return true
		}
	}

	// macOS Option+key sends ESC ESC [ X - wait for the full sequence
	if len(seq) >= 2 && seq[0] == 0x1b && seq[1] == 0x1b {
		// ESC ESC - could be start of macOS Option+arrow
		if len(seq) == 2 {
			return true // Wait for more
		}
		// ESC ESC [ - wait for arrow key (need 4 chars total: ESC ESC [ X)
		if seq[2] == '[' {
			if len(seq) < 4 {
				return true // Still waiting for the arrow key letter
			}
			// Length >= 4, check if terminated with A/B/C/D/H/F
			last := seq[len(seq)-1]
			if last >= 0x40 && last <= 0x7e {
				return false // Terminated
			}
			return true // Still in progress (shouldn't happen for this pattern)
		}
	}

	// Also allow CSI sequences in progress: ESC [ ...
	if len(seq) >= 2 && seq[0] == 0x1b && seq[1] == '[' {
		body := seq[2:]

		// SGR mouse: ESC [ < ... M/m - wait for M or m terminator
		if len(body) >= 1 && body[0] == '<' {
			last := body[len(body)-1]
			if last == 'M' || last == 'm' {
				return false // Terminated
			}
			return true // Still waiting for M/m
		}

		// X10 mouse: ESC [ M Cb Cx Cy - need exactly 3 bytes after M
		if len(body) >= 1 && body[0] == 'M' {
			if len(body) < 4 {
				return true // Need more bytes
			}
			return false // Got all 4 bytes (M + 3 data bytes)
		}

		// Regular CSI sequence - wait for terminator
		// Need at least 3 chars (ESC [ <final>) before checking termination
		if len(seq) < 3 {
			return true // Still waiting for parameters/final byte
		}
		last := seq[len(seq)-1]
		if last >= 0x40 && last <= 0x7e {
			return false // Terminated
		}
		return true // Still in progress
	}
	return false
}

// emitEscapeBuffer emits the escape buffer as individual keys
func (h *Handler) emitEscapeBuffer() {
	// First byte is ESC
	h.emitKey("Escape")
	// Remaining bytes as regular characters
	for _, b := range h.escBuffer[1:] {
		if b < 32 || b == 127 {
			if key, ok := controlKeys[b]; ok {
				h.emitKey(key)
			}
		} else {
			h.emitKey(string(b))
		}
	}
	h.escBuffer = nil
	h.inEscape = false
}

// emitKey sends a key event to either the Keys channel or line assembly
func (h *Handler) emitKey(key string) {
	// Decode macOS Option characters if enabled
	h.mu.Lock()
	decodeMacOS := h.decodeMacOSOption
	h.mu.Unlock()

	if decodeMacOS && len(key) > 0 {
		r, size := utf8.DecodeRuneInString(key)
		if size == len(key) && r != utf8.RuneError {
			if decoded, ok := macOSOptionChars[r]; ok {
				key = decoded
			}
		}
	}

	h.debug(fmt.Sprintf("Key: %q", key))

	// Call callback if set
	if h.OnKey != nil {
		h.OnKey(key)
	}

	// Check if we're in line read mode
	h.mu.Lock()
	inLineMode := h.inLineReadMode
	h.mu.Unlock()

	if inLineMode {
		// In line read mode: keys go to line assembly
		h.handleLineAssembly(key)
	} else {
		// Normal mode: keys go to Keys channel
		select {
		case h.Keys <- key:
			// Sent successfully
		default:
			// Buffer full - drop oldest key to make room
			select {
			case <-h.Keys:
			default:
			}
			// Try again
			select {
			case h.Keys <- key:
			default:
				// Still can't send, just drop this key
			}
		}
	}
}

// emitPaste handles bracketed paste content
func (h *Handler) emitPaste(content []byte) {
	// Call callback if set
	if h.OnPaste != nil {
		h.OnPaste(content)
	}

	h.mu.Lock()
	inLineMode := h.inLineReadMode
	h.mu.Unlock()

	if inLineMode {
		// In line read mode: add pasted content directly to line buffer
		h.handlePasteLineAssembly(content)
	} else {
		// Normal mode: emit each character as individual key events
		for len(content) > 0 {
			r, size := utf8.DecodeRune(content)
			if r == utf8.RuneError && size == 1 {
				content = content[1:]
				continue
			}
			// Handle special characters
			if r == '\r' {
				h.emitKey("Enter")
			} else if r == '\n' {
				h.emitKey("^J")
			} else if r == '\t' {
				h.emitKey("Tab")
			} else if r == 0x7f {
				h.emitKey("Backspace")
			} else if r < 32 {
				if key, ok := controlKeys[byte(r)]; ok {
					h.emitKey(key)
				}
			} else {
				h.emitKey(string(r))
			}
			content = content[size:]
		}
	}
}

// handlePasteLineAssembly adds pasted content to the line buffer
func (h *Handler) handlePasteLineAssembly(content []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.inLineReadMode {
		return
	}

	// Process pasted content byte by byte, handling special characters
	for len(content) > 0 {
		r, size := utf8.DecodeRune(content)
		if r == utf8.RuneError && size == 1 {
			content = content[1:]
			continue
		}

		if r == '\r' || r == '\n' {
			// Newline in paste - submit the current line
			lineBytes := make([]byte, len(h.currentLine))
			copy(lineBytes, h.currentLine)
			h.currentLine = nil
			h.charByteLengths = nil
			echoWriter := h.echoWriter
			h.mu.Unlock()

			// Send line
			select {
			case h.Lines <- lineBytes:
			default:
				select {
				case <-h.Lines:
				default:
				}
				h.Lines <- lineBytes
			}

			// Call callback
			if h.OnLine != nil {
				h.OnLine(lineBytes)
			}

			// Echo newline
			if echoWriter != nil {
				echoWriter.Write([]byte("\r\n"))
			}

			h.mu.Lock()
			// Skip remaining content after newline (single-line read)
			return
		} else if r >= 32 || r == '\t' {
			// Printable character or tab - add to line
			charBytes := content[:size]
			h.currentLine = append(h.currentLine, charBytes...)
			h.charByteLengths = append(h.charByteLengths, size)
			// Echo
			h.echoLocked(string(r))
		}

		content = content[size:]
	}
}

// handleLineAssembly processes a key for line assembly
func (h *Handler) handleLineAssembly(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.inLineReadMode {
		return
	}

	switch key {
	case "Enter":
		// Emit the completed line as raw bytes
		lineBytes := make([]byte, len(h.currentLine))
		copy(lineBytes, h.currentLine)
		h.currentLine = nil
		h.charByteLengths = nil
		echoWriter := h.echoWriter
		h.mu.Unlock()

		// Send to Lines channel
		select {
		case h.Lines <- lineBytes:
		default:
			select {
			case <-h.Lines:
			default:
			}
			h.Lines <- lineBytes
		}

		// Call callback
		if h.OnLine != nil {
			h.OnLine(lineBytes)
		}

		// Echo newline
		if echoWriter != nil {
			echoWriter.Write([]byte("\r\n"))
		}

		h.mu.Lock() // Re-acquire for deferred unlock
		return

	case "Backspace":
		if len(h.charByteLengths) > 0 {
			lastCharLen := h.charByteLengths[len(h.charByteLengths)-1]
			h.currentLine = h.currentLine[:len(h.currentLine)-lastCharLen]
			h.charByteLengths = h.charByteLengths[:len(h.charByteLengths)-1]
			h.echoLocked("\b \b")
		}

	case "^U":
		// Clear line
		for range h.charByteLengths {
			h.echoLocked("\b \b")
		}
		h.currentLine = nil
		h.charByteLengths = nil

	case "^C":
		// Interrupt - emit empty line
		h.echoLocked("^C\r\n")
		h.currentLine = nil
		h.charByteLengths = nil
		h.mu.Unlock()

		select {
		case h.Lines <- []byte{}:
		default:
		}

		if h.OnLine != nil {
			h.OnLine([]byte{})
		}

		h.mu.Lock()
		return

	default:
		// Check if it's a printable character
		if len(key) > 0 {
			r, _ := utf8.DecodeRuneInString(key)
			if r != utf8.RuneError && len(key) == utf8.RuneLen(r) && r >= 32 {
				h.currentLine = append(h.currentLine, []byte(key)...)
				h.charByteLengths = append(h.charByteLengths, len(key))
				h.echoLocked(key)
			}
		}
	}
}

// echoLocked writes to echo output - call only while holding h.mu
func (h *Handler) echoLocked(s string) {
	if h.echoWriter != nil {
		h.echoWriter.Write([]byte(s))
	}
}

func (h *Handler) debug(msg string) {
	if h.debugFn != nil {
		h.debugFn(msg)
	}
}

// parseAltSequence detects M- prefix for alt combinations
func (h *Handler) parseAltSequence(seq string) (string, bool) {
	// ESC followed by a character = Alt+char (Meta prefix)
	if len(seq) == 2 && seq[0] == 0x1b {
		char := seq[1]
		// Lowercase letters: M-a through M-z
		if char >= 'a' && char <= 'z' {
			return fmt.Sprintf("M-%c", char), true
		}
		// Uppercase letters: M-S-a through M-S-z (shift implied)
		if char >= 'A' && char <= 'Z' {
			return fmt.Sprintf("M-S-%c", char-'A'+'a'), true
		}
		// Numbers: M-0 through M-9
		if char >= '0' && char <= '9' {
			return fmt.Sprintf("M-%c", char), true
		}
		// Symbols and punctuation
		switch char {
		case '[':
			return "M-[", true
		case ']':
			return "M-]", true
		case '{':
			return "M-{", true
		case '}':
			return "M-}", true
		case '(':
			return "M-(", true
		case ')':
			return "M-)", true
		case '<':
			return "M-<", true
		case '>':
			return "M->", true
		case '/':
			return "M-/", true
		case '\\':
			return "M-\\", true
		case '\'':
			return "M-'", true
		case '"':
			return "M-\"", true
		case '`':
			return "M-`", true
		case ',':
			return "M-,", true
		case '.':
			return "M-.", true
		case ';':
			return "M-;", true
		case ':':
			return "M-:", true
		case '=':
			return "M-=", true
		case '+':
			return "M-+", true
		case '-':
			return "M--", true
		case '_':
			return "M-_", true
		case '!':
			return "M-!", true
		case '@':
			return "M-@", true
		case '#':
			return "M-#", true
		case '$':
			return "M-$", true
		case '%':
			return "M-%", true
		case '^':
			return "M-^", true
		case '&':
			return "M-&", true
		case '*':
			return "M-*", true
		case '?':
			return "M-?", true
		case '|':
			return "M-|", true
		case '~':
			return "M-~", true
		case ' ':
			return "M-Space", true
		default:
			// Special control characters
			switch char {
			case 0x09:
				return "M-Tab", true
			case 0x0D:
				return "M-Enter", true
			case 0x7F:
				return "M-Backspace", true
			case 0x08:
				return "M-Backspace", true
			case 0x1B:
				return "M-Escape", true
			}
			// Other control characters: M-^A through M-^Z
			if char >= 0x01 && char <= 0x1A {
				letter := 'A' + char - 1
				return fmt.Sprintf("M-^%c", letter), true
			}
			// Any other printable ASCII character
			if char >= 0x20 && char < 0x7f {
				return fmt.Sprintf("M-%c", char), true
			}
		}
	}
	return "", false
}

// parseModifiedCSI dynamically parses CSI sequences with modifiers
// Returns single key, or for mouse events returns "" and handles emission internally
func (h *Handler) parseModifiedCSI(seq string) (string, bool) {
	// Check for macOS Option+arrow: ESC ESC [ X
	// Emit "Special" first to distinguish from xterm-style sequences
	if len(seq) == 4 && seq[0] == 0x1b && seq[1] == 0x1b && seq[2] == '[' {
		var key string
		switch seq[3] {
		case 'A':
			key = "M-Up"
		case 'B':
			key = "M-Down"
		case 'C':
			key = "M-Right"
		case 'D':
			key = "M-Left"
		case 'H':
			key = "M-Home"
		case 'F':
			key = "M-End"
		}
		if key != "" {
			// Emit "Special" first, then return the key
			h.emitKey("Special")
			return key, true
		}
	}

	// Must start with ESC [
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return "", false
	}

	body := seq[2:]
	if len(body) == 0 {
		return "", false
	}

	// Check for SGR mouse: ESC [ < ... M/m
	if len(body) >= 4 && body[0] == '<' {
		finalByte := body[len(body)-1]
		if finalByte == 'M' || finalByte == 'm' {
			if posKey, actionKey, ok := parseMouseSGR(seq); ok {
				// For drag events, posKey is empty and position is in actionKey
				// For other events, emit position first, then action
				if posKey != "" {
					h.emitKey(posKey)
				}
				h.emitKey(actionKey)
				return "", true // Signal success but no additional key to emit
			}
		}
	}

	// Check for X10 mouse: ESC [ M Cb Cx Cy (exactly 3 bytes after M)
	if len(body) == 4 && body[0] == 'M' {
		if posKey, actionKey, ok := parseMouseX10(seq); ok {
			// For drag events, posKey is empty and position is in actionKey
			// For other events, emit position first, then action
			if posKey != "" {
				h.emitKey(posKey)
			}
			h.emitKey(actionKey)
			return "", true // Signal success but no additional key to emit
		}
	}

	// Check for Shift+Tab: ESC [ Z
	if body == "Z" {
		return "S-Tab", true
	}

	// Final byte determines the key type
	finalByte := body[len(body)-1]
	if finalByte < 0x40 || finalByte > 0x7E {
		return "", false
	}

	params := body[:len(body)-1]
	parts := splitCSIParams(params)

	switch finalByte {
	case 'A', 'B', 'C', 'D':
		return parseModifiedCursorKey(finalByte, parts)
	case 'H', 'F':
		return parseModifiedHomeEnd(finalByte, parts)
	case 'P', 'Q', 'R', 'S':
		return parseModifiedF1toF4(finalByte, parts)
	case '~':
		return parseModifiedTildeKey(parts)
	case 'u':
		return h.parseKittyProtocol(parts)
	}

	return "", false
}

// splitCSIParams splits parameter string by semicolons
func splitCSIParams(params string) []string {
	if params == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i <= len(params); i++ {
		if i == len(params) || params[i] == ';' {
			parts = append(parts, params[start:i])
			start = i + 1
		}
	}
	return parts
}

// modifierPrefix converts xterm modifier code to key prefix
func modifierPrefix(mod int) string {
	if mod < 2 {
		return ""
	}
	mod--

	prefix := ""
	if mod&1 != 0 {
		prefix += "S-"
	}
	if mod&2 != 0 {
		prefix += "M-"
	}
	if mod&4 != 0 {
		prefix += "C-"
	}
	if mod&8 != 0 {
		prefix += "s-"
	}
	return prefix
}

// parseModifierParam parses a modifier parameter string to int
func parseModifierParam(s string) int {
	if s == "" {
		return 1
	}
	mod := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			mod = mod*10 + int(c-'0')
		} else {
			return 1
		}
	}
	if mod < 1 {
		return 1
	}
	return mod
}

// parseModifiedCursorKey handles ESC [ 1 ; <mod> <A-D>
func parseModifiedCursorKey(finalByte byte, parts []string) (string, bool) {
	keyNames := map[byte]string{
		'A': "Up",
		'B': "Down",
		'C': "Right",
		'D': "Left",
	}

	baseName, ok := keyNames[finalByte]
	if !ok {
		return "", false
	}

	if len(parts) == 0 {
		return baseName, true
	}

	if len(parts) != 2 {
		return "", false
	}

	mod := parseModifierParam(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName, true
}

// parseModifiedHomeEnd handles ESC [ 1 ; <mod> <H|F>
func parseModifiedHomeEnd(finalByte byte, parts []string) (string, bool) {
	keyNames := map[byte]string{
		'H': "Home",
		'F': "End",
	}

	baseName, ok := keyNames[finalByte]
	if !ok {
		return "", false
	}

	if len(parts) == 0 {
		return baseName, true
	}

	if len(parts) != 2 {
		return "", false
	}

	mod := parseModifierParam(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName, true
}

// parseModifiedF1toF4 handles ESC [ 1 ; <mod> <P-S>
func parseModifiedF1toF4(finalByte byte, parts []string) (string, bool) {
	keyNames := map[byte]string{
		'P': "F1",
		'Q': "F2",
		'R': "F3",
		'S': "F4",
	}

	baseName, ok := keyNames[finalByte]
	if !ok {
		return "", false
	}

	if len(parts) == 0 {
		return baseName, true
	}

	if len(parts) != 2 {
		return "", false
	}

	mod := parseModifierParam(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName, true
}

// parseModifiedTildeKey handles ESC [ <num> ; <mod> ~
func parseModifiedTildeKey(parts []string) (string, bool) {
	tildeKeys := map[int]string{
		1:  "Home",
		2:  "Insert",
		3:  "Delete",
		4:  "End",
		5:  "PageUp",
		6:  "PageDown",
		15: "F5",
		17: "F6",
		18: "F7",
		19: "F8",
		20: "F9",
		21: "F10",
		23: "F11",
		24: "F12",
	}

	if len(parts) == 0 {
		return "", false
	}

	keyNum := parseModifierParam(parts[0])
	baseName, ok := tildeKeys[keyNum]
	if !ok {
		return "", false
	}

	if len(parts) == 1 {
		return baseName, true
	}

	if len(parts) == 2 {
		mod := parseModifierParam(parts[1])
		prefix := modifierPrefix(mod)
		return prefix + baseName, true
	}

	return "", false
}

// Kitty protocol special keys
var kittySpecialKeys = map[int]string{
	9:   "Tab",
	13:  "Enter",
	27:  "Escape",
	32:  "Space",
	127: "Backspace",
	// Functional keys (Kitty extended codes)
	57358: "CapsLock",
	57359: "ScrollLock",
	57360: "NumLock",
	57361: "PrintScreen",
	57362: "Pause",
	57363: "Menu",
	// F1-F12
	57364: "F1",
	57365: "F2",
	57366: "F3",
	57367: "F4",
	57368: "F5",
	57369: "F6",
	57370: "F7",
	57371: "F8",
	57372: "F9",
	57373: "F10",
	57374: "F11",
	57375: "F12",
	// F13-F20
	57376: "F13",
	57377: "F14",
	57378: "F15",
	57379: "F16",
	57380: "F17",
	57381: "F18",
	57382: "F19",
	57383: "F20",
	// Keypad
	57414: "Return", // KP_Enter - distinct from Enter (13)
	// Navigation
	57417: "Up",
	57418: "Down",
	57419: "Left",
	57420: "Right",
	57421: "PageUp",
	57422: "PageDown",
	57423: "Home",
	57424: "End",
	57425: "Insert",
	57426: "Delete",
}

// modifierKeyInfo holds modifier name and side (Left/Right)
type modifierKeyInfo struct {
	name string
	side string
}

// Kitty protocol modifier keys (for press/release events)
// Left modifiers are 57441-57446, Right modifiers are 57447-57452
var kittyModifierKeys = map[int]modifierKeyInfo{
	57441: {"Shift", "Left"},
	57442: {"Ctrl", "Left"},
	57443: {"Alt", "Left"},
	57444: {"Super", "Left"},
	57445: {"Hyper", "Left"},
	57446: {"Meta", "Left"},
	57447: {"Shift", "Right"},
	57448: {"Ctrl", "Right"},
	57449: {"Alt", "Right"},
	57450: {"Super", "Right"},
	57451: {"Hyper", "Right"},
	57452: {"Meta", "Right"},
}

// parseKittyProtocol handles CSI keycode ; modifiers : event_type u format
// Event types: 1=press, 2=repeat, 3=release
func (h *Handler) parseKittyProtocol(parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}

	// Parse keycode - handle extended format "keycode:shifted_key:base_key"
	keycodeStr := parts[0]
	if idx := strings.Index(keycodeStr, ":"); idx >= 0 {
		keycodeStr = keycodeStr[:idx]
	}
	keycode := parseModifierParam(keycodeStr)

	// Parse modifiers and event type from second part
	// Format can be: "modifiers" or "modifiers:event_type"
	mod := 1
	eventType := 1 // 1=press (default), 2=repeat, 3=release

	if len(parts) >= 2 {
		modPart := parts[1]
		if idx := strings.Index(modPart, ":"); idx >= 0 {
			mod = parseModifierParam(modPart[:idx])
			if et, err := strconv.Atoi(modPart[idx+1:]); err == nil {
				eventType = et
			}
		} else {
			mod = parseModifierParam(modPart)
		}
	}

	// Check if this is a modifier key press/release
	if modKeyInfo, ok := kittyModifierKeys[keycode]; ok {
		// Map modifier names to our prefix convention
		var prefix string
		switch modKeyInfo.name {
		case "Shift":
			prefix = "S"
		case "Ctrl":
			prefix = "C"
		case "Alt":
			prefix = "A"
		case "Super":
			prefix = "s"
		case "Meta":
			prefix = "M"
		case "Hyper":
			prefix = "H"
		}

		// Event type suffix
		var eventSuffix string
		switch eventType {
		case 1:
			eventSuffix = "-Press"
		case 2:
			eventSuffix = "-Repeat"
		case 3:
			eventSuffix = "-Release"
		}

		// Add :Left or :Right suffix to distinguish sides
		// Apps can match on "S-Press" to catch both, or "S-Press:Left" for specific side
		return prefix + eventSuffix + ":" + modKeyInfo.side, true
	}

	// Build event suffix for non-modifier keys (only for release, press is default)
	eventSuffix := ""
	if eventType == 3 {
		eventSuffix = ":Release"
	} else if eventType == 2 {
		eventSuffix = ":Repeat"
	}

	// Letter keys
	if keycode >= 'a' && keycode <= 'z' {
		return formatLetterKey(byte(keycode), mod) + eventSuffix, true
	} else if keycode >= 'A' && keycode <= 'Z' {
		return formatLetterKey(byte(keycode+32), mod) + eventSuffix, true
	}

	// Symbol keys
	if isSymbolKey(keycode) {
		return formatSymbolKey(byte(keycode), mod) + eventSuffix, true
	}

	// Number keys
	if isNumberKey(keycode) {
		return formatNumberKey(byte(keycode), mod) + eventSuffix, true
	}

	// Special keys from kittySpecialKeys
	baseName, ok := kittySpecialKeys[keycode]

	// If not in our special keys map, treat as unicode codepoint
	if !ok {
		// Check if it's a printable unicode character
		if keycode >= 32 && keycode < 0x110000 {
			baseName = string(rune(keycode))
		} else {
			return "", false
		}
	}

	// Check for macOS Option character decoding in Kitty protocol
	// e.g., Ctrl+´ should become M-^E since ´ = Option+e
	h.mu.Lock()
	decodeMacOS := h.decodeMacOSOption
	h.mu.Unlock()

	if decodeMacOS && len(baseName) > 0 {
		r, size := utf8.DecodeRuneInString(baseName)
		if size == len(baseName) && r != utf8.RuneError { // Single rune
			if decoded, exists := macOSOptionChars[r]; exists {
				// decoded is like "M-e" or "M-A"
				if len(decoded) >= 3 && decoded[0] == 'M' && decoded[1] == '-' {
					baseChar := decoded[2:]

					// Check modifiers from Kitty protocol (mod is 1-indexed)
					hasCtrl := mod > 1 && (mod-1)&4 != 0
					hasShift := mod > 1 && (mod-1)&1 != 0

					// Build result with M- prefix (Option/Meta is implicit from decode)
					result := "M-"
					if hasShift {
						result += "S-"
					}
					if hasCtrl {
						// Format Ctrl+char as ^X
						result += "^" + strings.ToUpper(baseChar)
					} else {
						result += baseChar
					}

					return result + eventSuffix, true
				}
			}
		}
	}

	if mod <= 1 {
		return baseName + eventSuffix, true
	}

	prefix := modifierPrefix(mod)
	return prefix + baseName + eventSuffix, true
}

// formatLetterKey formats a letter key with modifiers
func formatLetterKey(letter byte, mod int) string {
	if mod < 1 {
		mod = 1
	}
	mod--

	hasShift := mod&1 != 0
	hasAlt := mod&2 != 0
	hasCtrl := mod&4 != 0
	hasSuper := mod&8 != 0

	var keyPart string
	if hasCtrl {
		upperLetter := letter - 32
		if hasShift {
			keyPart = "S-^" + string(upperLetter)
		} else {
			keyPart = "^" + string(upperLetter)
		}
	} else if hasShift {
		keyPart = string(letter - 32)
	} else {
		keyPart = string(letter)
	}

	prefix := ""
	if hasSuper {
		prefix += "s-"
	}
	if hasAlt {
		prefix += "M-"
	}

	return prefix + keyPart
}

// symbolShiftMap maps unshifted symbol keycodes to their shifted variants
var symbolShiftMap = map[byte]byte{
	'`':  '~',
	',':  '<',
	'.':  '>',
	'/':  '?',
	';':  ':',
	'\'': '"',
	'[':  '{',
	']':  '}',
	'\\': '|',
	'-':  '_',
	'=':  '+',
}

// numberShiftMap maps number keys to their shifted variants
var numberShiftMap = map[byte]byte{
	'1': '!',
	'2': '@',
	'3': '#',
	'4': '$',
	'5': '%',
	'6': '^',
	'7': '&',
	'8': '*',
	'9': '(',
	'0': ')',
}

// isSymbolKey checks if the keycode is a symbol key
func isSymbolKey(keycode int) bool {
	switch byte(keycode) {
	case '`', ',', '.', '/', ';', '\'', '[', ']', '\\', '-', '=':
		return true
	}
	return false
}

// isNumberKey checks if the keycode is a number key
func isNumberKey(keycode int) bool {
	return keycode >= '0' && keycode <= '9'
}

// formatSymbolKey formats a symbol key with modifiers
func formatSymbolKey(symbol byte, mod int) string {
	if mod < 1 {
		mod = 1
	}
	mod--

	hasShift := mod&1 != 0
	hasAlt := mod&2 != 0
	hasCtrl := mod&4 != 0
	hasSuper := mod&8 != 0

	displayChar := symbol
	if hasShift {
		if shifted, ok := symbolShiftMap[symbol]; ok {
			displayChar = shifted
		}
	}

	var keyPart string
	if hasCtrl {
		keyPart = "^" + string(displayChar)
	} else {
		keyPart = string(displayChar)
	}

	prefix := ""
	if hasSuper {
		prefix += "s-"
	}
	if hasAlt {
		prefix += "M-"
	}

	return prefix + keyPart
}

// formatNumberKey formats a number key with modifiers
func formatNumberKey(number byte, mod int) string {
	if mod < 1 {
		mod = 1
	}
	mod--

	hasShift := mod&1 != 0
	hasAlt := mod&2 != 0
	hasCtrl := mod&4 != 0
	hasSuper := mod&8 != 0

	displayChar := number
	if hasShift {
		if shifted, ok := numberShiftMap[number]; ok {
			displayChar = shifted
		}
	}

	var keyPart string
	if hasCtrl {
		keyPart = "^" + string(displayChar)
	} else {
		keyPart = string(displayChar)
	}

	prefix := ""
	if hasSuper {
		prefix += "s-"
	}
	if hasAlt {
		prefix += "M-"
	}

	return prefix + keyPart
}

// parseMouseSGR parses SGR mouse sequences: ESC [ < Cb ; Cx ; Cy M/m
// Returns position key, action key, and success flag
func parseMouseSGR(seq string) (string, string, bool) {
	// Must start with ESC [ <
	if len(seq) < 6 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != '<' {
		return "", "", false
	}

	// Final byte must be M (press) or m (release)
	finalByte := seq[len(seq)-1]
	if finalByte != 'M' && finalByte != 'm' {
		return "", "", false
	}
	isRelease := finalByte == 'm'

	// Parse parameters: Cb;Cx;Cy
	params := seq[3 : len(seq)-1]
	parts := splitCSIParams(params)
	if len(parts) != 3 {
		return "", "", false
	}

	cb := parseModifierParam(parts[0])
	cx := parseModifierParam(parts[1])
	cy := parseModifierParam(parts[2])

	return formatMouseEvent(cb, cx, cy, isRelease)
}

// parseMouseX10 parses X10 mouse sequences: ESC [ M Cb Cx Cy
// Returns position key, action key, and success flag
func parseMouseX10(seq string) (string, string, bool) {
	// Must be exactly ESC [ M followed by 3 bytes
	if len(seq) != 6 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != 'M' {
		return "", "", false
	}

	// Decode button and coordinates (all have 32 added)
	cb := int(seq[3]) - 32
	cx := int(seq[4]) - 32
	cy := int(seq[5]) - 32

	// X10 protocol: button code 3 means release
	isRelease := (cb & 3) == 3

	return formatMouseEvent(cb, cx, cy, isRelease)
}

// formatMouseEvent formats a mouse event into position and action keys
// For drag events, position is embedded in action key (MouseLeftDrag@x,y)
// For press/release/scroll, separate posKey and actionKey are returned
func formatMouseEvent(cb, cx, cy int, isRelease bool) (string, string, bool) {
	// Decode modifiers from button code
	hasShift := (cb & 4) != 0
	hasAlt := (cb & 8) != 0
	hasCtrl := (cb & 16) != 0

	// Build modifier prefix
	prefix := ""
	if hasShift {
		prefix += "S-"
	}
	if hasAlt {
		prefix += "M-"
	}
	if hasCtrl {
		prefix += "C-"
	}

	// Decode button and action
	var action string
	buttonBits := cb & 3
	isMotion := (cb & 32) != 0
	isScroll := (cb & 64) != 0

	if isScroll {
		// Scroll wheel
		if buttonBits == 0 {
			action = "MouseScrollUp"
		} else {
			action = "MouseScrollDown"
		}
	} else if isMotion {
		// Mouse drag - include position in action key
		switch buttonBits {
		case 0:
			action = "MouseLeftDrag"
		case 1:
			action = "MouseMiddleDrag"
		case 2:
			action = "MouseRightDrag"
		default:
			action = "MouseDrag"
		}
		// For drag events, embed position in the action key and return empty posKey
		return "", fmt.Sprintf("%s%s@%d,%d", prefix, action, cx, cy), true
	} else if isRelease {
		// Button release
		switch buttonBits {
		case 0:
			action = "MouseLeftRelease"
		case 1:
			action = "MouseMiddleRelease"
		case 2:
			action = "MouseRightRelease"
		default:
			action = "MouseRelease"
		}
	} else {
		// Button press
		switch buttonBits {
		case 0:
			action = "MouseLeftPress"
		case 1:
			action = "MouseMiddlePress"
		case 2:
			action = "MouseRightPress"
		default:
			action = "MousePress"
		}
	}

	// Position key for press/release/scroll
	posKey := fmt.Sprintf("Mouse@%d,%d", cx, cy)
	return posKey, prefix + action, true
}
