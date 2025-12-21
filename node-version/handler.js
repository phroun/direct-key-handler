/**
 * keyboard/handler.js
 * 
 * A buffered keyboard input handler for terminal applications with 
 * VT100/ANSI escape sequence support.
 */

const EventEmitter = require('events');

// Escape sequence bindings - maps escape sequences to key names
const escBindings = {
    // Arrow keys
    '\x1b[A': 'Up',
    '\x1b[B': 'Down',
    '\x1b[C': 'Right',
    '\x1b[D': 'Left',

    // Arrow keys with modifiers
    '\x1b[1;2A': 'S-Up',
    '\x1b[1;2B': 'S-Down',
    '\x1b[1;2C': 'S-Right',
    '\x1b[1;2D': 'S-Left',
    '\x1b[1;3A': 'M-Up',
    '\x1b[1;3B': 'M-Down',
    '\x1b[1;3C': 'M-Right',
    '\x1b[1;3D': 'M-Left',
    '\x1b[1;5A': 'C-Up',
    '\x1b[1;5B': 'C-Down',
    '\x1b[1;5C': 'C-Right',
    '\x1b[1;5D': 'C-Left',

    // Function keys
    '\x1bOP': 'F1',
    '\x1bOQ': 'F2',
    '\x1bOR': 'F3',
    '\x1bOS': 'F4',
    '\x1b[15~': 'F5',
    '\x1b[17~': 'F6',
    '\x1b[18~': 'F7',
    '\x1b[19~': 'F8',
    '\x1b[20~': 'F9',
    '\x1b[21~': 'F10',
    '\x1b[23~': 'F11',
    '\x1b[24~': 'F12',

    // Navigation keys
    '\x1b[H': 'Home',
    '\x1b[F': 'End',
    '\x1b[1~': 'Home',
    '\x1b[4~': 'End',
    '\x1b[2~': 'Insert',
    '\x1b[3~': 'Delete',
    '\x1b[5~': 'PageUp',
    '\x1b[6~': 'PageDown',

    // Alternate arrow key sequences (some terminals)
    '\x1bOA': 'Up',
    '\x1bOB': 'Down',
    '\x1bOC': 'Right',
    '\x1bOD': 'Left',
};

// Control key names
const controlKeys = {
    0: '^@',        // Ctrl-Space or Ctrl-@
    1: '^A',
    2: '^B',
    3: '^C',
    4: '^D',
    5: '^E',
    6: '^F',
    7: '^G',
    8: 'Backspace', // Ctrl-H
    9: 'Tab',       // Ctrl-I
    10: '^J',       // Ctrl-J (LF) - distinct from Enter
    11: '^K',
    12: '^L',
    13: 'Enter',    // Ctrl-M (CR)
    14: '^N',
    15: '^O',
    16: '^P',
    17: '^Q',
    18: '^R',
    19: '^S',
    20: '^T',
    21: '^U',
    22: '^V',
    23: '^W',
    24: '^X',
    25: '^Y',
    26: '^Z',
    27: 'Escape',   // Escape itself
    28: '^\\',
    29: '^]',
    30: '^^',
    31: '^_',
    127: 'Backspace', // DEL
};

// Symbol shift mappings
const symbolShiftMap = {
    '`': '~',
    ',': '<',
    '.': '>',
    '/': '?',
    ';': ':',
    "'": '"',
    '[': '{',
    ']': '}',
    '\\': '|',
    '-': '_',
    '=': '+',
};

// Number shift mappings
const numberShiftMap = {
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
};

// Tilde key mappings
const tildeKeys = {
    1: 'Home',
    2: 'Insert',
    3: 'Delete',
    4: 'End',
    5: 'PageUp',
    6: 'PageDown',
    15: 'F5',
    17: 'F6',
    18: 'F7',
    19: 'F8',
    20: 'F9',
    21: 'F10',
    23: 'F11',
    24: 'F12',
};

// Kitty protocol special keys
const kittySpecialKeys = {
    9: 'Tab',
    13: 'Enter',
    27: 'Escape',
    32: 'Space',
    127: 'Backspace',
};

// Bracketed paste sequences
const BRACKETED_PASTE_START = '\x1b[200~';
const BRACKETED_PASTE_END = '\x1b[201~';

/**
 * DirectKeyboardHandler - handles raw keyboard input with escape sequence parsing
 */
class DirectKeyboardHandler extends EventEmitter {
    /**
     * Create a new keyboard handler
     * @param {Object} options - Configuration options
     * @param {stream.Readable} options.inputStream - Input stream (default: process.stdin)
     * @param {stream.Writable} options.outputStream - Output stream for echo (default: null)
     * @param {Function} options.debugFn - Debug callback (optional)
     */
    constructor(options = {}) {
        super();
        
        this.inputStream = options.inputStream || process.stdin;
        this.outputStream = options.outputStream || null;
        this.debugFn = options.debugFn || null;
        
        // State
        this.running = false;
        this.inLineMode = false;
        this.wasRawMode = false;
        
        // Escape sequence state
        this.escBuffer = '';
        this.inEscape = false;
        this.escTimeout = null;
        
        // UTF-8 state
        this.utf8Buffer = Buffer.alloc(0);
        this.utf8Remaining = 0;
        
        // Bracketed paste state
        this.inPaste = false;
        this.pasteBuffer = Buffer.alloc(0);
        
        // Line assembly state
        this.currentLine = Buffer.alloc(0);
        this.charByteLengths = [];
        
        // Key buffer for getKey()
        this.keyBuffer = [];
        this.keyResolvers = [];
        
        // Line buffer for getLine()
        this.lineBuffer = [];
        this.lineResolvers = [];
        
        // Callbacks
        this.onKeyCallback = null;
        this.onLineCallback = null;
        this.onPasteCallback = null;
        
        // Bind the data handler
        this._onData = this._onData.bind(this);
    }
    
    /**
     * Start listening for keyboard input
     */
    async start() {
        if (this.running) {
            throw new Error('Handler already running');
        }
        
        // Set raw mode if input is a TTY
        if (this.inputStream.isTTY && this.inputStream.setRawMode) {
            this.wasRawMode = this.inputStream.isRaw;
            this.inputStream.setRawMode(true);
            this._debug('Terminal set to raw mode');
        }
        
        this.running = true;
        this.inputStream.on('data', this._onData);
        this._debug('Handler started');
    }
    
    /**
     * Stop listening and restore terminal state
     */
    stop() {
        if (!this.running) {
            return;
        }
        
        this.running = false;
        this.inputStream.removeListener('data', this._onData);
        
        // Clear escape timeout
        if (this.escTimeout) {
            clearTimeout(this.escTimeout);
            this.escTimeout = null;
        }
        
        // Restore raw mode if we changed it
        if (this.inputStream.isTTY && this.inputStream.setRawMode) {
            this.inputStream.setRawMode(this.wasRawMode);
            this._debug('Terminal restored to original mode');
        }
        
        // Reject any pending promises
        for (const resolver of this.keyResolvers) {
            resolver.reject(new Error('Handler stopped'));
        }
        this.keyResolvers = [];
        
        for (const resolver of this.lineResolvers) {
            resolver.reject(new Error('Handler stopped'));
        }
        this.lineResolvers = [];
        
        this._debug('Handler stopped');
    }
    
    /**
     * Check if handler is running
     */
    isRunning() {
        return this.running;
    }
    
    /**
     * Check if handler manages terminal raw mode
     */
    managesTerminal() {
        return this.inputStream.isTTY && this.inputStream.setRawMode !== undefined;
    }
    
    /**
     * Set line mode on or off
     * @param {boolean} enabled - Enable line assembly mode
     */
    setLineMode(enabled) {
        this.inLineMode = enabled;
        if (enabled) {
            this.currentLine = Buffer.alloc(0);
            this.charByteLengths = [];
        }
    }
    
    /**
     * Check if line mode is active
     */
    isLineMode() {
        return this.inLineMode;
    }
    
    /**
     * Set the echo output stream
     * @param {stream.Writable} stream - Output stream for echo
     */
    setEchoWriter(stream) {
        this.outputStream = stream;
    }
    
    /**
     * Set callback for key events
     * @param {Function} callback - Called with each key
     */
    onKey(callback) {
        this.onKeyCallback = callback;
    }
    
    /**
     * Set callback for line events
     * @param {Function} callback - Called with each completed line
     */
    onLine(callback) {
        this.onLineCallback = callback;
    }
    
    /**
     * Set callback for paste events
     * @param {Function} callback - Called with pasted content
     */
    onPaste(callback) {
        this.onPasteCallback = callback;
    }
    
    /**
     * Get the next key (async)
     * @returns {Promise<string>} The next key
     */
    getKey() {
        return new Promise((resolve, reject) => {
            if (this.keyBuffer.length > 0) {
                resolve(this.keyBuffer.shift());
            } else {
                this.keyResolvers.push({ resolve, reject });
            }
        });
    }
    
    /**
     * Get the next line (async) - enables line mode temporarily
     * @returns {Promise<string>} The next line
     */
    getLine() {
        return new Promise((resolve, reject) => {
            this.setLineMode(true);
            
            if (this.lineBuffer.length > 0) {
                this.setLineMode(false);
                resolve(this.lineBuffer.shift());
            } else {
                this.lineResolvers.push({ 
                    resolve: (line) => {
                        this.setLineMode(false);
                        resolve(line);
                    }, 
                    reject 
                });
            }
        });
    }
    
    /**
     * Handle incoming data
     * @private
     */
    _onData(data) {
        for (let i = 0; i < data.length; i++) {
            this._processByte(data[i]);
        }
    }
    
    /**
     * Process a single byte
     * @private
     */
    _processByte(b) {
        // Handle bracketed paste mode
        if (this.inPaste) {
            this.pasteBuffer = Buffer.concat([this.pasteBuffer, Buffer.from([b])]);
            
            // Check if paste buffer ends with the end sequence
            if (this.pasteBuffer.length >= BRACKETED_PASTE_END.length) {
                const tail = this.pasteBuffer.slice(-BRACKETED_PASTE_END.length).toString();
                if (tail === BRACKETED_PASTE_END) {
                    // End of paste
                    const content = this.pasteBuffer.slice(0, -BRACKETED_PASTE_END.length);
                    this.inPaste = false;
                    this.pasteBuffer = Buffer.alloc(0);
                    this._debug(`Paste end, ${content.length} bytes`);
                    this._emitPaste(content);
                }
            }
            return;
        }
        
        if (this.inEscape) {
            this.escBuffer += String.fromCharCode(b);
            
            // Check for bracketed paste start
            if (this.escBuffer === BRACKETED_PASTE_START) {
                this._debug('Bracketed paste start detected');
                this.inEscape = false;
                this.escBuffer = '';
                this.inPaste = true;
                this.pasteBuffer = Buffer.alloc(0);
                this._clearEscTimeout();
                return;
            }
            
            // Check if we have a complete escape sequence
            if (escBindings[this.escBuffer]) {
                this._emitKey(escBindings[this.escBuffer]);
                this.escBuffer = '';
                this.inEscape = false;
                this._clearEscTimeout();
                return;
            }
            
            // Check if this could be a prefix of a valid sequence
            if (this._couldBeEscapePrefix(this.escBuffer)) {
                this._resetEscTimeout();
                return;
            }
            
            // Try dynamic parsing for CSI sequences with modifiers
            const csiResult = this._parseModifiedCSI(this.escBuffer);
            if (csiResult) {
                this._emitKey(csiResult);
                this.escBuffer = '';
                this.inEscape = false;
                this._clearEscTimeout();
                return;
            }
            
            // Try Alt+key parsing
            const altResult = this._parseAltSequence(this.escBuffer);
            if (altResult) {
                this._emitKey(altResult);
                this.escBuffer = '';
                this.inEscape = false;
                this._clearEscTimeout();
                return;
            }
            
            // Not a valid sequence - emit as individual keys
            this._emitEscapeBuffer();
            return;
        }
        
        // Check for escape start
        if (b === 0x1b) {
            this.inEscape = true;
            this.escBuffer = String.fromCharCode(b);
            this._resetEscTimeout();
            return;
        }
        
        // Handle control characters
        if (b < 32 || b === 127) {
            const key = controlKeys[b];
            if (key) {
                this._emitKey(key);
            } else {
                this._emitKey('^' + String.fromCharCode(b + 64));
            }
            return;
        }
        
        // Regular printable character or start of UTF-8 sequence
        if (b < 128) {
            this._emitKey(String.fromCharCode(b));
            return;
        }
        
        // UTF-8 multi-byte character handling
        if (this.utf8Remaining > 0) {
            // Continuation byte should be 10xxxxxx (0x80-0xBF)
            if (b >= 0x80 && b <= 0xBF) {
                this.utf8Buffer = Buffer.concat([this.utf8Buffer, Buffer.from([b])]);
                this.utf8Remaining--;
                if (this.utf8Remaining === 0) {
                    // Complete UTF-8 sequence
                    this._emitKey(this.utf8Buffer.toString('utf8'));
                    this.utf8Buffer = Buffer.alloc(0);
                }
            } else {
                // Invalid continuation - emit buffer as-is and reset
                for (const byte of this.utf8Buffer) {
                    this._emitKey(String.fromCharCode(byte));
                }
                this.utf8Buffer = Buffer.alloc(0);
                this.utf8Remaining = 0;
                this._processByte(b);
            }
            return;
        }
        
        // Start of new UTF-8 sequence
        if (b >= 0xC0 && b <= 0xDF) {
            // 2-byte sequence
            this.utf8Buffer = Buffer.from([b]);
            this.utf8Remaining = 1;
        } else if (b >= 0xE0 && b <= 0xEF) {
            // 3-byte sequence
            this.utf8Buffer = Buffer.from([b]);
            this.utf8Remaining = 2;
        } else if (b >= 0xF0 && b <= 0xF7) {
            // 4-byte sequence
            this.utf8Buffer = Buffer.from([b]);
            this.utf8Remaining = 3;
        } else {
            // Invalid UTF-8 lead byte
            this._emitKey(String.fromCharCode(b));
        }
    }
    
    /**
     * Check if seq could be a prefix of a valid escape sequence
     * @private
     */
    _couldBeEscapePrefix(seq) {
        for (const key of Object.keys(escBindings)) {
            if (key.length > seq.length && key.startsWith(seq)) {
                return true;
            }
        }
        // Also allow CSI sequences in progress: ESC [ ...
        if (seq.length >= 2 && seq[0] === '\x1b' && seq[1] === '[') {
            const last = seq.charCodeAt(seq.length - 1);
            if (last >= 0x40 && last <= 0x7e) {
                return false; // Terminated
            }
            return true; // Still in progress
        }
        return false;
    }
    
    /**
     * Emit escape buffer as individual keys
     * @private
     */
    _emitEscapeBuffer() {
        // First byte is ESC
        this._emitKey('Escape');
        // Remaining bytes as regular characters
        for (let i = 1; i < this.escBuffer.length; i++) {
            const b = this.escBuffer.charCodeAt(i);
            if (b < 32 || b === 127) {
                const key = controlKeys[b];
                if (key) {
                    this._emitKey(key);
                }
            } else {
                this._emitKey(this.escBuffer[i]);
            }
        }
        this.escBuffer = '';
        this.inEscape = false;
    }
    
    /**
     * Reset escape timeout
     * @private
     */
    _resetEscTimeout() {
        this._clearEscTimeout();
        this.escTimeout = setTimeout(() => {
            if (this.inEscape && this.escBuffer.length > 0) {
                // Try Alt sequence parsing before giving up
                const altResult = this._parseAltSequence(this.escBuffer);
                if (altResult) {
                    this._emitKey(altResult);
                    this.escBuffer = '';
                    this.inEscape = false;
                } else {
                    this._emitEscapeBuffer();
                }
            }
        }, 50);
    }
    
    /**
     * Clear escape timeout
     * @private
     */
    _clearEscTimeout() {
        if (this.escTimeout) {
            clearTimeout(this.escTimeout);
            this.escTimeout = null;
        }
    }
    
    /**
     * Emit a key event
     * @private
     */
    _emitKey(key) {
        this._debug(`Key: "${key}"`);
        
        // Call callback if set
        if (this.onKeyCallback) {
            this.onKeyCallback(key);
        }
        
        // Emit event
        this.emit('key', key);
        
        if (this.inLineMode) {
            // In line mode: keys go to line assembly
            this._handleLineAssembly(key);
        } else {
            // Normal mode: resolve pending getKey() or buffer
            if (this.keyResolvers.length > 0) {
                const resolver = this.keyResolvers.shift();
                resolver.resolve(key);
            } else {
                this.keyBuffer.push(key);
            }
        }
    }
    
    /**
     * Emit paste content
     * @private
     */
    _emitPaste(content) {
        // Call callback if set
        if (this.onPasteCallback) {
            this.onPasteCallback(content);
        }
        
        // Emit event
        this.emit('paste', content);
        
        if (this.inLineMode) {
            // In line mode: add pasted content to line buffer
            this._handlePasteLineAssembly(content);
        } else {
            // Normal mode: emit each character as individual key events
            const str = content.toString('utf8');
            for (const char of str) {
                const code = char.charCodeAt(0);
                if (char === '\r') {
                    this._emitKey('Enter');
                } else if (char === '\n') {
                    this._emitKey('^J');
                } else if (char === '\t') {
                    this._emitKey('Tab');
                } else if (code === 0x7f) {
                    this._emitKey('Backspace');
                } else if (code < 32) {
                    const key = controlKeys[code];
                    if (key) {
                        this._emitKey(key);
                    }
                } else {
                    this._emitKey(char);
                }
            }
        }
    }
    
    /**
     * Handle paste content in line assembly mode
     * @private
     */
    _handlePasteLineAssembly(content) {
        const str = content.toString('utf8');
        
        for (const char of str) {
            const code = char.charCodeAt(0);
            
            if (char === '\r' || char === '\n') {
                // Newline - submit the current line
                const line = this.currentLine.toString('utf8');
                this.currentLine = Buffer.alloc(0);
                this.charByteLengths = [];
                
                this._echo('\r\n');
                this._deliverLine(line);
                return; // Single-line read
            } else if (code >= 32 || char === '\t') {
                // Printable character or tab
                const charBuf = Buffer.from(char, 'utf8');
                this.currentLine = Buffer.concat([this.currentLine, charBuf]);
                this.charByteLengths.push(charBuf.length);
                this._echo(char);
            }
        }
    }
    
    /**
     * Handle line assembly
     * @private
     */
    _handleLineAssembly(key) {
        switch (key) {
            case 'Enter':
                const line = this.currentLine.toString('utf8');
                this.currentLine = Buffer.alloc(0);
                this.charByteLengths = [];
                this._echo('\r\n');
                this._deliverLine(line);
                break;
                
            case 'Backspace':
                if (this.charByteLengths.length > 0) {
                    const lastCharLen = this.charByteLengths.pop();
                    this.currentLine = this.currentLine.slice(0, -lastCharLen);
                    this._echo('\b \b');
                }
                break;
                
            case '^U':
                // Clear line
                for (let i = 0; i < this.charByteLengths.length; i++) {
                    this._echo('\b \b');
                }
                this.currentLine = Buffer.alloc(0);
                this.charByteLengths = [];
                break;
                
            case '^C':
                // Interrupt
                this._echo('^C\r\n');
                this.currentLine = Buffer.alloc(0);
                this.charByteLengths = [];
                this._deliverLine('');
                break;
                
            default:
                // Check if printable character
                if (key.length > 0) {
                    const code = key.codePointAt(0);
                    if (code >= 32) {
                        const charBuf = Buffer.from(key, 'utf8');
                        this.currentLine = Buffer.concat([this.currentLine, charBuf]);
                        this.charByteLengths.push(charBuf.length);
                        this._echo(key);
                    }
                }
                break;
        }
    }
    
    /**
     * Deliver a completed line
     * @private
     */
    _deliverLine(line) {
        // Call callback if set
        if (this.onLineCallback) {
            this.onLineCallback(line);
        }
        
        // Emit event
        this.emit('line', line);
        
        // Resolve pending getLine() or buffer
        if (this.lineResolvers.length > 0) {
            const resolver = this.lineResolvers.shift();
            resolver.resolve(line);
        } else {
            this.lineBuffer.push(line);
        }
    }
    
    /**
     * Echo to output stream
     * @private
     */
    _echo(str) {
        if (this.outputStream) {
            this.outputStream.write(str);
        }
    }
    
    /**
     * Debug output
     * @private
     */
    _debug(msg) {
        if (this.debugFn) {
            this.debugFn(msg);
        }
    }
    
    /**
     * Parse Alt sequence (ESC followed by character)
     * @private
     */
    _parseAltSequence(seq) {
        if (seq.length !== 2 || seq[0] !== '\x1b') {
            return null;
        }
        
        const char = seq[1];
        const code = char.charCodeAt(0);
        
        // Lowercase letters
        if (code >= 97 && code <= 122) { // a-z
            return `M-${char}`;
        }
        
        // Uppercase letters (shift implied)
        if (code >= 65 && code <= 90) { // A-Z
            return `M-S-${char.toLowerCase()}`;
        }
        
        // Numbers
        if (code >= 48 && code <= 57) { // 0-9
            return `M-${char}`;
        }
        
        // Symbols and punctuation
        const symbols = {
            '[': 'M-[', ']': 'M-]', '{': 'M-{', '}': 'M-}',
            '(': 'M-(', ')': 'M-)', '<': 'M-<', '>': 'M->',
            '/': 'M-/', '\\': 'M-\\', "'": "M-'", '"': 'M-"',
            '`': 'M-`', ',': 'M-,', '.': 'M-.', ';': 'M-;',
            ':': 'M-:', '=': 'M-=', '+': 'M-+', '-': 'M--',
            '_': 'M-_', '!': 'M-!', '@': 'M-@', '#': 'M-#',
            '$': 'M-$', '%': 'M-%', '^': 'M-^', '&': 'M-&',
            '*': 'M-*', '?': 'M-?', '|': 'M-|', '~': 'M-~',
            ' ': 'M-Space',
        };
        
        if (symbols[char]) {
            return symbols[char];
        }
        
        // Special control characters
        switch (code) {
            case 0x09: return 'M-Tab';
            case 0x0D: return 'M-Enter';
            case 0x7F: return 'M-Backspace';
            case 0x08: return 'M-Backspace';
            case 0x1B: return 'M-Escape';
        }
        
        // Control characters: M-^A through M-^Z
        if (code >= 0x01 && code <= 0x1A) {
            const letter = String.fromCharCode(64 + code);
            return `M-^${letter}`;
        }
        
        // Any other printable ASCII
        if (code >= 0x20 && code < 0x7f) {
            return `M-${char}`;
        }
        
        return null;
    }
    
    /**
     * Parse modified CSI sequences
     * @private
     */
    _parseModifiedCSI(seq) {
        // Must start with ESC [
        if (seq.length < 3 || seq[0] !== '\x1b' || seq[1] !== '[') {
            return null;
        }
        
        const body = seq.slice(2);
        if (body.length === 0) {
            return null;
        }
        
        // Check for Shift+Tab: ESC [ Z
        if (body === 'Z') {
            return 'S-Tab';
        }
        
        // Final byte determines the key type
        const finalByte = body.charCodeAt(body.length - 1);
        if (finalByte < 0x40 || finalByte > 0x7E) {
            return null;
        }
        
        const params = body.slice(0, -1);
        const parts = params ? params.split(';') : [];
        const finalChar = body[body.length - 1];
        
        switch (finalChar) {
            case 'A': case 'B': case 'C': case 'D':
                return this._parseModifiedCursorKey(finalChar, parts);
            case 'H': case 'F':
                return this._parseModifiedHomeEnd(finalChar, parts);
            case 'P': case 'Q': case 'R': case 'S':
                return this._parseModifiedF1toF4(finalChar, parts);
            case '~':
                return this._parseModifiedTildeKey(parts);
            case 'u':
                return this._parseKittyProtocol(parts);
        }
        
        return null;
    }
    
    /**
     * Parse modified cursor keys
     * @private
     */
    _parseModifiedCursorKey(finalChar, parts) {
        const keyNames = { 'A': 'Up', 'B': 'Down', 'C': 'Right', 'D': 'Left' };
        const baseName = keyNames[finalChar];
        if (!baseName) return null;
        
        if (parts.length === 0) return baseName;
        if (parts.length !== 2) return null;
        
        const mod = this._parseModifierParam(parts[1]);
        const prefix = this._modifierPrefix(mod);
        return prefix + baseName;
    }
    
    /**
     * Parse modified Home/End
     * @private
     */
    _parseModifiedHomeEnd(finalChar, parts) {
        const keyNames = { 'H': 'Home', 'F': 'End' };
        const baseName = keyNames[finalChar];
        if (!baseName) return null;
        
        if (parts.length === 0) return baseName;
        if (parts.length !== 2) return null;
        
        const mod = this._parseModifierParam(parts[1]);
        const prefix = this._modifierPrefix(mod);
        return prefix + baseName;
    }
    
    /**
     * Parse modified F1-F4
     * @private
     */
    _parseModifiedF1toF4(finalChar, parts) {
        const keyNames = { 'P': 'F1', 'Q': 'F2', 'R': 'F3', 'S': 'F4' };
        const baseName = keyNames[finalChar];
        if (!baseName) return null;
        
        if (parts.length === 0) return baseName;
        if (parts.length !== 2) return null;
        
        const mod = this._parseModifierParam(parts[1]);
        const prefix = this._modifierPrefix(mod);
        return prefix + baseName;
    }
    
    /**
     * Parse modified tilde keys
     * @private
     */
    _parseModifiedTildeKey(parts) {
        if (parts.length === 0) return null;
        
        const keyNum = this._parseModifierParam(parts[0]);
        const baseName = tildeKeys[keyNum];
        if (!baseName) return null;
        
        if (parts.length === 1) return baseName;
        if (parts.length === 2) {
            const mod = this._parseModifierParam(parts[1]);
            const prefix = this._modifierPrefix(mod);
            return prefix + baseName;
        }
        
        return null;
    }
    
    /**
     * Parse Kitty keyboard protocol
     * @private
     */
    _parseKittyProtocol(parts) {
        if (parts.length === 0) return null;
        
        const keycode = this._parseModifierParam(parts[0]);
        const mod = parts.length >= 2 ? this._parseModifierParam(parts[1]) : 1;
        
        // Letter keys
        if (keycode >= 97 && keycode <= 122) { // a-z
            return this._formatLetterKey(keycode, mod);
        } else if (keycode >= 65 && keycode <= 90) { // A-Z
            return this._formatLetterKey(keycode + 32, mod);
        }
        
        // Symbol keys
        if (this._isSymbolKey(keycode)) {
            return this._formatSymbolKey(keycode, mod);
        }
        
        // Number keys
        if (this._isNumberKey(keycode)) {
            return this._formatNumberKey(keycode, mod);
        }
        
        // Special keys
        const baseName = kittySpecialKeys[keycode];
        if (!baseName) return null;
        
        if (mod <= 1) return baseName;
        
        const prefix = this._modifierPrefix(mod);
        return prefix + baseName;
    }
    
    /**
     * Parse modifier parameter
     * @private
     */
    _parseModifierParam(s) {
        if (!s) return 1;
        const num = parseInt(s, 10);
        return isNaN(num) || num < 1 ? 1 : num;
    }
    
    /**
     * Convert modifier code to prefix
     * @private
     */
    _modifierPrefix(mod) {
        if (mod < 2) return '';
        mod--;
        
        let prefix = '';
        if (mod & 1) prefix += 'S-';
        if (mod & 2) prefix += 'M-';
        if (mod & 4) prefix += 'C-';
        if (mod & 8) prefix += 's-';
        return prefix;
    }
    
    /**
     * Check if keycode is a symbol key
     * @private
     */
    _isSymbolKey(keycode) {
        const symbols = ['`', ',', '.', '/', ';', "'", '[', ']', '\\', '-', '='];
        return symbols.includes(String.fromCharCode(keycode));
    }
    
    /**
     * Check if keycode is a number key
     * @private
     */
    _isNumberKey(keycode) {
        return keycode >= 48 && keycode <= 57; // 0-9
    }
    
    /**
     * Format letter key with modifiers
     * @private
     */
    _formatLetterKey(keycode, mod) {
        if (mod < 1) mod = 1;
        mod--;
        
        const hasShift = (mod & 1) !== 0;
        const hasAlt = (mod & 2) !== 0;
        const hasCtrl = (mod & 4) !== 0;
        const hasSuper = (mod & 8) !== 0;
        
        const letter = String.fromCharCode(keycode);
        const upperLetter = letter.toUpperCase();
        
        let keyPart;
        if (hasCtrl) {
            if (hasShift) {
                keyPart = `S-^${upperLetter}`;
            } else {
                keyPart = `^${upperLetter}`;
            }
        } else if (hasShift) {
            keyPart = upperLetter;
        } else {
            keyPart = letter;
        }
        
        let prefix = '';
        if (hasSuper) prefix += 's-';
        if (hasAlt) prefix += 'M-';
        
        return prefix + keyPart;
    }
    
    /**
     * Format symbol key with modifiers
     * @private
     */
    _formatSymbolKey(keycode, mod) {
        if (mod < 1) mod = 1;
        mod--;
        
        const hasShift = (mod & 1) !== 0;
        const hasAlt = (mod & 2) !== 0;
        const hasCtrl = (mod & 4) !== 0;
        const hasSuper = (mod & 8) !== 0;
        
        let displayChar = String.fromCharCode(keycode);
        if (hasShift && symbolShiftMap[displayChar]) {
            displayChar = symbolShiftMap[displayChar];
        }
        
        let keyPart;
        if (hasCtrl) {
            keyPart = `^${displayChar}`;
        } else {
            keyPart = displayChar;
        }
        
        let prefix = '';
        if (hasSuper) prefix += 's-';
        if (hasAlt) prefix += 'M-';
        
        return prefix + keyPart;
    }
    
    /**
     * Format number key with modifiers
     * @private
     */
    _formatNumberKey(keycode, mod) {
        if (mod < 1) mod = 1;
        mod--;
        
        const hasShift = (mod & 1) !== 0;
        const hasAlt = (mod & 2) !== 0;
        const hasCtrl = (mod & 4) !== 0;
        const hasSuper = (mod & 8) !== 0;
        
        let displayChar = String.fromCharCode(keycode);
        if (hasShift && numberShiftMap[displayChar]) {
            displayChar = numberShiftMap[displayChar];
        }
        
        let keyPart;
        if (hasCtrl) {
            keyPart = `^${displayChar}`;
        } else {
            keyPart = displayChar;
        }
        
        let prefix = '';
        if (hasSuper) prefix += 's-';
        if (hasAlt) prefix += 'M-';
        
        return prefix + keyPart;
    }
}

module.exports = { DirectKeyboardHandler };
