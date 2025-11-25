// Package keyboard provides a buffered direct keyboard handler with VT100 support and Unicode/Emoji support.
//
// This implementation:
// 1. Buffers all incoming input to prevent data loss during paste operations
// 2. Processes buffered input asynchronously
// 3. Maintains proper escape sequence handling
// 4. Returns individual keys/sequences via GetKey() while preserving paste data
// 5. Properly handles Unicode characters including emoji and high ASCII
package keyboard

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// DebugCallback is a function type for debug logging
type DebugCallback func(message string)

// DirectKeyboardHandler handles buffered direct keyboard input with VT100 support
type DirectKeyboardHandler struct {
	escBindings map[string]string
	debugLog    DebugCallback

	// Input buffering
	inputBuffer []byte
	keyQueue    []string
	bufferMu    sync.Mutex
	queueMu     sync.Mutex

	isProcessing bool
	processingMu sync.Mutex

	isListening bool
	listenMu    sync.Mutex

	// For stopping the listener
	stopChan chan struct{}

	// Original terminal state for restoration
	oldState *term.State

	// Channel to signal new keys available
	keyChan chan struct{}
}

// NewDirectKeyboardHandler creates a new DirectKeyboardHandler with an optional debug callback
func NewDirectKeyboardHandler(debugCallback DebugCallback) *DirectKeyboardHandler {
	handler := &DirectKeyboardHandler{
		escBindings: make(map[string]string),
		debugLog:    debugCallback,
		inputBuffer: make([]byte, 0),
		keyQueue:    make([]string, 0),
		stopChan:    make(chan struct{}),
		keyChan:     make(chan struct{}, 1),
	}

	if handler.debugLog == nil {
		handler.debugLog = func(message string) {}
	}

	// Initialize ANSI/VT100 terminal escape sequences
	handler.initializeEscBindings()

	return handler
}

// initializeEscBindings initializes common escape key bindings for terminal sequences
func (h *DirectKeyboardHandler) initializeEscBindings() {
	// Common escape sequences for VT100/ANSI terminals
	commonBindings := []struct {
		key    string
		action string
	}{
		// Arrow keys (common formats)
		{"[A", "up"},
		{"[B", "down"},
		{"[C", "right"},
		{"[D", "left"},
		{"[1;1A", "^up"},
		{"[1;1B", "^down"},
		{"[1;1C", "^right"},
		{"[1;1D", "^left"},
		{"[Z", "S-tab"},

		{"O1P", "H-F1"}, // Alternative format
		{"O1Q", "H-F2"},
		{"O1R", "H-F3"},
		{"O1S", "H-F4"},
		{"O2P", "S-F1"}, // Alternative format
		{"O2Q", "S-F2"},
		{"O2R", "S-F3"},
		{"O2S", "S-F4"},
		{"O3P", "M-F1"}, // Alternative format
		{"O3Q", "M-F2"},
		{"O3R", "M-F3"},
		{"O3S", "M-F4"},
		{"O4P", "S-M-F1"}, // Alternative format
		{"O4Q", "S-M-F2"},
		{"O4R", "S-M-F3"},
		{"O4S", "S-M-F4"},
		{"O5P", "^F1"}, // Alternative format
		{"O5Q", "^F2"},
		{"O5R", "^F3"},
		{"O5S", "^F4"},
		{"O6P", "S-^F1"}, // Alternative format
		{"O6Q", "S-^F2"},
		{"O6R", "S-^F3"},
		{"O6S", "S-^F4"},
		{"O7P", "M-^F1"}, // Alternative format
		{"O7Q", "M-^F2"},
		{"O7R", "M-^F3"},
		{"O7S", "M-^F4"},
		{"O8P", "S-M-^F1"}, // Alternative format
		{"O8Q", "S-M-^F2"},
		{"O8R", "S-M-^F3"},
		{"O8S", "S-M-^F4"},

		{"OA", "up"}, // Alternative format
		{"OB", "down"},
		{"OC", "right"},
		{"OD", "left"},

		// Function keys
		{"OP", "F1"},
		{"OQ", "F2"},
		{"OR", "F3"},
		{"OS", "F4"},
		{"3P", "^F1"},
		{"3Q", "^F2"},
		{"3R", "^F3"},
		{"3S", "^F4"},
		{"[15~", "F5"},
		{"[17~", "F6"},
		{"[18~", "F7"},
		{"[19~", "F8"},
		{"[20~", "F9"},
		{"[21~", "F10"},
		{"[23~", "F11"},
		{"[24~", "F12"},

		// Special Meta+Function key bindings
		{"EOP", "M-F1"},
		{"EOQ", "M-F2"},
		{"EOR", "M-F3"},
		{"EOS", "M-F4"},
		{"E[15~", "M-F5"},
		{"E[17~", "M-F6"},
		{"E[18~", "M-F7"},
		{"E[19~", "M-F8"},
		{"E[20~", "M-F9"},
		{"E[21~", "M-F10"},
		{"E[23~", "M-F11"},
		{"E[24~", "M-F12"},

		// Navigation keys
		{"[H", "home"},
		{"[F", "end"},
		{"[5~", "pgup"},
		{"[6~", "pgdn"},
		{"[2~", "ins"},
		{"[3~", "fdel"},

		// Common vi/emacs key bindings when used with Esc
		{"b", "esc b"},
		{"f", "esc f"},
		{"h", "esc h"},
		{"j", "esc j"},
		{"k", "esc k"},
		{"l", "esc l"},
		{"w", "esc w"},
		{"d", "esc d"},
		{"x", "esc x"},
		{"y", "esc y"},
		{"p", "esc p"},
		{"u", "esc u"},
		{"i", "esc i"},
		{"a", "esc a"},
		{"o", "esc o"},
		{"O", "esc O"},
		{"0", "esc 0"},
		{"$", "esc $"},
		{"G", "esc G"},
		{"/", "esc /"},
		{"n", "esc n"},
		{"N", "esc N"},
		{"v", "esc v"},
		{"V", "esc V"},

		// Meta key combinations - Alt+key in most terminals sends ESC+key
		{"1", "M-1"},
		{"2", "M-2"},
		{"3", "M-3"},
		{"4", "M-4"},
		{"5", "M-5"},
		{"6", "M-6"},
		{"7", "M-7"},
		{"8", "M-8"},
		{"9", "M-9"},
		{"0", "M-0"},
		{"-", "M--"},
		{"=", "M-="},
		{"!", "M-!"},
		{"@", "M-@"},
		{"#", "M-#"},
		{"$", "M-$"},
		{"%", "M-%"},
		{"^", "M-^"},
		{"&", "M-&"},
		{"*", "M-*"},
		{"(", "M-("},
		{")", "M-)"},
		{"_", "M-_"},
		{"+", "M-+"},
		{"[", "M-["},
		{"]", "M-]"},
		{"{", "M-{"},
		{"}", "M-}"},
		{"\\", "M-\\"},
		{"|", "M-|"},
		{";", "M-;"},
		{"'", "M-'"},
		{"\"", "M-\""},
		{",", "M-,"},
		{".", "M-."},
		{"/", "M-/"},
		{"<", "M-<"},
		{">", "M->"},
		{"?", "M-?"},
		{"`", "M-`"},
		{"~", "M-~"},
	}

	// Add lowercase and uppercase letters with M- prefix
	for i := 97; i <= 122; i++ { // a-z
		char := string(rune(i))
		commonBindings = append(commonBindings, struct {
			key    string
			action string
		}{char, "M-" + char})
	}

	for i := 65; i <= 90; i++ { // A-Z
		char := string(rune(i))
		commonBindings = append(commonBindings, struct {
			key    string
			action string
		}{char, "M-" + char})
	}

	// Add all the bindings to our map
	for _, binding := range commonBindings {
		h.escBindings[binding.key] = binding.action
	}
}

// getUTF8CharLength determines the number of bytes needed for a UTF-8 character based on the first byte
func (h *DirectKeyboardHandler) getUTF8CharLength(firstByte byte) int {
	if firstByte < 0x80 {
		return 1 // ASCII (0xxxxxxx)
	}
	if firstByte < 0xC0 {
		return 1 // Invalid UTF-8, treat as single byte
	}
	if firstByte < 0xE0 {
		return 2 // 2-byte character (110xxxxx)
	}
	if firstByte < 0xF0 {
		return 3 // 3-byte character (1110xxxx)
	}
	if firstByte < 0xF8 {
		return 4 // 4-byte character (11110xxx)
	}
	return 1 // Invalid, treat as single byte
}

// extractUnicodeChar extracts a Unicode character from the buffer
// Returns the character, bytes used, and a boolean indicating success
func (h *DirectKeyboardHandler) extractUnicodeChar() (string, int, bool) {
	if len(h.inputBuffer) == 0 {
		return "", 0, false
	}

	firstByte := h.inputBuffer[0]
	charLength := h.getUTF8CharLength(firstByte)

	// Check if we have enough bytes for this character
	if len(h.inputBuffer) < charLength {
		return "", 0, false // Need more bytes
	}

	// Extract the bytes for this character
	charBytes := h.inputBuffer[:charLength]

	// Convert to string using UTF-8 decoding
	char := string(charBytes)

	// Verify it's a valid character (not a replacement character from invalid UTF-8)
	if utf8.ValidString(char) && char != "\uFFFD" {
		return char, charLength, true
	}

	// If invalid UTF-8, treat the first byte as a single character
	return string(rune(firstByte)), 1, true
}

// StartListening starts listening for input and buffering it
func (h *DirectKeyboardHandler) StartListening() {
	h.listenMu.Lock()
	if h.isListening {
		h.listenMu.Unlock()
		return
	}

	h.isListening = true
	h.stopChan = make(chan struct{})
	h.listenMu.Unlock()

	// Set stdin to raw mode
	var err error
	h.oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		h.debugLog(fmt.Sprintf("Failed to set raw mode: %v", err))
		h.listenMu.Lock()
		h.isListening = false
		h.listenMu.Unlock()
		return
	}

	h.debugLog("Started listening for input")

	// Start the input reader goroutine
	go h.readInput()
}

// StopListening stops listening for input
func (h *DirectKeyboardHandler) StopListening() {
	h.listenMu.Lock()
	if !h.isListening {
		h.listenMu.Unlock()
		return
	}

	h.isListening = false
	close(h.stopChan)
	h.listenMu.Unlock()

	// Restore terminal state
	if h.oldState != nil {
		if err := term.Restore(int(os.Stdin.Fd()), h.oldState); err != nil {
			h.debugLog(fmt.Sprintf("Error restoring terminal state: %v", err))
		}
	}

	h.debugLog("Stopped listening for input")
}

// readInput reads from stdin and buffers the input
func (h *DirectKeyboardHandler) readInput() {
	buf := make([]byte, 256)

	for {
		select {
		case <-h.stopChan:
			return
		default:
			// Set a read deadline to allow checking stopChan periodically
			// Note: os.Stdin doesn't support SetReadDeadline, so we rely on the raw mode read
			n, err := os.Stdin.Read(buf)
			if err != nil {
				h.debugLog(fmt.Sprintf("Error reading input: %v", err))
				return
			}

			if n > 0 {
				h.handleInputData(buf[:n])
			}
		}
	}
}

// handleInputData handles incoming input data by buffering it
func (h *DirectKeyboardHandler) handleInputData(data []byte) {
	// Append to input buffer
	h.bufferMu.Lock()
	h.inputBuffer = append(h.inputBuffer, data...)
	bufLen := len(h.inputBuffer)
	h.bufferMu.Unlock()

	h.debugLog(fmt.Sprintf("Buffered %d bytes, total buffer: %d bytes", len(data), bufLen))

	// Process buffer asynchronously to avoid blocking
	h.processingMu.Lock()
	isProcessing := h.isProcessing
	h.processingMu.Unlock()

	if !isProcessing {
		go h.processBuffer()
	}
}

// processBuffer processes the input buffer and extracts key sequences
func (h *DirectKeyboardHandler) processBuffer() {
	h.processingMu.Lock()
	if h.isProcessing {
		h.processingMu.Unlock()
		return
	}

	h.bufferMu.Lock()
	bufLen := len(h.inputBuffer)
	h.bufferMu.Unlock()

	if bufLen == 0 {
		h.processingMu.Unlock()
		return
	}

	h.isProcessing = true
	h.processingMu.Unlock()

	defer func() {
		h.processingMu.Lock()
		h.isProcessing = false
		h.processingMu.Unlock()
	}()

	for {
		h.bufferMu.Lock()
		bufLen := len(h.inputBuffer)
		h.bufferMu.Unlock()

		if bufLen == 0 {
			break
		}

		result := h.extractNextKey()
		if result != "" {
			h.queueMu.Lock()
			h.keyQueue = append(h.keyQueue, result)
			h.queueMu.Unlock()

			// Signal that a new key is available
			select {
			case h.keyChan <- struct{}{}:
			default:
			}
		} else {
			// If we can't extract a key, break to avoid infinite loop
			break
		}
	}
}

// extractNextKey extracts the next key or key sequence from the buffer
func (h *DirectKeyboardHandler) extractNextKey() string {
	h.bufferMu.Lock()
	if len(h.inputBuffer) == 0 {
		h.bufferMu.Unlock()
		return ""
	}

	firstByte := h.inputBuffer[0]
	h.bufferMu.Unlock()

	const ESC = 27

	// Handle escape sequences
	if firstByte == ESC {
		return h.extractEscapeSequence()
	}

	// Handle Unicode characters (including ASCII)
	h.bufferMu.Lock()
	char, bytesUsed, ok := h.extractUnicodeChar()
	if !ok {
		h.bufferMu.Unlock()
		// Need more bytes for this character
		return ""
	}

	// Remove the processed bytes
	h.inputBuffer = h.inputBuffer[bytesUsed:]
	h.bufferMu.Unlock()

	// Handle control characters and special cases for ASCII
	if len(char) == 1 {
		code := int(char[0])
		if code <= 31 || code == 127 {
			// Control character - use existing logic
			key := h.processControlKey(code)
			h.debugLog(fmt.Sprintf("Extracted control key: %s (code %d)", key, code))
			return key
		}
	}

	// For regular Unicode characters (including high ASCII), return as-is
	hexCodes := make([]string, len(char))
	for i, b := range []byte(char) {
		hexCodes[i] = fmt.Sprintf("%02x", b)
	}
	h.debugLog(fmt.Sprintf("Extracted Unicode character: \"%s\" (%d bytes, codes: %s)", char, bytesUsed, strings.Join(hexCodes, " ")))
	return char
}

// extractEscapeSequence extracts an escape sequence from the buffer
func (h *DirectKeyboardHandler) extractEscapeSequence() string {
	const ESC = 27

	h.bufferMu.Lock()
	if len(h.inputBuffer) < 1 || h.inputBuffer[0] != ESC {
		h.bufferMu.Unlock()
		return ""
	}

	// If we only have ESC, we need to wait a bit to see if more comes
	if len(h.inputBuffer) == 1 {
		h.bufferMu.Unlock()

		// Use a small delay to distinguish standalone ESC from escape sequences
		time.Sleep(10 * time.Millisecond)

		h.bufferMu.Lock()
		if len(h.inputBuffer) == 1 && h.inputBuffer[0] == ESC {
			// Still just ESC - treat as standalone
			h.inputBuffer = h.inputBuffer[1:]
			h.bufferMu.Unlock()
			h.debugLog("Extracted standalone ESC key")
			return "esc"
		}
		h.bufferMu.Unlock()

		// More data arrived - try to extract sequence recursively
		return h.extractEscapeSequence()
	}

	// Try to find a complete escape sequence by finding the LONGEST possible match
	rawSequence := string(h.inputBuffer)
	var bestMatch string
	bestLength := 1

	// FIRST: Check for function keys and other multi-character sequences from longest to shortest
	// This prevents '[' from matching before '[15~' can be checked
	maxLen := len(h.inputBuffer)
	if maxLen > 20 {
		maxLen = 20
	}

	// Try from longest to shortest to prioritize complete sequences
	for length := maxLen; length >= 2; length-- {
		testSequence := rawSequence[:length]
		escPart := testSequence[1:]

		// Check for exact matches in our escape bindings
		if action, ok := h.escBindings[escPart]; ok {
			// Special handling: if this starts with '[', make sure it's a complete sequence
			if strings.HasPrefix(escPart, "[") {
				// For '[' sequences, only accept if it ends with a letter or ~ (complete)
				// OR if it's a single character (like for M-[)
				matched, _ := regexp.MatchString(`[A-Za-z~]$`, escPart)
				if len(escPart) == 1 || matched {
					bestMatch = action
					bestLength = length
					h.debugLog(fmt.Sprintf("Found exact escape binding: ESC%s → %s", escPart, bestMatch))
					break
				}
			} else {
				// Non-[ sequences can be matched immediately
				bestMatch = action
				bestLength = length
				h.debugLog(fmt.Sprintf("Found exact escape binding: ESC%s → %s", escPart, bestMatch))
				break
			}
		}

		// Check for CSI sequences for this length
		csiMatch := h.parseCSISequence(escPart)
		if csiMatch != "" {
			// CSI sequences should end with a terminating character
			matched, _ := regexp.MatchString(`[A-Za-z~]$`, escPart)
			if matched {
				bestMatch = csiMatch
				bestLength = length
				h.debugLog(fmt.Sprintf("Found complete CSI sequence: ESC%s → %s", escPart, bestMatch))
				break
			}
		}
	}

	// If we found a match, use it
	if bestMatch != "" && bestLength > 1 {
		processedBytes := h.inputBuffer[:bestLength]
		h.inputBuffer = h.inputBuffer[bestLength:]
		h.bufferMu.Unlock()

		hexCodes := make([]string, len(processedBytes))
		for i, b := range processedBytes {
			hexCodes[i] = fmt.Sprintf("%02x", b)
		}
		h.debugLog(fmt.Sprintf("Extracted escape sequence: %s (%d bytes, hex: %s)", bestMatch, bestLength, strings.Join(hexCodes, " ")))

		return bestMatch
	}

	// Check for double escape sequences (ESC ESC ...)
	if len(h.inputBuffer) >= 3 && h.inputBuffer[1] == ESC {
		escPart := rawSequence[1:]
		doubleEscPart := escPart[1:]
		doubleEscMatch := h.parseDoubleEscapeSequence(doubleEscPart)
		if doubleEscMatch != "" {
			h.inputBuffer = h.inputBuffer[3:]
			h.bufferMu.Unlock()
			h.debugLog(fmt.Sprintf("Found double escape sequence: ESC ESC %s → %s", doubleEscPart, doubleEscMatch))
			return doubleEscMatch
		}
	}

	// Check if we might have an incomplete sequence that we should wait for
	escPart := rawSequence[1:]

	// If it starts with [ and looks like an incomplete CSI sequence, wait
	incompleteCSI1, _ := regexp.MatchString(`^\[[0-9;]*$`, escPart)
	incompleteCSI2, _ := regexp.MatchString(`^\[[0-9;]+[A-Za-z]?$`, escPart)
	if incompleteCSI1 || incompleteCSI2 {
		h.bufferMu.Unlock()
		h.debugLog(fmt.Sprintf("Waiting for more data for potential CSI sequence: ESC%s", escPart))
		return "" // Wait for more data
	}

	// If it starts with O and we only have one character after ESC, wait
	if len(escPart) == 1 && escPart == "O" {
		h.bufferMu.Unlock()
		h.debugLog(fmt.Sprintf("Waiting for more data for potential O sequence: ESC%s", escPart))
		return "" // Wait for more data
	}

	// If we have a reasonable length sequence and no match, check simple patterns
	if len(h.inputBuffer) >= 2 {
		secondChar := string(h.inputBuffer[1])

		// Simple Alt+key sequences (ESC + single printable character)
		matched, _ := regexp.MatchString(`[a-zA-Z0-9!@#$%^&*()\-_=+\[\]{}\\|;:'",.<>?/` + "`~]", secondChar)
		if len(h.inputBuffer) == 2 && matched {
			h.inputBuffer = h.inputBuffer[2:]
			h.bufferMu.Unlock()
			altKey := "M-" + secondChar
			h.debugLog(fmt.Sprintf("Extracted Alt+key sequence: %s", altKey))
			return altKey
		}
	}

	// If we have more than reasonable escape sequence length and no match,
	// treat the ESC as standalone
	if len(h.inputBuffer) > 20 {
		h.inputBuffer = h.inputBuffer[1:]
		h.bufferMu.Unlock()
		h.debugLog("Extracted standalone ESC (sequence too long, no match)")
		return "esc"
	}

	// Not enough data yet or ambiguous, wait for more
	h.bufferMu.Unlock()
	h.debugLog(fmt.Sprintf("Waiting for more data, current buffer length: %d, escPart: \"%s\"", len(h.inputBuffer), escPart))
	return ""
}

// parseCSISequence parses CSI sequences with modifiers
func (h *DirectKeyboardHandler) parseCSISequence(escPart string) string {
	if !strings.HasPrefix(escPart, "[") || !strings.Contains(escPart, ";") {
		return ""
	}

	// Alt+Function key format: [1;3P, [1;3Q, etc.
	altFnRegex := regexp.MustCompile(`^\[1;3([PQRS])$`)
	if match := altFnRegex.FindStringSubmatch(escPart); match != nil {
		fnKeyMap := map[string]string{
			"P": "F1", "Q": "F2", "R": "F3", "S": "F4",
		}
		if fnKey, ok := fnKeyMap[match[1]]; ok {
			return "M-" + fnKey
		}
	}

	// Alt+F5-F12: [15;3~, [17;3~, etc.
	altHighFnRegex := regexp.MustCompile(`^\[(\d+);3~$`)
	if match := altHighFnRegex.FindStringSubmatch(escPart); match != nil {
		fnMap := map[string]string{
			"15": "F5", "17": "F6", "18": "F7", "19": "F8",
			"20": "F9", "21": "F10", "23": "F11", "24": "F12",
		}
		if fnKey, ok := fnMap[match[1]]; ok {
			return "M-" + fnKey
		}
	}

	// General CSI format
	generalRegex := regexp.MustCompile(`\[(\d+);(\d+)([~A-Z])`)
	match := generalRegex.FindStringSubmatch(escPart)
	if match == nil {
		return ""
	}

	numStr, modStr, key := match[1], match[2], match[3]
	num, _ := strconv.Atoi(numStr)
	mod, _ := strconv.Atoi(modStr)

	var baseKey string

	switch key {
	case "A":
		baseKey = "up"
	case "B":
		baseKey = "down"
	case "C":
		baseKey = "right"
	case "D":
		baseKey = "left"
	case "H":
		baseKey = "home"
	case "F":
		baseKey = "end"
	case "P":
		baseKey = "F1"
	case "Q":
		baseKey = "F2"
	case "R":
		baseKey = "F3"
	case "S":
		baseKey = "F4"
	case "~":
		switch num {
		case 1, 7:
			baseKey = "home"
		case 2:
			baseKey = "ins"
		case 3:
			baseKey = "fdel"
		case 4, 8:
			baseKey = "end"
		case 5:
			baseKey = "pgup"
		case 6:
			baseKey = "pgdn"
		case 11, 12, 13, 14:
			baseKey = fmt.Sprintf("F%d", num-10)
		case 15:
			baseKey = "F5"
		case 17:
			baseKey = "F6"
		case 18:
			baseKey = "F7"
		case 19:
			baseKey = "F8"
		case 20:
			baseKey = "F9"
		case 21:
			baseKey = "F10"
		case 23:
			baseKey = "F11"
		case 24:
			baseKey = "F12"
		default:
			baseKey = fmt.Sprintf("Key%d", num)
		}
	default:
		baseKey = "Key" + key
	}

	modifierMap := map[int][]string{
		2: {"Shift"},
		3: {"Alt"},
		4: {"Shift", "Alt"},
		5: {"Control"},
		6: {"Shift", "Control"},
		7: {"Alt", "Control"},
		8: {"Shift", "Alt", "Control"},
	}

	modifiers := modifierMap[mod]
	resultKey := baseKey

	// Check for Control
	hasControl := false
	hasAlt := false
	hasShift := false
	for _, m := range modifiers {
		switch m {
		case "Control":
			hasControl = true
		case "Alt":
			hasAlt = true
		case "Shift":
			hasShift = true
		}
	}

	if hasControl {
		resultKey = "^" + baseKey
	}

	if hasAlt {
		resultKey = "M-" + resultKey
	}

	if hasShift {
		// Don't add S- prefix for uppercase single letters
		isUpperLetter := len(baseKey) == 1 && baseKey >= "A" && baseKey <= "Z"
		if !isUpperLetter {
			resultKey = "S-" + resultKey
		}
	}

	return resultKey
}

// parseDoubleEscapeSequence parses double-escape sequences
func (h *DirectKeyboardHandler) parseDoubleEscapeSequence(remainingPart string) string {
	for escSeq, action := range h.escBindings {
		normalizedRemaining := strings.ReplaceAll(remainingPart, " ", "")
		if remainingPart == escSeq || normalizedRemaining == escSeq {
			if strings.HasPrefix(action, "F") || strings.HasPrefix(action, "fdel") ||
				strings.HasPrefix(action, "del") || strings.HasPrefix(action, "back") ||
				strings.HasPrefix(action, "up") || strings.HasPrefix(action, "down") ||
				strings.HasPrefix(action, "left") || strings.HasPrefix(action, "right") ||
				strings.HasPrefix(action, "home") || strings.HasPrefix(action, "end") ||
				strings.HasPrefix(action, "esc") ||
				strings.HasPrefix(action, "pg") || strings.HasPrefix(action, "ins") {
				return "M-" + action
			}
		}
	}
	return ""
}

// processControlKey processes a control key character
func (h *DirectKeyboardHandler) processControlKey(code int) string {
	switch code {
	case 0:
		return "^space"
	case 8:
		return "back"
	case 9:
		return "tab"
	case 13:
		return "return"
	case 27:
		return "esc"
	case 28:
		return "^\\"
	case 29:
		return "^]"
	case 30:
		return "^^"
	case 31:
		return "^_"
	case 32:
		return "space"
	case 127:
		return "del"
	default:
		if code >= 1 && code <= 26 {
			letter := string(rune(code + 64))
			return "^" + letter
		} else if code <= 126 {
			return string(rune(code))
		}
		return fmt.Sprintf("[%d]", code)
	}
}

// GetKey gets the next key from the queue
// This is the main entry point - call this instead of reading directly
func (h *DirectKeyboardHandler) GetKey() string {
	// Start listening if not already
	h.listenMu.Lock()
	isListening := h.isListening
	h.listenMu.Unlock()

	if !isListening {
		h.StartListening()
	}

	// If we have keys in the queue, return the first one
	h.queueMu.Lock()
	if len(h.keyQueue) > 0 {
		key := h.keyQueue[0]
		h.keyQueue = h.keyQueue[1:]
		h.queueMu.Unlock()
		h.debugLog(fmt.Sprintf("Returning queued key: %s", key))
		return key
	}
	h.queueMu.Unlock()

	// Wait for a key to be available
	for {
		select {
		case <-h.keyChan:
			h.queueMu.Lock()
			if len(h.keyQueue) > 0 {
				key := h.keyQueue[0]
				h.keyQueue = h.keyQueue[1:]
				h.queueMu.Unlock()
				h.debugLog(fmt.Sprintf("Returning waited key: %s", key))
				return key
			}
			h.queueMu.Unlock()
		case <-time.After(10 * time.Millisecond):
			// Periodically check the queue in case we missed a signal
			h.queueMu.Lock()
			if len(h.keyQueue) > 0 {
				key := h.keyQueue[0]
				h.keyQueue = h.keyQueue[1:]
				h.queueMu.Unlock()
				h.debugLog(fmt.Sprintf("Returning polled key: %s", key))
				return key
			}
			h.queueMu.Unlock()
		}
	}
}

// HasKeys checks if there are keys available without waiting
func (h *DirectKeyboardHandler) HasKeys() bool {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	return len(h.keyQueue) > 0
}

// GetAvailableKeys gets multiple keys at once (useful for processing paste)
func (h *DirectKeyboardHandler) GetAvailableKeys(maxKeys int) []string {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()

	if maxKeys <= 0 {
		maxKeys = len(h.keyQueue)
	}

	count := len(h.keyQueue)
	if count > maxKeys {
		count = maxKeys
	}

	keys := make([]string, count)
	copy(keys, h.keyQueue[:count])
	h.keyQueue = h.keyQueue[count:]

	return keys
}

// ClearBuffers clears the input buffer and key queue (emergency cleanup)
func (h *DirectKeyboardHandler) ClearBuffers() {
	h.bufferMu.Lock()
	h.inputBuffer = make([]byte, 0)
	h.bufferMu.Unlock()

	h.queueMu.Lock()
	h.keyQueue = make([]string, 0)
	h.queueMu.Unlock()

	h.debugLog("Cleared input buffers")
}
