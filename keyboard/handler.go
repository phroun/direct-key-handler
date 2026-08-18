// Package keyboard provides raw keyboard input handling with escape sequence parsing.
// It handles VT100/ANSI escape sequences, UTF-8 characters, bracketed paste,
// and line assembly for terminal input.
package keyboard

import (
	"encoding/base64"
	"fmt"
	"io"
	"runtime"
	"sort"
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
	Keys  chan string // Parsed key events ("a", "M-a", "F1", etc.)
	Lines chan []byte // Assembled lines

	// Callbacks (optional, called in addition to channel sends)
	OnKey        func(key string)       // Called on each key event
	OnLine       func(line []byte)      // Called on each completed line
	OnPaste      func(content []byte)   // Called on bracketed paste content (complete)
	OnPasteChunk func(chunk PasteChunk) // Called on incremental paste chunks

	// OnFocus is called when the terminal reports gaining or losing focus,
	// which it does only if the application has enabled focus reporting
	// (DECSET ?1004). Not a key: nothing is sent to the Keys channel for it.
	//
	// Losing focus also RELEASES every key still down, before this is called.
	// The keyboard has gone to someone else, so the key-up for anything held
	// is delivered there and will never arrive here — waiting for it means
	// waiting forever, and the press would stand for good in anything tracking
	// held keys. A browser does the same on blur.
	OnFocus func(focused bool)

	// OnMode is called when a keyboard STATE moves — the pad's lock, CapsLock,
	// focus, or one a host published itself. Not a key: the cap that moves the
	// pad's lock is eaten (see numlock.go) and nothing is sent to the Keys
	// channel for any of them. See modes.go.
	//
	// The pad's lock fires on a system that has none of its own too, where this
	// package keeps the state — so a host can show an indicator on a Mac, which
	// is the one place the OS offers none.
	OnMode func(m Mode)

	// OnClipboard is called with an OSC 52 clipboard *response*
	// (ESC ] 52 ; <selection> ; <base64> BEL/ST) - the terminal's answer to a
	// clipboard-read query. selection is the target byte ('c', 'p', ...) and
	// data is the base64-decoded clipboard content. This is distinct from
	// OnPaste (a user pasting into the terminal): a clipboard response is a
	// fetch reply, so the caller consumes it rather than inserting it as typed
	// text. It reuses the same buffering mechanism as bracketed paste.
	OnClipboard func(selection byte, data []byte)

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

	// OSC 52 clipboard-response state - the same accumulate-into-a-buffer idea
	// as bracketed paste, but with an OSC terminator (BEL or ST) and base64
	// content, emitted on OnClipboard instead of OnPaste. Kept in its own small
	// buffer (not fullPasteContent) so the well-tested paste path is untouched;
	// the two are never in flight at the same time.
	inClipboard     bool
	clipboardBuffer []byte // accumulates "<selection>;<base64>"
	clipboardEsc    bool   // last byte was ESC (a possible ST terminator start)

	// APC (Application Program Command) response state, gathered exactly like
	// the OSC 52 body above: ESC _ ... ST. The kitty graphics protocol answers
	// here (ESC _ G i=<id>;OK ESC \), which is how an application asks whether
	// the terminal can show it a picture. Surfaced as one "APC:<body>" key
	// rather than interpreted, since APC is a private channel and the payload
	// means whatever the two ends agreed it means.
	inAPC     bool
	apcBuffer []byte // accumulates the body between ESC _ and the terminator
	apcEsc    bool   // last byte was ESC (a possible ST terminator start)

	// macOS Option key decoding
	decodeMacOSOption bool // When true, decode macOS Option+key chars to M-key notation

	// keyNames renames keys on the way out (see Options.KeyNames). Read-only
	// after New, so it needs no lock.
	keyNames map[Key]string

	// Paste key echo. When false, bracketed-paste content is delivered only via
	// OnPaste/OnPasteChunk and is NOT also re-emitted as individual key events.
	emitPasteKeys bool

	// Echo output (where to echo typed characters)
	echoWriter io.Writer

	// heldKeys remembers, per kitty keycode, the name this package EMITTED when
	// that key went down, so its repeat and release can be reported under the
	// same name rather than derived again.
	//
	// Deriving again is wrong because the modifiers have moved on. A release
	// carries the modifier mask as it stands at the moment of release, so
	// letting go of Control a few milliseconds before the letter — which is
	// what fingers actually do — sent "^A" down and "a" up. Nothing downstream
	// could pair those, and anything tracking held keys held "^A" forever.
	//
	// The rules are what keep it honest: a press RECORDS, overwriting any
	// stale entry, so a press can never inherit an older one; a release
	// REPLAYS and DELETES, so an entry cannot outlive the key being down; and
	// a release with no entry is dropped, which is safe by construction —
	// no entry means this package never emitted a press for that key, so
	// nothing downstream believes it is held.
	//
	// Guarded by mu.
	heldKeys map[string]string

	// numLock is what we know about the pad's lock, and on a system that has
	// no lock of its own it IS the lock. See numlock.go. Guarded by mu.
	numLock numLockState

	// caps and focus are the other two states published as modes, and unlike
	// the pad's lock neither is knowable until something says so: CapsLock
	// rides in on a key, focus in on a report the terminal only sends if it
	// was asked. See modes.go. Guarded by mu.
	caps  modeLatch
	focus modeLatch

	// extraModes are the states a HOST published through SetMode, which this
	// package stores and reports without knowing what any of them mean.
	// Guarded by mu.
	extraModes map[string]string

	// sides is which side of each doubling modifier is held, for the Hyper
	// promotion. See hyper.go. Guarded by mu.
	sides doubledSides

	// mouseHeld is the name this package EMITTED for each mouse button that is
	// down — index 0 left, 1 middle, 2 right, empty for a button that is up.
	//
	// It serves the same purpose for the mouse that heldKeys serves for the
	// keyboard, and it answers a question the keyboard never has to: the X10
	// encoding's release does not say WHICH button was let go, only that one
	// was. The buttons this package has reported as down are the only honest
	// answer, so that is what a button-agnostic release releases.
	//
	// Guarded by mu.
	mouseHeld [3]string

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

	// KeyNames renames the keys an application cares about, so it receives its
	// own vocabulary instead of translating this package's afterwards (see the
	// Key type). Entries overlay the defaults; anything unlisted keeps its
	// DefaultName. Modifier prefixes and event suffixes are preserved, so
	// naming KeyPageUp "pgup" also turns "M-PageUp" into "M-pgup". nil leaves
	// every name at its default.
	KeyNames map[Key]string

	// DebugFn is called with debug messages (optional)
	DebugFn func(string)

	// ManageTerminal controls whether to put stdin in raw mode.
	// Only applies if InputReader is os.Stdin and is a terminal.
	// Default: true
	ManageTerminal *bool

	// EmitPasteKeys controls whether bracketed-paste content is ALSO re-emitted
	// as individual key events on the Keys channel. Consumers that handle paste
	// through OnPaste or OnPasteChunk (e.g. to batch it into a single edit) do
	// not want this echo: it duplicates the content and, on a large paste, can
	// overflow the Keys channel and lose events. Default: true (backward
	// compatible); set to false to deliver paste only via the callbacks.
	EmitPasteKeys *bool
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

	// Default to true (paste is echoed as keys) for backward compatibility.
	emitPasteKeys := true
	if opts.EmitPasteKeys != nil {
		emitPasteKeys = *opts.EmitPasteKeys
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
		emitPasteKeys:     emitPasteKeys,
		keyNames:          copyKeyNames(opts.KeyNames),
		heldKeys:          make(map[string]string),
		// A locked pad is what the printed legends promise, what every pad
		// without a latch does permanently, and what a pad with one is in
		// almost always. The first keystroke corrects it if not.
		numLock: numLockState{on: true},
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

	// Forget which keys were down. Nothing is emitted for them — the consumer
	// is going away, and a release delivered during shutdown reaches nobody —
	// but the entries must not survive into a restart, where a release could
	// otherwise replay a name from the previous session.
	clear(h.heldKeys)

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
	// CSI-number forms of F1-F4: sent by terminals with the Kitty keyboard
	// protocol enabled (disambiguate flag drops the ambiguous SS3 ESC O P
	// encodings), and by legacy xterm-R6/rxvt/VT220-style terminals.
	"\x1b[11~": "F1",
	"\x1b[12~": "F2",
	"\x1b[13~": "F3",
	"\x1b[14~": "F4",
	// Linux console F1-F5.
	"\x1b[[A":  "F1",
	"\x1b[[B":  "F2",
	"\x1b[[C":  "F3",
	"\x1b[[D":  "F4",
	"\x1b[[E":  "F5",
	"\x1b[15~": "F5",
	"\x1b[17~": "F6",
	"\x1b[18~": "F7",
	"\x1b[19~": "F8",
	"\x1b[20~": "F9",
	"\x1b[21~": "F10",
	"\x1b[23~": "F11",
	"\x1b[24~": "F12",

	// Navigation keys
	"\x1b[H":  "Home",
	"\x1b[F":  "End",
	"\x1b[1~": "Home",
	"\x1b[4~": "End",
	"\x1b[2~": "Insert",
	"\x1b[3~": "FDel", // forward delete; the DEL character is "Delete"
	"\x1b[5~": "PageUp",
	"\x1b[6~": "PageDown",

	// Alternate arrow key sequences (some terminals)
	"\x1bOA": "Up",
	"\x1bOB": "Down",
	"\x1bOC": "Right",
	"\x1bOD": "Left",

	// The keypad's Enter, which a terminal in application keypad mode (DECKPAM)
	// sends as SS3 M. It is the only encoding that tells this key apart from the
	// home-row Return: in numeric keypad mode both send CR, and a reader given
	// CR cannot know which was struck. Without this entry the sequence decoded
	// as three keystrokes — Escape, then O, then M — so the keypad's Enter was
	// unreadable from any terminal that sent it.
	//
	// The rest of the application-keypad set (SS3 p-y for the digits, l/m/n for
	// the punctuation) is still undecoded; when it arrives it belongs under the
	// same "P-" prefix this one now carries.
	"\x1bOM": "P-Enter",
}

// Control key names
var controlKeys = map[byte]string{
	0:  "^@", // Ctrl-Space or Ctrl-@
	1:  "^A",
	2:  "^B",
	3:  "^C",
	4:  "^D",
	5:  "^E",
	6:  "^F",
	7:  "^G",
	8:  "Backspace", // Ctrl-H; the OTHER erase byte is 127, named "Delete"
	9:  "Tab",       // Ctrl-I
	10: "^J",        // Ctrl-J (LF) - distinct from Enter
	11: "^K",
	12: "^L",
	13: "Return", // Ctrl-M (CR) - the home-row key; the keypad's is "P-Enter"
	14: "^N",
	15: "^O",
	16: "^P",
	17: "^Q",
	18: "^R",
	19: "^S",
	20: "^T",
	21: "^U",
	22: "^V",
	23: "^W",
	24: "^X",
	25: "^Y",
	26: "^Z",
	27: "Escape", // Escape itself (handled specially)
	28: "^\\",
	29: "^]",
	30: "^^",
	31: "^_",
	// DEL, which is what most terminals in use send for their backspace key
	// (terminfo kbs=^? for xterm, linux, screen, tmux, rxvt). Named apart from
	// byte 8 so an application that maps input to key events can tell which
	// one it was handed; both erase backwards, and an application that does
	// not care should alias them rather than have this table guess.
	127: "Delete",
}

// macOSOptionChars maps Unicode characters produced by macOS Option+key to M-key notation
// This is for US keyboard layout
//
// Every entry must be a character that ONLY Option can produce. The table works
// by recognizing the character alone — there is no modifier field on the byte
// path — so an entry keyed on something an unmodified key also produces takes
// that plain key away from the user, and gives them a chord they did not press.
// '`' was such an entry; see TestPlainBacktickIsNotAChord.
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
	'Ô':      "M-J", // Option+Shift+j
	'\uF8FF': "M-K", // Option+Shift+k (Apple logo, private use area)
	'Ò':      "M-L", // Option+Shift+l
	'Â':      "M-M", // Option+Shift+m
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
	'–':      "M--",  // Option+minus (en dash)
	'≠':      "M-=",  // Option+equals
	'“':      "M-[",  // Option+[ (left double quote, U+201C — the char Option actually emits; keying on ASCII '"' here would rewrite a plain double quote into a curly one)
	'\u2019': "M-]",  // Option+] (right single quote)
	'«':      "M-\\", // Option+backslash
	'…':      "M-;",  // Option+semicolon
	'æ':      "M-'",  // Option+quote
	'≤':      "M-,",  // Option+comma
	'≥':      "M-.",  // Option+period
	'÷':      "M-/",  // Option+slash

	// No entry for '`'. Option+backtick emits a plain backtick on the layouts
	// where it emits anything at all, so this table cannot tell the chord from
	// the key — and when it cannot tell, the key wins: one is pressed all day
	// and the other almost never. Under the kitty protocol the modifier field
	// says which it was, and M-` arrives correctly without this table.
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
			// An APC that never terminated was not one: ESC _ is equally how
			// a terminal reports Mega+_, and a reply from the terminal arrives
			// as one burst rather than trailing off.
			if h.inAPC {
				if h.apcEsc {
					h.finishAPC() // the ESC was a terminator; its '\' never came
				} else {
					h.abandonAPC(escTimeout)
				}
			}
			// Escape sequence timeout - try Mega sequence parsing before giving up
			if h.inEscape && len(h.escBuffer) > 0 {
				seq := string(h.escBuffer)
				// Try Mega+key parsing (ESC followed by character)
				if key, ok := h.parseMegaSequence(seq); ok {
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

// osc52Start introduces an OSC 52 clipboard response: ESC ] 52 ; . The body
// (<selection>;<base64>) follows and is terminated by BEL (0x07) or ST (ESC \).
const osc52Start = "\x1b]52;"

// apcStart introduces an APC string: ESC _ . The body runs to a BEL or ST
// (ESC \) terminator, as OSC 52's does.
const apcStart = "\x1b_"

// maxAPCBody caps an APC body. A reply is a few dozen bytes; anything longer
// is a terminal echoing something unexpected, and the buffer must not grow
// without bound on a stream we do not control.
const maxAPCBody = 4096

// escapeTimeout is how long an incomplete escape sequence waits for the rest
// of itself before being read as what it would otherwise be - the same window
// the escape disambiguation below has always used, named here because the APC
// accumulator has to hold to it too.
const escapeTimeout = 50 * time.Millisecond

// pasteEndBufferSize is the number of bytes to keep buffered during paste
// to avoid splitting the end sequence (\x1b[201~ is 6 bytes, we buffer 7 to be safe)
const pasteEndBufferSize = 7

// DefaultPasteChunkSize is the default size for paste chunks (1KB)
const DefaultPasteChunkSize = 1024

// processByte handles a single byte of input
func (h *Handler) processByte(b byte, escTimeout *time.Timer) {
	// Handle an in-progress OSC 52 clipboard response: accumulate the body
	// until a BEL (0x07) or ST (ESC \) terminator, then decode and emit it.
	// base64 never contains ESC, so an ESC always ends the body.
	if h.inClipboard {
		if h.clipboardEsc {
			h.clipboardEsc = false
			h.finishClipboard() // ESC (\ for ST, or stray) ends the body
			return
		}
		switch b {
		case 0x07: // BEL terminator
			h.finishClipboard()
		case 0x1b: // ESC - possible ST terminator start
			h.clipboardEsc = true
		default:
			h.clipboardBuffer = append(h.clipboardBuffer, b)
		}
		return
	}

	// Handle an in-progress APC response: accumulate the body until BEL or
	// ST (ESC \), then emit it. Same shape as the OSC 52 branch above.
	if h.inAPC {
		if h.apcEsc {
			h.apcEsc = false
			h.finishAPC() // ESC (\ for ST, or stray) ends the body
			return
		}
		switch b {
		case 0x07: // BEL terminator
			h.finishAPC()
		case 0x1b: // ESC - possible ST terminator start
			h.apcEsc = true
			escTimeout.Reset(escapeTimeout)
		default:
			if len(h.apcBuffer) < maxAPCBody {
				h.apcBuffer = append(h.apcBuffer, b)
			}
			// A real reply arrives as one burst; keep the abandon timer
			// ahead of it (see the escTimeout case in the read loop).
			escTimeout.Reset(escapeTimeout)
		}
		return
	}

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
		// ESC restarts. It is the one byte that can never appear inside a
		// sequence, so a second one ends whatever is pending and begins anew:
		// the unconditional "escape entry" transition every VT state machine
		// has from every state.
		//
		// Without this, a lone Escape immediately followed by any sequence
		// swallows both. The bytes accumulate as "\x1b\x1b[...", match no
		// binding, are no valid prefix, fail parseModifiedCSI and the Mega
		// parsers, and fall through to emitEscapeBuffer -- which types the
		// SECOND sequence's body out as literal characters. With mouse
		// reporting on, pressing Escape while the pointer is over the window
		// puts the two in one read, and a mouse report's body lands in the
		// user's document.
		//
		// The 50ms escape timer cannot cover this: both bytes arrive in the
		// same read, so there is no gap to time out on.
		if b == 0x1b {
			// A second ESC while ONLY the first ESC is buffered is the head of
			// a Mega-prefixed escape-sequence key: the Option/Alt cap plus an
			// arrow sends
			// ESC + ESC[A. Keep buffering the double-ESC and let the resolver
			// below decide — it parses ESC ESC [X as M-<arrow>, and a double-ESC
			// that is NOT such a chord (a real Escape immediately followed by
			// another sequence, e.g. a mouse report landing in the same read) is
			// split into a standalone Escape plus its trailing sequence further
			// down. Bare Escape still resolves on its own via the timeout.
			if len(h.escBuffer) == 1 {
				h.escBuffer = append(h.escBuffer, b)
				escTimeout.Reset(50 * time.Millisecond)
				return
			}
			// Mid-sequence ESC abandons the malformed partial and restarts.
			h.emitEscapeBuffer()
			h.escBuffer = []byte{b}
			h.inEscape = true
			escTimeout.Reset(50 * time.Millisecond)
			return
		}

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

		// Check for an OSC 52 clipboard-response start (ESC ] 52 ;). The body
		// runs until BEL/ST and is gathered by the h.inClipboard branch above.
		if seq == osc52Start {
			h.debug("OSC 52 clipboard response start detected")
			h.inEscape = false
			h.escBuffer = nil
			h.inClipboard = true
			h.clipboardBuffer = nil
			h.clipboardEsc = false
			escTimeout.Stop()
			return
		}

		// Check for an APC start (ESC _). The body runs until BEL/ST and is
		// gathered by the h.inAPC branch above.
		if seq == apcStart {
			h.debug("APC response start detected")
			h.inEscape = false
			h.escBuffer = nil
			h.inAPC = true
			h.apcBuffer = nil
			h.apcEsc = false
			// ESC _ is also how a terminal reports Mega+_, so this cannot
			// simply commit to APC and wait: an unterminated one is given
			// back as that key (see abandonAPC).
			escTimeout.Reset(escapeTimeout)
			return
		}

		// Focus reporting (DECSET ?1004): CSI I on gaining focus, CSI O on
		// losing it. These are not keys, and without this they fell through
		// every parse and were re-read byte by byte — a phantom Escape, then
		// "[" and "I" typed as text, for any application that had enabled the
		// mode.
		if seq == "\x1b[I" || seq == "\x1b[O" {
			h.handleFocusReport(seq == "\x1b[I")
			h.escBuffer = nil
			h.inEscape = false
			escTimeout.Stop()
			return
		}

		if key, ok := escBindings[seq]; ok {
			// Remember it as held. This table holds LITERAL sequences, so it
			// only ever matches presses — a release carries the event
			// sub-parameter and can never be one of these strings, so it falls
			// through to parseModifiedCSI below. Without recording here the
			// press of every literally-spelled key would go unremembered and
			// its release would arrive an orphan and be dropped: an unmodified
			// arrow goes DOWN as "\x1b[A" and comes UP as "\x1b[1;1:3A", and
			// that is the pair this would have lost.
			h.recordHeld(seq, key)
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

		// Try Mega+key parsing (ESC followed by character)
		if key, ok := h.parseMegaSequence(seq); ok {
			h.emitKey(key)
			h.escBuffer = nil
			h.inEscape = false
			escTimeout.Stop()
			return
		}

		// A double-ESC that did not resolve as a Mega chord (Mega+arrow is
		// handled by parseModifiedCSI above) is a real Escape immediately
		// followed by another sequence — classically Escape pressed while mouse
		// tracking put a report in the same read. Emit the standalone Escape and
		// re-parse the trailing sequence so it resolves normally (a mouse report
		// becomes a mouse event) instead of being typed out as literal text.
		if len(h.escBuffer) >= 2 && h.escBuffer[0] == 0x1b && h.escBuffer[1] == 0x1b {
			tail := append([]byte(nil), h.escBuffer[1:]...) // second ESC + its sequence
			h.escBuffer = nil
			h.inEscape = false
			escTimeout.Stop()
			h.emitKey("Escape")
			for _, tb := range tail {
				h.processByte(tb, escTimeout)
			}
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
		key, ok := controlKeys[b]
		if !ok {
			key = fmt.Sprintf("^%c", b+64)
		}
		h.holdByteKey(b, key)
		h.emitKey(key)
		return
	}

	// Regular printable character or start of UTF-8 sequence
	if b < 128 {
		h.holdByteKey(b, string(b))
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
	// A partial OSC 52 clipboard-response introducer (ESC ] 5 2 ;): keep
	// buffering until the full osc52Start is seen (then processByte switches to
	// clipboard mode). Without this, ESC ] would fall through and be emitted as
	// stray keys.
	if len(seq) < len(osc52Start) && osc52Start[:len(seq)] == seq {
		return true
	}

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

	// The application's own name for this key, if it chose one. Line assembly
	// below matches on the canonical name, so it keeps `key`; everything that
	// leaves this package gets `display`.
	display := h.displayKey(key)

	h.debug(fmt.Sprintf("Key: %q", display))

	// Call callback if set
	if h.OnKey != nil {
		h.OnKey(display)
	}

	// Check if we're in line read mode
	h.mu.Lock()
	inLineMode := h.inLineReadMode
	h.mu.Unlock()

	if inLineMode {
		// In line read mode: keys go to line assembly
		h.handleLineAssembly(key)
	} else {
		key = display
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

// finishAPC ends an APC response, emitting the accumulated body as one
// "APC:<body>" key. The body is passed through verbatim: APC is a private
// channel between an application and the terminal, so this layer's job is to
// find its boundaries, not to read it. An empty body emits nothing — there is
// no reply in it to dispatch on.
func (h *Handler) finishAPC() {
	body := string(h.apcBuffer)
	h.inAPC = false
	h.apcBuffer = nil
	h.apcEsc = false
	if body == "" {
		return
	}
	h.debug(fmt.Sprintf("APC response, %d bytes", len(body)))
	h.emitKey("APC:" + body)
}

// abandonAPC gives up on an APC that never terminated and hands the input
// back the way it would have arrived before: ESC _ as the Mega+_ key, then
// whatever was accumulated as ordinary input. Typing must never be swallowed
// by a reply that turned out not to be one.
func (h *Handler) abandonAPC(escTimeout *time.Timer) {
	body := h.apcBuffer
	h.inAPC = false
	h.apcBuffer = nil
	h.apcEsc = false
	if key, ok := h.parseMegaSequence(apcStart); ok {
		h.emitKey(key)
	}
	for _, b := range body {
		h.processByte(b, escTimeout)
	}
}

// finishClipboard ends an OSC 52 clipboard response: the accumulated body is
// "<selection>;<base64>", so it splits off the selection, base64-decodes the
// payload, and delivers it on OnClipboard. A malformed body is dropped. Unlike
// a paste, the content is NOT emitted as keys - it is a fetch reply the caller
// consumes.
func (h *Handler) finishClipboard() {
	body := h.clipboardBuffer
	h.inClipboard = false
	h.clipboardBuffer = nil
	h.clipboardEsc = false

	var sel byte
	payload := body
	if i := strings.IndexByte(string(body), ';'); i >= 0 {
		if i > 0 {
			sel = body[0] // first selection byte (c/p/...)
		}
		payload = body[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		h.debug(fmt.Sprintf("OSC 52 malformed base64, dropped (%d bytes)", len(payload)))
		return
	}
	h.debug(fmt.Sprintf("OSC 52 clipboard response, %d bytes", len(data)))
	if h.OnClipboard != nil {
		h.OnClipboard(sel, data)
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
	} else if h.emitPasteKeys {
		// Normal mode: emit each character as individual key events. Consumers
		// that take paste via OnPaste/OnPasteChunk opt out (EmitPasteKeys=false)
		// so the content is not also delivered as a burst of keystrokes.
		for len(content) > 0 {
			r, size := utf8.DecodeRune(content)
			if r == utf8.RuneError && size == 1 {
				content = content[1:]
				continue
			}
			// Handle special characters
			if r == '\r' {
				h.emitKey("Return")
			} else if r == '\n' {
				h.emitKey("^J")
			} else if r == '\t' {
				h.emitKey("Tab")
			} else if r == 0x7f {
				h.emitKey("Delete") // the DEL character, as controlKeys names it
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

	// Match the nameable keys by identity, not spelling. Line assembly runs
	// before an application's renaming (emitKey passes the canonical name
	// here), so these arrive under this package's DEFAULT names — and a
	// literal would break silently the moment a default changed, as swapping
	// the Return/Enter defaults would have. The control chords keep literals:
	// they come from the control byte, not from a renameable name table.
	k := keyByDefaultName[key]
	switch {
	case k == KeyReturn:
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

	// Both erase bytes, because line assembly is where a person is typing and
	// they pressed one backspace key. Which byte their terminal sent for it is
	// exactly the thing this package refuses to guess at the naming layer, so
	// it has to accept both here — matching only KeyBackspace would leave the
	// key dead on every terminal whose kbs is ^?, which is most of them.
	case k == KeyBackspace || k == KeyDEL:
		if len(h.charByteLengths) > 0 {
			lastCharLen := h.charByteLengths[len(h.charByteLengths)-1]
			h.currentLine = h.currentLine[:len(h.currentLine)-lastCharLen]
			h.charByteLengths = h.charByteLengths[:len(h.charByteLengths)-1]
			h.echoLocked("\b \b")
		}

	case key == "^U":
		// Clear line
		for range h.charByteLengths {
			h.echoLocked("\b \b")
		}
		h.currentLine = nil
		h.charByteLengths = nil

	case key == "^C":
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

// parseMegaSequence detects the M- prefix for Mega combinations
func (h *Handler) parseMegaSequence(seq string) (string, bool) {
	// ESC followed by a character = Mega+char
	if len(seq) == 2 && seq[0] == 0x1b {
		char := seq[1]
		// A trailing ESC is not Mega+Escape: ESC ESC is a double Escape (or the
		// head of a Mega+arrow, resolved before this) — never M-Escape.
		if char == 0x1b {
			return "", false
		}
		// Letters: "M-a" lowercase, "M-X" upper. Case carries Shift here, the
		// same as it does for every other shown key.
		//
		// This spelled the shifted form "M-S-x", and that invented a
		// distinction the wire never made. ESC 'X' is two bytes, 0x1B 0x58,
		// with no modifier field anywhere — the uppercase byte IS the whole of
		// what the terminal said. Decomposing it into a prefix plus a
		// lowercased base then spent that invention, and left "M-X" — the
		// spelling this vocabulary uses for a shifted shown key everywhere
		// else — unreachable on this path.
		//
		// Every shifted SYMBOL already arrived as itself: "%" is Shift+5 and
		// comes through "M-%", "?" is Shift+/ and comes through "M-?". Letters
		// were the sole exception. The macOS Option table in this file has
		// spelled uppercase "M-A" from the beginning, and says why in a comment
		// beside it.
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' {
			return fmt.Sprintf("M-%c", char), true
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
			// A control byte under Mega is the name that byte already has, with
			// the prefix in front. Reading controlKeys rather than restating it
			// is the point: this was a hand-copied duplicate of that table plus
			// a second copy of its ^A-^Z half, and it had DRIFTED.
			//
			// 0x7F said "M-Backspace" while the bare byte says "Delete", so
			// M-Delete could not be produced at all — and Alt+Backspace, which
			// is the delete-previous-word chord, arrives as ESC 0x7F from every
			// terminal whose kbs is ^?, which is most of them. A keymap binding
			// M-Delete never fired while M-Backspace answered for both
			// keyboards: exactly the conflation naming the two bytes apart
			// exists to prevent, reappearing as soon as a modifier was held.
			//
			// 0x0D said "M-Enter", and "Enter" is the KEYPAD's name — bare
			// Enter is never emitted now, only "P-Enter". The home-row key is
			// Return.
			//
			// Derived, the two cannot drift again: the Mega form IS the bare
			// name with a prefix, by construction.
			if name, ok := controlKeys[char]; ok {
				return "M-" + name, true
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
// parseModifiedCSI decodes one CSI sequence and reconciles it against the keys
// this package has already reported as down.
//
// The reconciliation sits here, around every family at once, rather than inside
// the "u" decoder: with event types negotiated but disambiguation left off — as
// a host does when it wants presses to stay byte-identical — an arrow key's
// release arrives as "CSI 1;1:3A", not as a "u" form at all. Fixing only the
// keycode path would have left the common case untouched.
func (h *Handler) parseModifiedCSI(seq string) (string, bool) {
	name, ok := h.decodeModifiedCSI(seq)
	if !ok {
		return name, ok
	}
	return h.reconcileHeld(seq, name)
}

func (h *Handler) decodeModifiedCSI(seq string) (string, bool) {
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
			if keys, ok := h.parseMouseSGR(seq); ok {
				for _, k := range keys {
					h.emitKey(k)
				}
				return "", true // Signal success but no additional key to emit
			}
		}
	}

	// Check for X10 mouse: ESC [ M Cb Cx Cy (exactly 3 bytes after M)
	if len(body) == 4 && body[0] == 'M' {
		if keys, ok := h.parseMouseX10(seq); ok {
			for _, k := range keys {
				h.emitKey(k)
			}
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
	case 'R':
		// A two-parameter R whose first parameter is not 1 is a Cursor
		// Position Report (DSR reply: ESC[row;colR), not modified F3 —
		// the legacy F3-with-modifiers form is always "1;mod". Surface it
		// as a distinct event so the application can consume it.
		if len(parts) == 2 && parts[0] != "1" && parts[0] != "" {
			return "CPR:" + parts[0] + ";" + parts[1], true
		}
		return parseModifiedF1toF4(finalByte, parts)
	case 'P', 'Q', 'S':
		return parseModifiedF1toF4(finalByte, parts)
	case '~':
		return parseModifiedTildeKey(parts)
	case 'u':
		return h.parseKittyProtocol(parts)
	// window-op reports (XTWINOPS replies) ---------------------
	case 't':
		// Reply form CSI Ps ; p1 ; p2 t (Ps = 3 pos, 4 pixel size, 6 CELL
		// pixel size, 8/9 char size). Surface generically; the app dispatches
		// on Ps. Only the plain numeric reply — never the private
		// CSI > / = / ? set-requests (app->terminal), which never arrive here.
		if len(parts) >= 1 && parts[0] != "" && parts[0][0] >= '0' && parts[0][0] <= '9' {
			return "WinOp:" + params, true // params = body without the final 't'
		}
		return "", false
	// Device Attributes replies -------------------------------
	case 'c':
		// CSI ? Ps ; Ps ; ... c is the PRIMARY DA reply: the terminal's list
		// of what it supports, where attribute 4 is Sixel graphics. CSI > ...
		// c is the SECONDARY DA reply (terminal id, version, ROM). Both are
		// surfaced generically, like WinOp above; the app dispatches on the
		// attributes it cares about. Only replies reach here — the plain
		// CSI c / CSI > c REQUESTS travel app->terminal and never arrive.
		switch {
		case len(body) >= 2 && body[0] == '?':
			return "DA1:" + body[1:len(body)-1], true
		case len(body) >= 2 && body[0] == '>':
			return "DA2:" + body[1:len(body)-1], true
		}
		return "", false
	// DECRPM reply to a DECRQM query --------------------------
	case 'y':
		// CSI ? Ps ; Pm $ y  (Pm: 0 unrecognized, 1 set, 2 reset,
		// 3 perm-set, 4 perm-reset). Lets the app detect optional-mode
		// support, e.g. ?1016 SGR-pixels.
		if len(body) >= 5 && body[0] == '?' && body[len(body)-2] == '$' {
			ip := splitCSIParams(body[1 : len(body)-2]) // "Ps;Pm"
			if len(ip) == 2 {
				return "DECRPM:" + ip[0] + ";" + ip[1], true
			}
		}
		return "", false
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

	// Canonical order: C- G- M- m- S- s- H- ^. It follows the order macOS
	// renders modifiers (⌃⌥⇧⌘), extended with the ones a Mac keyboard has
	// no cap for. Order is not meaning -- a consumer that sorts before matching
	// does not care -- but emitting one fixed order keeps a consumer that
	// compares strings from having to know which producer it is listening to.
	return modsFromKitty(mod + 1).prefix()
}

// parseModifierParam parses a modifier parameter string to int
// Returns 1 for empty string or invalid input (xterm modifiers are 1-indexed)
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

// parseModifierField reads a CSI modifier parameter that may carry the kitty
// protocol's EVENT TYPE as a sub-parameter: "5" is Ctrl on a press, "5:3" is
// Ctrl on a release. It returns the modifier code and the name suffix the event
// earns — "" for a press, ":Release" or ":Repeat" otherwise.
//
// The event type rides in the modifier field for every CSI form the protocol
// touches, not only the "u" one: an arrow key's release is CSI 1;1:3A and a
// function key's is CSI 15;1:3~. parseModifierParam stops at the colon and
// answers 1, so both of those used to read back as an unmodified PRESS —
// releases did not merely go missing, they arrived as a second keystroke. A
// held arrow key moved the cursor twice, and nothing downstream could tell.
func parseModifierField(s string) (mod int, eventSuffix string) {
	base, sub, found := strings.Cut(s, ":")
	mod = parseModifierParam(base)
	if !found {
		return mod, ""
	}
	switch parseIntParam(sub) {
	case 2:
		return mod, ":Repeat"
	case 3:
		return mod, ":Release"
	}
	return mod, ""
}

// parseIntParam parses an integer parameter string where 0 is valid
// Used for mouse button codes and coordinates where 0 is meaningful
func parseIntParam(s string) int {
	if s == "" {
		return 0
	}
	val := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			val = val*10 + int(c-'0')
		} else {
			return 0
		}
	}
	return val
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

	mod, event := parseModifierField(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName + event, true
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

	mod, event := parseModifierField(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName + event, true
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

	mod, event := parseModifierField(parts[1])
	prefix := modifierPrefix(mod)
	return prefix + baseName + event, true
}

// parseModifiedTildeKey handles ESC [ <num> ; <mod> ~
func parseModifiedTildeKey(parts []string) (string, bool) {
	tildeKeys := map[int]string{
		1:  "Home",
		2:  "Insert",
		3:  "FDel", // forward delete, matching escBindings for the bare form
		4:  "End",
		5:  "PageUp",
		6:  "PageDown",
		11: "F1",
		12: "F2",
		13: "F3",
		14: "F4",
		15: "F5",
		17: "F6",
		18: "F7",
		19: "F8",
		20: "F9",
		21: "F10",
		23: "F11",
		24: "F12",
		// F13-F20, which stopped at F12 and so could not arrive on this path
		// at all. The kitty protocol reports them (57376-57383) and the
		// vocabulary has named them since it was written, but a terminal
		// without that protocol sends them here — and an unlisted number
		// falls through to be read byte by byte, so a VT220's Help key
		// produced five keystrokes, "Escape [ 2 8 ~", typing "[28~" into the
		// application. The same failure the keypad's Enter had before SS3 M
		// was read.
		//
		// The numbering is xterm's, and it is not contiguous: the gaps at 16,
		// 22, 27, 30 and 35 are real, left by DEC. What a VT220 keyboard calls
		// Help and Do land on F15 and F16, which is where every terminal in
		// use puts them.
		25: "F13",
		26: "F14",
		28: "F15", // "Help" on a VT220 keyboard
		29: "F16", // "Do" on a VT220 keyboard
		31: "F17",
		32: "F18",
		33: "F19",
		34: "F20",
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
		mod, event := parseModifierField(parts[1])
		prefix := modifierPrefix(mod)
		return prefix + baseName + event, true
	}

	return "", false
}

// Kitty protocol special keys.
//
// These are KEYCODES, not bytes. kitty keycode 127 is the physical erase-left
// key; our name for that key is "Delete"; "Backspace" names the byte-8
// convention only.
//
// This said "Backspace", on the reasoning that 127-the-keycode identifies the
// KEY while 127-the-byte is DEL, so the two should differ. The reasoning had
// the vocabulary backwards. There is one physical erase-left key, and Delete
// IS its name — Backspace is the concession that lets a terminal sending BS (8)
// be told apart from one sending DEL (127) without either being confused with
// forward delete. Handing up the concession spelling for a channel that reports
// no byte at all named the key by a convention it was not using: the same key
// arrived as "del" over a legacy terminal and "back" over kitty, so a binding
// written for one went silent on the other.
//
// (kitty's own "DELETE" is CSI 3~, which is forward delete — our FDel — and
// arrives through the tilde path, not this table.)
var kittySpecialKeys = map[int]string{
	9:   "Tab",
	13:  "Return",
	27:  "Escape",
	32:  "Space",
	127: "Delete", // the physical erase-left key — see above
	// Functional keys (Kitty extended codes)
	57358: "CapsLock",
	57359: "ScrollLock",
	57360: "Clear", // pressed alone this never arrives: it is the pad lock
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
	// The KEYPAD, every key of it, under the "P-" prefix.
	//
	// This block was wrong twice over. It named ten keypad keys as though they
	// were the main cluster — so keypad Home could not be told from Home — and
	// it had the arrows ROTATED, because 57417 is KP_LEFT and this called it
	// "Up", 57419 is KP_UP and this called it "Left". A keypad arrow moved the
	// cursor ninety degrees from where it pointed.
	//
	// The pad is a modal duplicate of keys that exist elsewhere, so the prefix
	// says which one was pressed rather than inventing twenty names. And the
	// duplication runs the other way too: kitty reports 57406 for the 7 with
	// NumLock ON and 57423 for the same key with it OFF, which is exactly the
	// rule — the number when it is locked, the named action when it is not.
	57399: "P-0",
	57400: "P-1",
	57401: "P-2",
	57402: "P-3",
	57403: "P-4",
	57404: "P-5",
	57405: "P-6",
	57406: "P-7",
	57407: "P-8",
	57408: "P-9",
	57409: "P-.",
	57410: "P-/",
	57411: "P-*",
	57412: "P--",
	57413: "P-+",
	57414: "P-Enter", // the keypad's own, never the home row's Return
	57415: "P-=",
	// KP_SEPARATOR, and it is the LOWERCASE comma because of where that name
	// comes from. kitty resolves this one from the xkb keysym rather than a
	// scancode (glfw_key_for_sym: XKB_KEY_KP_Separator -> KP_SEPARATOR), and
	// X11's keypad keysyms are the DEC LK201's pad almost one for one — KP_F1
	// through KP_F4 exist for nothing but that keyboard's PF1-PF4, beside
	// KP_Separator, KP_Decimal, KP_Subtract and KP_Enter. The LK201 wears its
	// comma in the right-hand column directly above Enter, which is the same
	// key an AS/400 column carries (HID 133 KeypadComma, next door to 134
	// KeypadEqualSign) and the same one a modern USB pad puts beside the plus.
	// So this is the archaic comma, and "P-," stays reserved for the PC-98's,
	// which sits in the bottom row beside the 0 and the period and reaches HID
	// as International6 (140).
	57416: "p-,",
	57417: "P-Left",
	57418: "P-Right",
	57419: "P-Up",
	57420: "P-Down",
	57421: "P-PageUp",
	57422: "P-PageDown",
	57423: "P-Home",
	57424: "P-End",
	57425: "P-Insert",
	57426: "P-Delete", // the pad's own erase, not the erase-left key at 127
	57427: "P-Begin",  // the 5 with NumLock off; duplicates nothing elsewhere
}

// modifierKeyInfo holds modifier name and side (Left/Right)
type modifierKeyInfo struct {
	name string
	side string
}

// Kitty protocol modifier keys (for press/release events)
// Left modifiers are 57441-57446, Right modifiers are 57447-57452
//
// The names are this vocabulary's, not the protocol's. Kitty calls 57443 "alt"
// and 57446 "meta"; here they are Mega and Micro, because both keys have a
// genuine claim to the name Meta and neither may take it — Emacs and the PC
// keyboard call the first one Meta, X11 and the Space Cadet call the second
// one Meta, and they are each right about their own lineage. Splitting by the
// case of the prefix settles it: capital M is Mega, lowercase m is Micro.
var kittyModifierKeys = map[int]modifierKeyInfo{
	57441: {"Shift", "Left"},
	57442: {"Ctrl", "Left"},
	57443: {"Mega", "Left"},
	57444: {"Super", "Left"},
	57445: {"Hyper", "Left"},
	57446: {"Micro", "Left"},
	57447: {"Shift", "Right"},
	57448: {"Ctrl", "Right"},
	57449: {"Mega", "Right"},
	57450: {"Super", "Right"},
	57451: {"Hyper", "Right"},
	57452: {"Micro", "Right"},
}

// parseKittyProtocol handles CSI keycode ; modifiers : event_type u format
// Event types: 1=press, 2=repeat, 3=release
// csiKeyIdentity names the physical key a CSI sequence is about, and says which
// of press, repeat and release it is.
//
// The identity has to be namespaced because the CSI families identify a key
// three different ways: the "u" form by a keycode, the "~" form by a number,
// and the cursor and F1-F4 forms by their final letter alone. Those number
// spaces overlap — "CSI 3~" is forward delete and "CSI 3;5u" is Ctrl-C — so a
// bare integer would let one key consume another's entry.
//
// Reports false for anything that is not a key: mouse events, position and
// window-op replies, and the macOS Option+arrow shape that carries no event
// type at all.
func csiKeyIdentity(seq string) (identity string, eventType int, ok bool) {
	if len(seq) < 3 || seq[0] != 0x1b {
		return "", 0, false
	}
	// SS3: the same keys in application cursor mode. "ESC O A" and "CSI A" are
	// one physical key spelled two ways, so they share an identity — a terminal
	// can send the press in either mode and the release always arrives as CSI.
	// SS3 M is left out deliberately: the keypad's Enter is reported by keycode
	// under the kitty protocol, so it lives in the "u" space and pairing it
	// here would put its press and release in different namespaces.
	if seq[1] == 'O' && len(seq) == 3 {
		switch seq[2] {
		case 'A', 'B', 'C', 'D', 'H', 'F', 'P', 'Q', 'S':
			return "f:" + string(seq[2]), 1, true
		}
		return "", 0, false
	}
	if seq[1] != '[' {
		return "", 0, false
	}
	body := seq[2:]
	final := body[len(body)-1]
	switch final {
	case 'u', '~', 'A', 'B', 'C', 'D', 'H', 'F', 'E', 'P', 'Q', 'S':
	default:
		return "", 0, false
	}

	params := strings.Split(body[:len(body)-1], ";")
	first := params[0]
	if i := strings.IndexByte(first, ':'); i >= 0 {
		first = first[:i]
	}

	// The event type rides as a sub-parameter of the MODIFIER field, in every
	// family — that is what makes one rule possible here at all.
	eventType = 1
	if len(params) >= 2 {
		if i := strings.IndexByte(params[1], ':'); i >= 0 {
			if et, err := strconv.Atoi(params[1][i+1:]); err == nil {
				eventType = et
			}
		}
	}

	switch final {
	case 'u':
		// The modifier keys report themselves in their own shape ("LMod:S"),
		// carry no chord name to hold, and are the one thing whose release is
		// the event rather than the end of one.
		if _, isModifier := kittyModifierKeys[parseModifierParam(first)]; isModifier {
			return "", 0, false
		}
		return "u:" + first, eventType, true
	case '~':
		return "~:" + first, eventType, true
	default:
		return "f:" + string(final), eventType, true
	}
}

// holdByteKey remembers a name for a press that arrived as a plain BYTE.
//
// With event reporting on but disambiguation off — what a host asks for when it
// wants presses to stay byte-identical — a text key is split across two
// channels: it goes DOWN as a byte and comes UP as "CSI <keycode> ; <mod> : 3
// u". Nothing else in this package sees both, so without this the press of
// every letter, digit and symbol went unrecorded and its release matched
// nothing.
//
// The identity is the keycode kitty will use, which is the key's BASE
// character: the lowercase letter for a letter, and for a control byte the
// letter it is Control of, since Ctrl-L is the "l" key with a modifier. Shifted
// punctuation cannot be mapped back without knowing the layout — "%" is Shift+5
// only on some keyboards — so those simply do not match, and the release falls
// back to the name derived from the sequence rather than being lost.
func (h *Handler) holdByteKey(b byte, name string) {
	var keycode int
	switch {
	case b >= 0x01 && b <= 0x1A:
		keycode = int('a' + b - 1) // Ctrl-A..Ctrl-Z are the letter keys
	case b >= 'A' && b <= 'Z':
		keycode = int(b - 'A' + 'a')
	case b >= 32 && b < 127:
		keycode = int(b)
	default:
		return
	}
	h.mu.Lock()
	h.heldKeys["u:"+strconv.Itoa(keycode)] = name
	h.mu.Unlock()
}

// recordHeld remembers a name as this key's, for a press that reached the
// caller by a route with no event type to read — the literal-sequence table.
func (h *Handler) recordHeld(seq, name string) {
	identity, _, ok := csiKeyIdentity(seq)
	if !ok {
		return
	}
	h.mu.Lock()
	h.heldKeys[identity] = name
	h.mu.Unlock()
}

// reconcileHeld records a press and replays it for the repeats and the release
// that follow, so all three name one key the same way.
//
// A release with nothing recorded is DROPPED. That is safe by construction
// rather than by luck: heldKeys holds exactly what this package emitted, so an
// absent entry means no press was ever reported for that key and nothing
// downstream can be holding it. Passing the derived name through instead is
// what produced the mismatch in the first place.
func (h *Handler) reconcileHeld(seq, name string) (string, bool) {
	identity, eventType, ok := csiKeyIdentity(seq)
	if !ok {
		return name, true
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	switch eventType {
	case 3: // release
		held, ok := h.heldKeys[identity]
		if !ok {
			// Dropping rests on "no entry means no press was emitted", and
			// that holds only where a press is ALWAYS a sequence — the cursor,
			// tilde and F1-F4 families. The "u" family is different: with
			// disambiguation off a text key goes down as a byte, and although
			// holdByteKey records what it can, a shifted punctuation key
			// cannot be mapped back to its keycode without knowing the layout.
			// Dropping there would lose a real release, so the derived name
			// stands instead. That is the old behaviour, kept exactly where
			// the premise for replacing it does not hold.
			if strings.HasPrefix(identity, "u:") {
				return name, true
			}
			// Consumed and emitted as nothing — NOT "not a key". Answering
			// false sends the caller back to read the sequence byte by byte,
			// which turns a dropped release into a phantom Escape followed by
			// its digits typed as text.
			return "", true
		}
		delete(h.heldKeys, identity)
		return held + ":Release", true
	case 2: // repeat
		// A held key whose modifiers change mid-hold would otherwise start
		// repeating under a different name than it went down with.
		if held, ok := h.heldKeys[identity]; ok {
			return held + ":Repeat", true
		}
		return name, true
	default: // press
		// A press that emitted NOTHING is not held. The lock cap is eaten
		// (see numlock.go) and comes back as an empty name; recording it would
		// put an empty entry under its identity, and the release that matched
		// it would then come out as a bare ":Release" for a key nobody was
		// ever told about — the very mismatch the registry exists to stop.
		if name != "" {
			h.heldKeys[identity] = name
		}
		return name, true
	}
}

// handleFocusReport acts on the terminal's focus notification.
//
// Losing focus releases everything still down FIRST, before the callback, so a
// consumer that reacts to the focus change is not still holding keys when it
// does. This is the one case dropping an unmatched release cannot cover: the
// key-up goes to whoever has the keyboard now, so it never arrives here.
func (h *Handler) handleFocusReport(focused bool) {
	if !focused {
		h.ReleaseHeldKeys()
		h.forgetModifierSides()
	}
	// Focus is a state as much as an event, and something watching the modes
	// for a status bar should not also have to wire up OnFocus. A game that
	// pauses when the window goes away can read it either way.
	if h.noteFocus(focused) {
		h.announceMode(Mode{ModeFocus, modeValue(focused)})
	}
	if h.OnFocus != nil {
		h.OnFocus(focused)
	}
}

// ReleaseHeldKeys reports a release for every key still down and forgets them,
// returning the names it emitted.
//
// A host calls this when it knows the keyboard has gone away — focus lost, a
// window deactivated — because in that case the terminal will never send the
// releases: the key is let go while someone else is listening. Without it the
// press stands forever downstream, which is the one way dropping an unmatched
// release could strand a key. This package cannot detect that moment itself,
// having no focus reporting of its own, so it offers the mechanism to whoever
// does know.
func (h *Handler) ReleaseHeldKeys() []string {
	h.mu.Lock()
	held := h.heldKeys
	h.heldKeys = make(map[string]string)
	h.mu.Unlock()

	names := make([]string, 0, len(held))
	for _, name := range held {
		names = append(names, name+":Release")
	}
	sort.Strings(names) // a map has no order; releases should not be a lottery

	// The mouse's buttons are held the same way and are let go of for the same
	// reason: the pointer went somewhere else, and the button-up will be
	// delivered there.
	names = append(names, h.releaseHeldMouse()...)

	for _, n := range names {
		h.emitKey(n)
	}
	return names
}

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

	// Every key says something about the pad's lock, so every key is read for
	// it before anything else looks at the key itself. The lock decides what
	// eleven other caps are CALLED, so it has to be current by the time one of
	// them is named — and the keystroke that settles whether this system even
	// has a lock is a pad key, not the lock cap. See numlock.go.
	if changed, on := h.noteNumLock(keycode, mod); changed {
		h.announceNumLock(on)
	}

	// The other latch in the same field. It names no key — a capital arrives
	// as a capital — so nothing below reads it; it is here to be published.
	if changed, on := h.noteCapsLock(mod); changed {
		h.announceMode(Mode{ModeCapsLock, modeValue(on)})
	}

	// The lock cap pressed alone is not a key: it moves the lock and is eaten.
	// With a modifier held it is an ordinary key called Clear, and falls
	// through to be named like any other.
	//
	// Every event type is eaten, not just the press — a repeat and a release of
	// something that was never emitted must not be emitted either. Only the
	// press moves the lock; a hold does not ratchet it.
	if keycode == kittyClearKey && !heldModifier(mod) {
		if eventType == 1 {
			if changed, on := h.toggleNumLock(); changed {
				h.announceNumLock(on)
			}
		}
		// Consumed, emitting nothing — NOT "not a key". Answering false sends
		// the caller back to read the sequence byte by byte, which turns this
		// into a phantom Escape followed by its digits typed as text.
		return "", true
	}

	// A doubled side modifier means Hyper, which no keyboard here has a cap
	// for. Done before anything reads the modifiers, so every formatter below
	// sees the promoted set rather than the pair that produced it.
	mod = h.promoteHyper(mod)

	// And a dual-legend pad cap is resolved against the lock we keep, on a
	// system that has none of its own. The terminal cannot do it there and
	// sends the locked keycode always; without this the simulated lock moves a
	// number and changes nothing anyone can see. See numlock.go.
	keycode = h.applyPadLock(keycode)

	// Check if this is a modifier key press/release
	if modKeyInfo, ok := kittyModifierKeys[keycode]; ok {
		// Which SIDE went down is the whole basis of the Hyper promotion, and
		// these events are the only place the protocol says it. See hyper.go.
		h.noteModifierSide(modKeyInfo.name, modKeyInfo.side, eventType)

		// A modifier pressed on its OWN, named by side and by the letter its
		// prefix uses: "LMod:S" is the left Shift key going down, "RMod:C" the
		// right Control, "Mod:m" a Micro whose producer cannot say which side
		// it was (or which has only one).
		//
		// The letter has to be the prefix letter. A consumer that knows "M-"
		// reads Mega must not meet a second spelling for the same key here —
		// this once emitted "A" for Mega, a prefix the vocabulary does not
		// have, so key-sequence-processor could never match it.
		//
		// There is no repeat. Holding a modifier says nothing a repeat could
		// add: the press already said it went down and the release will say it
		// came up, and unlike a letter — where a repeat means "type another
		// one" — there is no second anything to report. Kitty rarely sends one
		// in any case, since most systems do not auto-repeat modifiers.
		var letter string
		switch modKeyInfo.name {
		case "Shift":
			letter = "S"
		case "Ctrl":
			letter = "C"
		case "Mega":
			letter = "M"
		case "Super":
			letter = "s"
		case "Micro":
			letter = "m"
		case "Hyper":
			letter = "H"
		}
		if eventType == 2 {
			return "", true // consumed, emitting nothing — see above
		}
		name := sideMod(modKeyInfo.side) + ":" + letter
		if eventType == 3 {
			name += ":Release"
		}
		return name, true
	}

	// Build event suffix for non-modifier keys (only for release, press is default)
	eventSuffix := ""
	if eventType == 3 {
		eventSuffix = ":Release"
	} else if eventType == 2 {
		eventSuffix = ":Repeat"
	}

	// Glyph modifier (private extension; kitty modifier bit value 256). A host
	// that composes an AltGr / ISO_Level3_Shift character can carry it as
	// CSI <glyph-codepoint> ; 257 u so it reaches the application as a distinct
	// "G-" chord rather than an anonymous typed character (letting a keymap
	// intercept e.g. G-€ while an unbound G-€ still self-inserts "€"). Glyph's
	// whole purpose is an alternate produced character, so the keycode IS that
	// glyph; surface it as "G-<glyph>" with any co-held standard modifiers
	// preserved. Handled before the letter/symbol/number formatters because
	// those re-derive a key from an ASCII base, which a composed glyph is not.
	if bits := mod - 1; bits&256 != 0 && keycode >= 32 && keycode < 0x110000 {
		return "G-" + modifierPrefix((bits&^256)+1) + string(rune(keycode)) + eventSuffix, true
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

	// Control on a SHOWN keypad key takes the caret, written against the
	// character the key shows: Ctrl on the pad's 7 is "P-^7", not "C-P-7".
	// That is how this vocabulary spells Control for a key that is shown
	// rather than named — the main number row already does it in
	// formatNumberKey — and the pad prefix sits outside the caret, which is
	// where the canonical order puts it (C- G- M- m- S- s- H- P- p- ^Key).
	//
	// A NAMED pad key keeps "C-", because a name has no character for the
	// caret to sit against: "C-P-Home", not "P-^Home".
	if pad, shown, isShown := splitPadShownKey(baseName); isShown && (mod-1)&4 != 0 {
		withoutCtrl := modifierPrefix(((mod - 1) &^ 4) + 1)
		return withoutCtrl + pad + "^" + shown + eventSuffix, true
	}

	prefix := modifierPrefix(mod)
	return prefix + baseName + eventSuffix, true
}

// splitPadShownKey separates a keypad name into its prefix and the character
// the key shows, reporting false for a pad key that is NAMED rather than shown.
//
// The test is the remainder's length: every shown pad key is one character
// (a digit, ".", "/", "*", "-", "+", "=", ","), and every named one is a word
// (Home, Enter, Begin, Delete). Nothing else in the table starts with a pad
// prefix, so a non-pad name falls straight through.
func splitPadShownKey(name string) (pad, shown string, ok bool) {
	for _, p := range []string{"P-", "p-"} {
		if rest, found := strings.CutPrefix(name, p); found {
			if len([]rune(rest)) == 1 {
				return p, rest, true
			}
			return "", "", false
		}
	}
	return "", "", false
}

// formatLetterKey formats a letter key with modifiers
func formatLetterKey(letter byte, mod int) string {
	if mod < 1 {
		mod = 1
	}
	mod--

	hasShift := mod&1 != 0
	hasCtrl := mod&4 != 0

	// Control is spelled with the caret when the key it pairs with is one the
	// caret is natural for — a letter, here. That choice depends on the BASE
	// KEY and never on what else is held: Ctrl+A is "^A" and Ctrl+Shift+A is
	// "S-^A", not "C-S-A". Adding a modifier does not change how Control is
	// written.
	//
	// Ctrl+Shift+letter only exists on a terminal speaking the kitty protocol,
	// or on a graphical host. A legacy terminal encodes Ctrl+letter as an ASCII
	// control code — 26 values, no room for a Shift bit — so Ctrl+Shift+A
	// arrives there as a plain "^A" and finds the "^A" binding, which is the
	// right degradation and works only because the two are not merged.
	keyPart := string(letter)
	if hasCtrl {
		keyPart = "^" + string(letter-32)
	} else if hasShift {
		// Without Control, Shift is carried by the letter's own case.
		keyPart = string(letter - 32)
	}

	// Canonical order. The caret already sits against the letter, so every
	// other modifier precedes it in rank: M- m- S- s- H- ^A.
	m := modsFromKitty(mod + 1)
	// The letter's own case carries Shift, and Control is spent on the caret —
	// except that "^A" spent the case on Control, so Shift needs saying again.
	m.ctrl = false
	m.shift = hasCtrl && hasShift

	return m.prefix() + keyPart
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
// The comparison is against the keycode itself, NOT byte(keycode).
//
// Truncating threw away everything above the low eight bits, so any keycode
// whose last byte happened to land on one of these characters was claimed here
// — and this test runs BEFORE kittySpecialKeys, so the claim won. Keypad 4 is
// 57403, whose low byte is ';', and keypad 6 is 57405, whose low byte is '=':
// both typed a symbol instead of moving the cursor. F20 (57383, low byte '\”)
// typed an apostrophe. Every one of these cases is silent, because what comes
// out is a real key that a keymap will happily bind.
//
// All eleven characters are ASCII, so comparing the int is exact and the
// byte() conversion in formatSymbolKey stays safe: nothing reaches it now
// without being one of these.
func isSymbolKey(keycode int) bool {
	switch keycode {
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

	m := modsFromKitty(mod + 1)

	displayChar := symbol
	if m.shift {
		if shifted, ok := symbolShiftMap[symbol]; ok {
			displayChar = shifted
		}
	}

	var keyPart string
	if m.ctrl {
		keyPart = "^" + string(displayChar)
	} else {
		keyPart = string(displayChar)
	}

	// Shift is absorbed into the shifted character and Control into the caret,
	// so both are spent. Everything else is spelled.
	m.shift, m.ctrl = false, false
	return m.prefix() + keyPart
}

// formatNumberKey formats a number key with modifiers
func formatNumberKey(number byte, mod int) string {
	if mod < 1 {
		mod = 1
	}
	mod--

	m := modsFromKitty(mod + 1)

	displayChar := number
	if m.shift {
		if shifted, ok := numberShiftMap[number]; ok {
			displayChar = shifted
		}
	}

	var keyPart string
	if m.ctrl {
		keyPart = "^" + string(displayChar)
	} else {
		keyPart = string(displayChar)
	}

	// As above: Shift and Control are spent, the rest are spelled.
	m.shift, m.ctrl = false, false
	return m.prefix() + keyPart
}

// parseMouseSGR parses SGR mouse sequences: ESC [ < Cb ; Cx ; Cy M/m
// Returns the keys to emit, in order, and a success flag.
func (h *Handler) parseMouseSGR(seq string) ([]string, bool) {
	// Must start with ESC [ <
	if len(seq) < 6 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != '<' {
		return nil, false
	}

	// Final byte must be M (press) or m (release)
	finalByte := seq[len(seq)-1]
	if finalByte != 'M' && finalByte != 'm' {
		return nil, false
	}
	isRelease := finalByte == 'm'

	// Parse parameters: Cb;Cx;Cy
	params := seq[3 : len(seq)-1]
	parts := splitCSIParams(params)
	if len(parts) != 3 {
		return nil, false
	}

	cb := parseIntParam(parts[0])
	cx := parseIntParam(parts[1])
	cy := parseIntParam(parts[2])

	return h.mouseKeys(cb, cx, cy, isRelease), true
}

// parseMouseX10 parses X10 mouse sequences: ESC [ M Cb Cx Cy
// Returns the keys to emit, in order, and a success flag.
func (h *Handler) parseMouseX10(seq string) ([]string, bool) {
	// Must be exactly ESC [ M followed by 3 bytes
	if len(seq) != 6 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != 'M' {
		return nil, false
	}

	// Decode button and coordinates (all have 32 added)
	cb := int(seq[3]) - 32
	cx := int(seq[4]) - 32
	cy := int(seq[5]) - 32

	// X10 protocol: button code 3 means release, and does not say of what
	isRelease := (cb & 3) == 3

	return h.mouseKeys(cb, cx, cy, isRelease), true
}

// mouseButtons names the three buttons both encodings can count, in their
// order: bits 0, 1 and 2 of the button code.
var mouseButtons = [3]string{"MouseLeft", "MouseMiddle", "MouseRight"}

// mouseKeys turns one decoded mouse report into the keys it should be
// reported as, in the order they should be emitted.
//
// A press and a scroll report a position first — Mouse@x,y — and then what
// happened there. A drag carries its position in the name instead, because a
// drag IS its position: the pointer moving is the whole event. Motion with no
// button down is the same thing minus the button, which leaves just the
// position, so that is all it reports.
//
// A release is emitted under the name its PRESS was emitted under, not one
// derived again from the modifiers as they stand now. Let go of Control before
// the button and the two would otherwise not match; and a release for a button
// this package never reported down is not emitted at all, since nothing
// downstream can believe it is held.
func (h *Handler) mouseKeys(cb, cx, cy int, isRelease bool) []string {
	// The mouse encodings carry only these three modifiers, in bit positions of
	// their own that have nothing to do with the kitty keyboard field.
	prefix := keyMods{
		shift: cb&4 != 0,
		mega:  cb&8 != 0,
		ctrl:  cb&16 != 0,
	}.prefix()

	buttonBits := cb & 3
	isMotion := (cb & 32) != 0
	isScroll := (cb & 64) != 0
	posKey := fmt.Sprintf("Mouse@%d,%d", cx, cy)

	switch {
	case isScroll:
		// Scroll wheel. The low two bits select the wheel axis/direction:
		// 0 = up, 1 = down, 2 = left, 3 = right (SGR buttons 64..67).
		var action string
		switch buttonBits {
		case 0:
			action = "MouseScrollUp"
		case 1:
			action = "MouseScrollDown"
		case 2:
			action = "MouseScrollLeft"
		case 3:
			action = "MouseScrollRight"
		}
		return []string{posKey, prefix + action}

	case isMotion:
		// Bits of 3 mean the pointer moved with no button down, and that is
		// not an action at all: it is nothing but where the pointer now is,
		// which is exactly what Mouse@x,y says.
		if buttonBits > 2 {
			return []string{posKey}
		}
		// A drag names the button being dragged with.
		action := "MouseDrag" + strings.TrimPrefix(mouseButtons[buttonBits], "Mouse")
		return []string{fmt.Sprintf("%s%s@%d,%d", prefix, action, cx, cy)}

	case isRelease:
		h.mu.Lock()
		defer h.mu.Unlock()
		keys := []string{posKey}
		if buttonBits < 3 {
			// The encoding named the button, so only that one comes up.
			if held := h.mouseHeld[buttonBits]; held != "" {
				h.mouseHeld[buttonBits] = ""
				keys = append(keys, held+":Release")
			}
			return keys
		}
		// X10's release names no button, so every button this package has
		// reported down comes up — one release each, never a bare one.
		for i, held := range h.mouseHeld {
			if held != "" {
				h.mouseHeld[i] = ""
				keys = append(keys, held+":Release")
			}
		}
		return keys

	default:
		if buttonBits > 2 {
			return []string{posKey}
		}
		name := prefix + mouseButtons[buttonBits]
		h.mu.Lock()
		h.mouseHeld[buttonBits] = name
		h.mu.Unlock()
		return []string{posKey, name}
	}
}

// releaseHeldMouse takes every button this package has reported down and
// returns the releases for them, leaving none held.
func (h *Handler) releaseHeldMouse() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var names []string
	for i, held := range h.mouseHeld {
		if held != "" {
			h.mouseHeld[i] = ""
			names = append(names, held+":Release")
		}
	}
	return names
}
