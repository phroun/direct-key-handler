/**
 * Buffered Direct Keyboard Handler with VT100 Support and Unicode/Emoji Support
 * 
 * This implementation:
 * 1. Buffers all incoming input to prevent data loss during paste operations
 * 2. Processes buffered input asynchronously 
 * 3. Maintains proper escape sequence handling
 * 4. Returns individual keys/sequences via getKey() while preserving paste data
 * 5. Properly handles Unicode characters including emoji and high ASCII
 */

class DirectKeyboardHandler {
  constructor(debugCallback) {
    this.escBindings = new Map();
    this.debugLog = debugCallback || ((message) => {});
    
    // Input buffering
    this.inputBuffer = Buffer.alloc(0);
    this.keyQueue = [];
    this.isProcessing = false;
    this.isListening = false;
    
    // Initialize ANSI/VT100 terminal escape sequences
    this.initializeEscBindings();
  }
  
  /**
   * Initialize common escape key bindings for terminal sequences
   */
  initializeEscBindings() {
    // Common escape sequences for VT100/ANSI terminals
    const commonBindings = [
      // Arrow keys (common formats)
      ['[A', 'up'],
      ['[B', 'down'],
      ['[C', 'right'],
      ['[D', 'left'],
      ['[1;1A', '^up'],
      ['[1;1B', '^down'],
      ['[1;1C', '^right'],
      ['[1;1D', '^left'],
      ['[Z', 'S-tab'],

      ['O1P', 'H-F1'],     // Alternative format
      ['O1Q', 'H-F2'],
      ['O1R', 'H-F3'],
      ['O1S', 'H-F4'],
      ['O2P', 'S-F1'],     // Alternative format
      ['O2Q', 'S-F2'],
      ['O2R', 'S-F3'],
      ['O2S', 'S-F4'],
      ['O3P', 'M-F1'],     // Alternative format
      ['O3Q', 'M-F2'],
      ['O3R', 'M-F3'],
      ['O3S', 'M-F4'],
      ['O4P', 'S-M-F1'],     // Alternative format
      ['O4Q', 'S-M-F2'],
      ['O4R', 'S-M-F3'],
      ['O4S', 'S-M-F4'],
      ['O5P', '^F1'],     // Alternative format
      ['O5Q', '^F2'],
      ['O5R', '^F3'],
      ['O5S', '^F4'],
      ['O6P', 'S-^F1'],     // Alternative format
      ['O6Q', 'S-^F2'],
      ['O6R', 'S-^F3'],
      ['O6S', 'S-^F4'],
      ['O7P', 'M-^F1'],     // Alternative format
      ['O7Q', 'M-^F2'],
      ['O7R', 'M-^F3'],
      ['O7S', 'M-^F4'],
      ['O8P', 'S-M-^F1'],     // Alternative format
      ['O8Q', 'S-M-^F2'],
      ['O8R', 'S-M-^F3'],
      ['O8S', 'S-M-^F4'],

      ['OA', 'up'],     // Alternative format
      ['OB', 'down'],
      ['OC', 'right'],
      ['OD', 'left'],
      
      // Function keys
      ['OP', 'F1'],
      ['OQ', 'F2'],
      ['OR', 'F3'],
      ['OS', 'F4'],
      ['3P', '^F1'],
      ['3Q', '^F2'],
      ['3R', '^F3'],
      ['3S', '^F4'],
      ['[15~', 'F5'],
      ['[17~', 'F6'],
      ['[18~', 'F7'],
      ['[19~', 'F8'],
      ['[20~', 'F9'],
      ['[21~', 'F10'],
      ['[23~', 'F11'],
      ['[24~', 'F12'],
      
      // Special Meta+Function key bindings
      ['EOP', 'M-F1'],
      ['EOQ', 'M-F2'],
      ['EOR', 'M-F3'],
      ['EOS', 'M-F4'],
      ['E[15~', 'M-F5'],
      ['E[17~', 'M-F6'],
      ['E[18~', 'M-F7'],
      ['E[19~', 'M-F8'],
      ['E[20~', 'M-F9'],
      ['E[21~', 'M-F10'],
      ['E[23~', 'M-F11'],
      ['E[24~', 'M-F12'],
      
      // Navigation keys
      ['[H', 'home'],
      ['[F', 'end'],
      ['[5~', 'pgup'],
      ['[6~', 'pgdn'],
      ['[2~', 'ins'],
      ['[3~', 'fdel'],
      
      // Common vi/emacs key bindings when used with Esc
      ['b', 'esc b'],
      ['f', 'esc f'],
      ['h', 'esc h'],
      ['j', 'esc j'],
      ['k', 'esc k'],
      ['l', 'esc l'],
      ['w', 'esc w'],
      ['d', 'esc d'],
      ['x', 'esc x'],
      ['y', 'esc y'],
      ['p', 'esc p'],
      ['u', 'esc u'],
      ['i', 'esc i'],
      ['a', 'esc a'],
      ['o', 'esc o'],
      ['O', 'esc O'],
      ['0', 'esc 0'],
      ['$', 'esc $'],
      ['G', 'esc G'],
      ['/', 'esc /'],
      ['n', 'esc n'],
      ['N', 'esc N'],
      ['v', 'esc v'],
      ['V', 'esc V'],
      
      // Meta key combinations - Alt+key in most terminals sends ESC+key
      ['1', 'M-1'],
      ['2', 'M-2'],
      ['3', 'M-3'],
      ['4', 'M-4'],
      ['5', 'M-5'],
      ['6', 'M-6'],
      ['7', 'M-7'],
      ['8', 'M-8'],
      ['9', 'M-9'],
      ['0', 'M-0'],
      ['-', 'M--'],
      ['=', 'M-='],
      ['!', 'M-!'],
      ['@', 'M-@'],
      ['#', 'M-#'],
      ['$', 'M-$'],
      ['%', 'M-%'],
      ['^', 'M-^'],
      ['&', 'M-&'],
      ['*', 'M-*'],
      ['(', 'M-('],
      [')', 'M-)'],
      ['_', 'M-_'],
      ['+', 'M-+'],
      ['[', 'M-['],
      [']', 'M-]'],
      ['{', 'M-{'],
      ['}', 'M-}'],
      ['\\', 'M-\\'],
      ['|', 'M-|'],
      [';', 'M-;'],
      ['\'', 'M-\''],
      ['"', 'M-"'],
      [',', 'M-,'],
      ['.', 'M-.'],
      ['/', 'M-/'],
      ['<', 'M-<'],
      ['>', 'M->'],
      ['?', 'M-?'],
      ['`', 'M-`'],
      ['~', 'M-~']
    ];
    
    // Add lowercase and uppercase letters with M- prefix
    for (let i = 97; i <= 122; i++) { // a-z
      const char = String.fromCharCode(i);
      commonBindings.push([char, `M-${char}`]);
    }
    
    for (let i = 65; i <= 90; i++) { // A-Z
      const char = String.fromCharCode(i);
      commonBindings.push([char, `M-${char}`]);
    }
    
    // Add all the bindings to our map
    for (const [key, action] of commonBindings) {
      this.escBindings.set(key, action);
    }
  }

  /**
   * Determine the number of bytes needed for a UTF-8 character based on the first byte
   * @param {number} firstByte - The first byte of the character
   * @returns {number} - Number of bytes needed for this character
   */
  getUTF8CharLength(firstByte) {
    if (firstByte < 0x80) return 1;      // ASCII (0xxxxxxx)
    if (firstByte < 0xC0) return 1;      // Invalid UTF-8, treat as single byte
    if (firstByte < 0xE0) return 2;      // 2-byte character (110xxxxx)
    if (firstByte < 0xF0) return 3;      // 3-byte character (1110xxxx)
    if (firstByte < 0xF8) return 4;      // 4-byte character (11110xxx)
    return 1;                            // Invalid, treat as single byte
  }

  /**
   * Extract a Unicode character from the buffer
   * @returns {Object|null} - {char: string, bytesUsed: number} or null if incomplete
   */
  extractUnicodeChar() {
    if (this.inputBuffer.length === 0) return null;
    
    const firstByte = this.inputBuffer[0];
    const charLength = this.getUTF8CharLength(firstByte);
    
    // Check if we have enough bytes for this character
    if (this.inputBuffer.length < charLength) {
      return null; // Need more bytes
    }
    
    try {
      // Extract the bytes for this character
      const charBytes = this.inputBuffer.slice(0, charLength);
      
      // Convert to string using UTF-8 decoding
      const char = charBytes.toString('utf8');
      
      // Verify it's a valid character (not a replacement character from invalid UTF-8)
      if (char && char !== '\uFFFD') {
        return { char, bytesUsed: charLength };
      }
      
      // If invalid UTF-8, treat the first byte as a single character
      return { char: String.fromCharCode(firstByte), bytesUsed: 1 };
      
    } catch (error) {
      this.debugLog(`Error decoding UTF-8: ${error.message}`);
      // Fall back to treating as single byte
      return { char: String.fromCharCode(firstByte), bytesUsed: 1 };
    }
  }

  /**
   * Start listening for input and buffering it
   */
  startListening() {
    if (this.isListening) return;
    
    this.isListening = true;
    
    // Set stdin to raw mode
    const wasRaw = process.stdin.isRaw;
    if (!wasRaw) {
      try {
        process.stdin.setRawMode(true);
        process.stdin.resume();
      } catch (error) {
        this.debugLog(`Failed to set raw mode: ${error.message}`);
        return;
      }
    }
    
    // Listen for all data and buffer it
    process.stdin.on('data', this.handleInputData.bind(this));
    
    this.debugLog('Started listening for input');
  }
  
  /**
   * Stop listening for input
   */
  stopListening() {
    if (!this.isListening) return;
    
    this.isListening = false;
    process.stdin.removeListener('data', this.handleInputData.bind(this));
    
    try {
      process.stdin.setRawMode(false);
    } catch (error) {
      this.debugLog(`Error unsetting raw mode: ${error.message}`);
    }
    
    this.debugLog('Stopped listening for input');
  }
  
  /**
   * Handle incoming input data by buffering it
   * @param {Buffer} data - Raw input data
   */
  handleInputData(data) {
    // Append to input buffer
    this.inputBuffer = Buffer.concat([this.inputBuffer, data]);
    
    this.debugLog(`Buffered ${data.length} bytes, total buffer: ${this.inputBuffer.length} bytes`);
    
    // Process buffer asynchronously to avoid blocking
    if (!this.isProcessing) {
      setImmediate(() => this.processBuffer());
    }
  }
  
  /**
   * Process the input buffer and extract key sequences
   */
  async processBuffer() {
    if (this.isProcessing || this.inputBuffer.length === 0) return;
    
    this.isProcessing = true;
    
    try {
      while (this.inputBuffer.length > 0) {
        const result = await this.extractNextKey();
        if (result) {
          this.keyQueue.push(result);
        } else {
          // If we can't extract a key, something is wrong - break to avoid infinite loop
          break;
        }
      }
    } catch (error) {
      this.debugLog(`Error processing buffer: ${error.message}`);
    } finally {
      this.isProcessing = false;
    }
  }
  
  /**
   * Extract the next key or key sequence from the buffer
   * @returns {Promise<string|null>} - The extracted key sequence or null if incomplete
   */
  async extractNextKey() {
    if (this.inputBuffer.length === 0) return null;
    
    const firstByte = this.inputBuffer[0];
    const ESC = 27;
    
    // Handle escape sequences
    if (firstByte === ESC) {
      return this.extractEscapeSequence();
    }
    
    // Handle Unicode characters (including ASCII)
    const unicodeResult = this.extractUnicodeChar();
    if (!unicodeResult) {
      // Need more bytes for this character
      return null;
    }
    
    const { char, bytesUsed } = unicodeResult;
    
    // Remove the processed bytes
    this.inputBuffer = this.inputBuffer.slice(bytesUsed);
    
    // Handle control characters and special cases for ASCII
    if (char.length === 1) {
      const code = char.charCodeAt(0);
      if (code <= 31 || code === 127) {
        // Control character - use existing logic
        const key = this.processControlKey(code);
        this.debugLog(`Extracted control key: ${key} (code ${code})`);
        return key;
      }
    }
    
    // For regular Unicode characters (including high ASCII), return as-is
    this.debugLog(`Extracted Unicode character: "${char}" (${bytesUsed} bytes, codes: ${Array.from(Buffer.from(char, 'utf8')).map(b => b.toString(16)).join(' ')})`);
    return char;
  }
  
  /**
   * Extract an escape sequence from the buffer
   * @returns {string|null} - The extracted sequence or null if incomplete
   */
  extractEscapeSequence() {
    const ESC = 27;
    
    if (this.inputBuffer.length < 1 || this.inputBuffer[0] !== ESC) {
      return null;
    }
    
    // If we only have ESC, we need to wait a bit to see if more comes
    if (this.inputBuffer.length === 1) {
      // Use a small delay to distinguish standalone ESC from escape sequences
      return new Promise(resolve => {
        setTimeout(() => {
          if (this.inputBuffer.length === 1 && this.inputBuffer[0] === ESC) {
            // Still just ESC - treat as standalone
            this.inputBuffer = this.inputBuffer.slice(1);
            this.debugLog('Extracted standalone ESC key');
            resolve('esc');
          } else {
            // More data arrived - try to extract sequence
            resolve(this.extractEscapeSequence());
          }
        }, 10); // Short delay for escape sequence detection
      });
    }
    
    // Try to find a complete escape sequence by finding the LONGEST possible match
    const rawSequence = this.inputBuffer.toString('utf8');
    let bestMatch = null;
    let bestLength = 1;
    
    // FIRST: Check for function keys and other multi-character sequences from longest to shortest
    // This prevents '[' from matching before '[15~' can be checked
    const maxLen = Math.min(this.inputBuffer.length, 20);
    
    // Try from longest to shortest to prioritize complete sequences
    for (let len = maxLen; len >= 2; len--) {
      const testSequence = rawSequence.substring(0, len);
      const escPart = testSequence.substring(1);
      
      // Check for exact matches in our escape bindings
      if (this.escBindings.has(escPart)) {
        // Special handling: if this starts with '[', make sure it's a complete sequence
        if (escPart.startsWith('[')) {
          // For '[' sequences, only accept if it ends with a letter or ~ (complete)
          // OR if it's a single character (like for M-[)
          if (escPart.length === 1 || escPart.match(/[A-Za-z~]$/)) {
            bestMatch = this.escBindings.get(escPart);
            bestLength = len;
            this.debugLog(`Found exact escape binding: ESC${escPart} → ${bestMatch}`);
            break;
          }
        } else {
          // Non-[ sequences can be matched immediately
          bestMatch = this.escBindings.get(escPart);
          bestLength = len;
          this.debugLog(`Found exact escape binding: ESC${escPart} → ${bestMatch}`);
          break;
        }
      }
      
      // Check for CSI sequences for this length
      const csiMatch = this.parseCSISequence(escPart);
      if (csiMatch) {
        // CSI sequences should end with a terminating character
        if (escPart.match(/[A-Za-z~]$/)) {
          bestMatch = csiMatch;
          bestLength = len;
          this.debugLog(`Found complete CSI sequence: ESC${escPart} → ${bestMatch}`);
          break;
        }
      }
    }
    
    // If we found a match, use it
    if (bestMatch && bestLength > 1) {
      const processedBytes = this.inputBuffer.slice(0, bestLength);
      this.inputBuffer = this.inputBuffer.slice(bestLength);
      
      const hexCodes = Array.from(processedBytes)
        .map(k => k.toString(16).padStart(2, '0')).join(' ');
      this.debugLog(`Extracted escape sequence: ${bestMatch} (${bestLength} bytes, hex: ${hexCodes})`);
      
      return bestMatch;
    }
    
    // Check for double escape sequences (ESC ESC ...)
    if (this.inputBuffer.length >= 3 && this.inputBuffer[1] === ESC) {
      const escPart = rawSequence.substring(1);
      const doubleEscPart = escPart.substring(1);
      const doubleEscMatch = this.parseDoubleEscapeSequence(doubleEscPart);
      if (doubleEscMatch) {
        this.inputBuffer = this.inputBuffer.slice(3);
        this.debugLog(`Found double escape sequence: ESC ESC ${doubleEscPart} → ${doubleEscMatch}`);
        return doubleEscMatch;
      }
    }
    
    // Check if we might have an incomplete sequence that we should wait for
    const escPart = rawSequence.substring(1);
    
    // If it starts with [ and looks like an incomplete CSI sequence, wait
    if (escPart.match(/^\[[0-9;]*$/) || escPart.match(/^\[[0-9;]+[A-Za-z]?$/)) {
      this.debugLog(`Waiting for more data for potential CSI sequence: ESC${escPart}`);
      return null; // Wait for more data
    }
    
    // If it starts with O and we only have one character after ESC, wait
    if (escPart.length === 1 && escPart === 'O') {
      this.debugLog(`Waiting for more data for potential O sequence: ESC${escPart}`);
      return null; // Wait for more data
    }
    
    // If we have a reasonable length sequence and no match, check simple patterns
    if (this.inputBuffer.length >= 2) {
      const secondChar = String.fromCharCode(this.inputBuffer[1]);
      
      // Simple Alt+key sequences (ESC + single printable character)
      if (this.inputBuffer.length === 2 && secondChar.match(/[a-zA-Z0-9!@#$%^&*()\-_=+[\]{}\\|;:'",.<>?\/`~]/)) {
        this.inputBuffer = this.inputBuffer.slice(2);
        const altKey = `M-${secondChar}`;
        this.debugLog(`Extracted Alt+key sequence: ${altKey}`);
        return altKey;
      }
    }
    
    // If we have more than reasonable escape sequence length and no match,
    // treat the ESC as standalone
    if (this.inputBuffer.length > 20) {
      this.inputBuffer = this.inputBuffer.slice(1);
      this.debugLog('Extracted standalone ESC (sequence too long, no match)');
      return 'esc';
    }
    
    // Not enough data yet or ambiguous, wait for more
    this.debugLog(`Waiting for more data, current buffer length: ${this.inputBuffer.length}, escPart: "${escPart}"`);
    return null;
  }
  
  /**
   * Parse CSI sequences with modifiers (same as original)
   */
  parseCSISequence(escPart) {
    if (!escPart.startsWith('[') || !escPart.includes(';')) {
      return null;
    }
    
    try {
      // Alt+Function key format: [1;3P, [1;3Q, etc.
      const altFnMatch = escPart.match(/^\[1;3([PQRS])$/);
      if (altFnMatch) {
        const fnKey = {
          'P': 'F1', 'Q': 'F2', 'R': 'F3', 'S': 'F4'
        }[altFnMatch[1]];
        
        if (fnKey) return `M-${fnKey}`;
      }
      
      // Alt+F5-F12: [15;3~, [17;3~, etc.
      const altHighFnMatch = escPart.match(/^\[(\d+);3~$/);
      if (altHighFnMatch) {
        const fnMap = {
          '15': 'F5', '17': 'F6', '18': 'F7', '19': 'F8',
          '20': 'F9', '21': 'F10', '23': 'F11', '24': 'F12'
        };
        
        const fnKey = fnMap[altHighFnMatch[1]];
        if (fnKey) return `M-${fnKey}`;
      }
      
      // General CSI format
      const match = escPart.match(/\[(\d+);(\d+)([~A-Z])/);
      if (!match) return null;
      
      const [, numStr, modStr, key] = match;
      const num = parseInt(numStr, 10);
      const mod = parseInt(modStr, 10);
      
      let baseKey = '';
      
      switch (key) {
        case 'A': baseKey = 'up'; break;
        case 'B': baseKey = 'down'; break;
        case 'C': baseKey = 'right'; break;
        case 'D': baseKey = 'left'; break;
        case 'H': baseKey = 'home'; break;
        case 'F': baseKey = 'end'; break;
        case 'P': baseKey = 'F1'; break;
        case 'Q': baseKey = 'F2'; break;
        case 'R': baseKey = 'F3'; break;
        case 'S': baseKey = 'F4'; break;
        case '~':
          switch (num) {
            case 1: case 7: baseKey = 'home'; break;
            case 2: baseKey = 'ins'; break;
            case 3: baseKey = 'fdel'; break;
            case 4: case 8: baseKey = 'end'; break;
            case 5: baseKey = 'pgup'; break;
            case 6: baseKey = 'pgdn'; break;
            case 11: case 12: case 13: case 14: baseKey = `F${num - 10}`; break;
            case 15: baseKey = 'F5'; break;
            case 17: baseKey = 'F6'; break;
            case 18: baseKey = 'F7'; break;
            case 19: baseKey = 'F8'; break;
            case 20: baseKey = 'F9'; break;
            case 21: baseKey = 'F10'; break;
            case 23: baseKey = 'F11'; break;
            case 24: baseKey = 'F12'; break;
            default: baseKey = `Key${num}`;
          }
          break;
        default: baseKey = `Key${key}`;
      }
      
      const modifierMap = {
        2: ['Shift'],
        3: ['Alt'],
        4: ['Shift', 'Alt'],
        5: ['Control'],
        6: ['Shift', 'Control'],
        7: ['Alt', 'Control'],
        8: ['Shift', 'Alt', 'Control']
      };
      
      const modifiers = modifierMap[mod] || [];
      let resultKey = baseKey;
      
      if (modifiers.includes('Control')) {
        resultKey = `^${baseKey}`;
      }
      
      if (modifiers.includes('Alt')) {
        resultKey = `M-${resultKey}`;
      }
      
      if (modifiers.includes('Shift')) {
        if (!(baseKey.length === 1 && baseKey === baseKey.toUpperCase() && baseKey.match(/[A-Z]/))) {
          resultKey = `S-${resultKey}`;  
        }
      }
      
      return resultKey;
    } catch (e) {
      this.debugLog(`Error parsing CSI sequence: ${e.message}`);
      return null;
    }
  }
  
  /**
   * Parse double-escape sequences (same as original)
   */
  parseDoubleEscapeSequence(remainingPart) {
    for (const [escSeq, action] of this.escBindings.entries()) {
      if (remainingPart === escSeq || remainingPart.replace(/ /g, '') === escSeq) {
        if (action.startsWith('F') || action.startsWith('fdel')
        || action.startsWith('del') || action.startsWith('back')
        || action.startsWith('up') || action.startsWith('down')
        || action.startsWith('left') || action.startsWith('right')
        || action.startsWith('home') || action.startsWith('end')
        || action.startsWith('esc')
        || action.startsWith('pg') || action.startsWith('ins')) {
          return `M-${action}`;
        }
      }
    }
    return null;
  }
  
  /**
   * Process a control key character (same as original)
   */
  processControlKey(code) {
    switch (code) {
      case 0: return '^space';
      case 8: return 'back';
      case 9: return 'tab';
      case 13: return 'return';
      case 27: return 'esc';
      case 28: return '^\\';
      case 29: return '^]';
      case 30: return '^^';
      case 31: return '^_';
      case 32: return 'space';
      case 127: return 'del';
      default:
        if (code >= 1 && code <= 26) {
          const letter = String.fromCharCode(code + 64);
          return `^${letter}`;
        } else if (code <= 126) {
          return String.fromCharCode(code);
        }
      return `[${code}]`;
    }
  }
  
  /**
   * Get the next key from the queue
   * This is the main entry point - call this instead of the old getKey()
   * @returns {Promise<string>} - The next key or key sequence
   */
  async getKey() {
    // Start listening if not already
    if (!this.isListening) {
      this.startListening();
    }
    
    // If we have keys in the queue, return the first one
    if (this.keyQueue.length > 0) {
      const key = this.keyQueue.shift();
      this.debugLog(`Returning queued key: ${key}`);
      return key;
    }
    
    // Wait for a key to be available
    return new Promise((resolve) => {
      const checkForKey = () => {
        if (this.keyQueue.length > 0) {
          const key = this.keyQueue.shift();
          this.debugLog(`Returning waited key: ${key}`);
          resolve(key);
        } else {
          setImmediate(checkForKey);
        }
      };
      
      checkForKey();
    });
  }
  
  /**
   * Check if there are keys available without waiting
   * @returns {boolean} - True if keys are available
   */
  hasKeys() {
    return this.keyQueue.length > 0;
  }
  
  /**
   * Get multiple keys at once (useful for processing paste)
   * @param {number} maxKeys - Maximum number of keys to return
   * @returns {string[]} - Array of available keys
   */
  getAvailableKeys(maxKeys = Infinity) {
    const keys = [];
    while (keys.length < maxKeys && this.keyQueue.length > 0) {
      keys.push(this.keyQueue.shift());
    }
    return keys;
  }
  
  /**
   * Clear the input buffer and key queue (emergency cleanup)
   */
  clearBuffers() {
    this.inputBuffer = Buffer.alloc(0);
    this.keyQueue = [];
    this.debugLog('Cleared input buffers');
  }
}

module.exports = { DirectKeyboardHandler };
