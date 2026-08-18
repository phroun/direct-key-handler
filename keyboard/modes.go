package keyboard

import "sort"

// The modes a keyboard is IN, as opposed to the keys it sends.
//
// A latch like the pad's lock is not a keystroke and not a chord: it is a
// standing state that decides what other keys MEAN, and something on screen
// usually has to say so. Every status bar that ever showed CAPS or NUM was
// answering this, and each application had to reach into the keyboard layer its
// own way to do it.
//
// So the states are published under stable tokens with token values, and a
// consumer reads whichever it recognizes and draws it however it likes. New
// states can be added here without an API change, and a host can add its own
// with SetMode — a mode this package has never heard of is stored and reported
// beside the ones it keeps itself.
//
// A mode this package cannot determine is ABSENT rather than false. The
// difference is the one a display cares about: "the lock is off" and "we cannot
// see the lock from here" are not the same picture.

// Mode is one state, named by a token and valued by a token.
type Mode struct {
	Name  string
	Value string
}

// The states this package keeps.
//
// ModeFocus is only ever known if the application asked the terminal for focus
// reporting and the terminal obliged; until one arrives, nothing here can tell
// whether the window has the keyboard.
const (
	ModeNumLock  = "num"   // the pad's lock: are the dual-legend caps digits?
	ModeCapsLock = "caps"  // as of the last key that carried a modifier field
	ModeFocus    = "focus" // does this terminal have the keyboard?
)

// The two values a latch takes. A mode that is not a latch may use others.
const (
	ModeOn  = "on"
	ModeOff = "off"
)

// modeLatch is a state we only learn by being told, and might not have been.
type modeLatch struct {
	known bool
	on    bool
}

// modeValue spells a latch.
func modeValue(on bool) string {
	if on {
		return ModeOn
	}
	return ModeOff
}

// Modes returns every state this package can currently answer for, sorted by
// name. A state it cannot see is not in the list.
func (h *Handler) Modes() []Mode {
	h.mu.Lock()
	defer h.mu.Unlock()

	// The pad's lock is always answerable, including on a system that has no
	// lock at all: there the pad is permanently locked and this permanently
	// says so, which is what those caps do. A host can therefore draw the
	// indicator everywhere, which is most useful exactly where the OS draws
	// none.
	modes := []Mode{{ModeNumLock, modeValue(h.numLock.on)}}
	if h.caps.known {
		modes = append(modes, Mode{ModeCapsLock, modeValue(h.caps.on)})
	}
	if h.focus.known {
		modes = append(modes, Mode{ModeFocus, modeValue(h.focus.on)})
	}
	for name, value := range h.extraModes {
		modes = append(modes, Mode{name, value})
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i].Name < modes[j].Name })
	return modes
}

// Mode returns one state's value. ok is false when this package cannot tell,
// which is what a display should draw greyed rather than off.
func (h *Handler) Mode(name string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch name {
	case ModeNumLock:
		return modeValue(h.numLock.on), true
	case ModeCapsLock:
		if !h.caps.known {
			return "", false
		}
		return modeValue(h.caps.on), true
	case ModeFocus:
		if !h.focus.known {
			return "", false
		}
		return modeValue(h.focus.on), true
	}
	value, ok := h.extraModes[name]
	return value, ok
}

// SetMode writes a state, and reports whether anything changed.
//
// A name this package does not keep becomes a mode of its own, so a host with a
// state of its own — an insert/overtype mode, a recording light — can publish
// it here and let one display draw everything. Setting such a mode to the empty
// string removes it.
//
// The three states this package keeps are a different matter: it goes on
// watching the keyboard, so a write to one of them is a BELIEF, held until the
// next event contradicts it. Writing "num" is how a host moves the simulated
// lock on a system with no latch behind the cap; on a system that has one, the
// OS still owns it and the next keystroke will say so. The two latches take
// only "on" and "off"; any other value is ignored.
func (h *Handler) SetMode(name, value string) bool {
	changed, mode := h.setMode(name, value)
	if changed {
		h.announceMode(mode)
	}
	return changed
}

func (h *Handler) setMode(name, value string) (bool, Mode) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch name {
	case ModeNumLock, ModeCapsLock, ModeFocus:
		if value != ModeOn && value != ModeOff {
			return false, Mode{}
		}
		on := value == ModeOn
		switch name {
		case ModeNumLock:
			if h.numLock.on == on {
				return false, Mode{}
			}
			h.numLock.on = on
		case ModeCapsLock:
			if h.caps.known && h.caps.on == on {
				return false, Mode{}
			}
			h.caps = modeLatch{known: true, on: on}
		case ModeFocus:
			if h.focus.known && h.focus.on == on {
				return false, Mode{}
			}
			h.focus = modeLatch{known: true, on: on}
		}
		return true, Mode{name, value}
	}

	if value == "" {
		if _, ok := h.extraModes[name]; !ok {
			return false, Mode{}
		}
		delete(h.extraModes, name)
		return true, Mode{name, ""}
	}
	if h.extraModes == nil {
		h.extraModes = make(map[string]string)
	}
	if h.extraModes[name] == value {
		return false, Mode{}
	}
	h.extraModes[name] = value
	return true, Mode{name, value}
}

// announceMode fires OnMode outside the handler's lock, so a consumer is free
// to call back in — to read the rest of the modes for a status bar, say.
func (h *Handler) announceMode(m Mode) {
	if h.OnMode != nil {
		h.OnMode(m)
	}
}

// noteCapsLock reads what one arriving key says about CapsLock.
//
// The bit rides on every key the kitty protocol reports, so the state is only
// ever as fresh as the last keystroke: a user who presses CapsLock and then
// goes to lunch leaves a stale indicator behind. That is a real limit of asking
// the keyboard instead of the OS, and it is why this is reported as a state we
// were TOLD rather than one we can poll.
func (h *Handler) noteCapsLock(mod int) (bool, bool) {
	on := mod > 1 && (mod-1)&kittyCapsLockBit != 0
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.caps.known && h.caps.on == on {
		return false, on
	}
	h.caps = modeLatch{known: true, on: on}
	return true, on
}

// noteFocus records the terminal's focus report.
func (h *Handler) noteFocus(focused bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.focus.known && h.focus.on == focused {
		return false
	}
	h.focus = modeLatch{known: true, on: focused}
	return true
}
