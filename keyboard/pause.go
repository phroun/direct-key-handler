package keyboard

import (
	"fmt"

	"golang.org/x/term"
)

// Pause hands the terminal back to the shell for a while without stopping the
// handler, and Resume takes it again. The case they exist for is job control: a
// program suspending itself has to put the terminal back in cooked mode before
// it stops, and back in raw mode when it is continued in the foreground.
//
// Stop cannot serve for this. It closes the channel both goroutines watch, and a
// closed channel cannot be reopened, so a stopped handler stays stopped. The
// reader goroutine is also usually blocked in a Read that only returns on input,
// so it would not be gone by the time a Start ran anyway. Pause leaves both
// goroutines where they are: a stopped process reads nothing, and the next
// bytes the reader sees are ones typed after the program has the terminal back.
//
// Pausing releases every key still down, the same as losing focus does. The
// key-ups for anything held across a suspend are typed into the shell, so they
// never arrive here, and without the release the presses would stand for good
// downstream.
//
// Both are safe to call on a handler that is not running or does not manage the
// terminal; they then do nothing to the terminal, and Pause still releases held
// keys. Pausing twice or resuming twice is a no-op.
func (h *Handler) Pause() error {
	h.mu.Lock()
	if !h.running || h.paused {
		h.mu.Unlock()
		return nil
	}
	h.paused = true
	state := h.originalTermState
	h.mu.Unlock()

	h.ReleaseHeldKeys()
	h.forgetModifierSides()

	if h.managesTerminal && state != nil {
		if err := term.Restore(h.terminalFd, state); err != nil {
			return fmt.Errorf("failed to restore terminal: %w", err)
		}
		h.debug("Terminal restored for pause")
	}
	return nil
}

// Resume puts the terminal back in raw mode after a Pause.
//
// The state it saves for the next restore is the one the terminal is in NOW,
// not the one Start found. The shell may have changed its settings while the
// program was stopped, and what the program should hand back later is what the
// shell last left.
func (h *Handler) Resume() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running || !h.paused {
		return nil
	}
	if h.managesTerminal {
		state, err := term.MakeRaw(h.terminalFd)
		if err != nil {
			return fmt.Errorf("failed to re-enable raw mode: %w", err)
		}
		h.originalTermState = state
		h.debug("Terminal set to raw mode on resume")
	}
	h.paused = false
	return nil
}

// IsPaused reports whether the handler is running but paused.
func (h *Handler) IsPaused() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paused
}
