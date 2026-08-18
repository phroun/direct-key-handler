package keyboard

// The pad's lock is a STATE, and a state is not a key.
//
// One cap carries both ideas and always has: HID calls usage 0x53 "Keyboard
// Num Lock and Clear", and SDL calls the same scancode NUMLOCKCLEAR with the
// comment "num lock on PC, clear on Mac keyboards". Two standards looked at
// this key and declined to split it, which is the hint: the difference is not
// a property of the key, it is a property of the system the key is plugged
// into.
//
// So the cap is handled as what it does rather than as what it is called.
// Pressed alone it is a LOCK: it is eaten here, it moves the pad's lock state,
// and nothing is emitted for it. Nobody writes a binding against "NumLock" —
// its entire meaning is the state, and that state already decides what every
// other pad key is named. A name you would never sensibly bind does not belong
// in a binding vocabulary.
//
// Pressed WITH a modifier it is an ordinary key, and it is called Clear —
// because Clear is an action, which is the kind of thing bindings are for, and
// because that is what the cap says on the keyboards that have no lock behind
// it.

const (
	// heldModifierBits are the modifier bits a hand is HOLDING DOWN when a key
	// goes down: exactly the ones this vocabulary spells with a prefix
	// (C- G- M- m- S- s- H-).
	//
	// Stated as what a held modifier IS, never as what it is not. Masking off
	// the LATCHES we happen to know about — CapsLock (64) and NumLock (128) —
	// would fail open: the day ScrollLock, KanaLock or HangulLock gains a bit,
	// it reads as a modifier, and every unmodified press of the lock cap starts
	// arriving as a chord instead of moving the lock. A positive list cannot
	// fail that way, because a latch nobody has thought of yet is simply not on
	// it. SDL already carries a third latch (KMOD_SCROLL) that kitty has no bit
	// for, so this is not hypothetical.
	heldModifierBits = 1 | // S- shift
		2 | // M- mega
		4 | // C- ctrl
		8 | // s- super
		16 | // H- hyper
		32 | // m- micro
		256 // G- glyph

	// kittyNumLockBit is the pad-lock LATCH as kitty reports it, in the same
	// modifier field as the held ones. It is not in heldModifierBits, and that
	// is the whole point.
	kittyNumLockBit = 128

	// kittyClearKey is the cap this file is about: kitty's code for the key HID
	// names "Num Lock and Clear".
	kittyClearKey = 57360

	// The dual-legend caps, which is how a keystroke tells us what the lock is
	// doing without anyone having to press the lock itself. kitty resolves them
	// for us: 57399-57409 are the digits and the decimal point (locked), and
	// 57417-57427 are the navigation actions on the same eleven caps
	// (unlocked). The operators between them carry one legend and say nothing.
	kittyPadLockedLo, kittyPadLockedHi     = 57399, 57409
	kittyPadUnlockedLo, kittyPadUnlockedHi = 57417, 57427
)

// heldModifier reports whether a kitty modifier value — 1-indexed, exactly as
// it arrives on the wire — carries any modifier a hand is holding down.
//
// Latches are not modifiers here. Reading them as such is the bug this guards:
// pressing the lock cap while the lock is already ON arrives with bit 128 set,
// so a naive test sees a chord and every SECOND press would come out as Clear
// while the first was eaten. CapsLock does the same to a user who types in
// capitals.
func heldModifier(mod int) bool {
	if mod < 2 {
		return false
	}
	return (mod-1)&heldModifierBits != 0
}

// numLockState is what this package has worked out about the pad's lock.
//
// latchKnown/hasLatch answer a question that is about the SYSTEM and not the
// keyboard: is there a NumLock here at all? macOS has none — the pad is always
// numeric and the cap says Clear — while X11, Wayland and Windows each keep one
// and hand it to us in every key event. Until a keystroke settles it the answer
// is genuinely unknown, and guessing is what a platform constant would do.
type numLockState struct {
	latchKnown bool
	hasLatch   bool
	on         bool
}

// noteNumLock reads what one arriving key says about the pad's lock, and
// returns the state to announce if it moved.
//
// The detector is a DISAGREEMENT, and it needs no press of the lock cap — which
// matters, because on a Mac that press has no reason ever to happen. A pad key
// arriving with its locked legend while the latch bit is clear can only mean
// there is no latch: a system that has one and has it off sends the navigation
// legend instead. That fires on the first pad keystroke of the session.
//
// Where a latch does exist it is AUTHORITATIVE and resyncs on every key, not
// just on pad keys — the user can toggle it while this process is not focused
// and come back, and our own count would be stale. Counting is the fallback for
// a system with nothing to be stale against.
func (h *Handler) noteNumLock(keycode, mod int) (changed bool, on bool) {
	bit := mod > 1 && (mod-1)&kittyNumLockBit != 0

	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.numLock.on

	switch {
	case bit:
		// The latch exists and it is on. Nothing else can produce this bit.
		h.numLock.latchKnown, h.numLock.hasLatch, h.numLock.on = true, true, true
	case keycode >= kittyPadLockedLo && keycode <= kittyPadLockedHi:
		// A digit with the bit clear: no latch on this system, and a pad with
		// no latch is permanently locked, which is what its caps say.
		h.numLock.latchKnown, h.numLock.hasLatch, h.numLock.on = true, false, true
	case keycode >= kittyPadUnlockedLo && keycode <= kittyPadUnlockedHi:
		// A navigation legend: there IS a latch, and it is off.
		h.numLock.latchKnown, h.numLock.hasLatch, h.numLock.on = true, true, false
	case h.numLock.hasLatch:
		// Any other key on a system known to have a latch: the absent bit says
		// the latch is off, and this is the resync that keeps us honest across
		// a toggle made while we were not looking.
		h.numLock.on = false
	}
	return h.numLock.on != before, h.numLock.on
}

// toggleNumLock moves the lock because the cap was pressed alone.
//
// Only where we own the state. On a system with a real latch the OS has already
// moved it and will tell us in the modifier field of the next key that arrives
// — toggling here as well would race that and could land inverted. While the
// question is still unsettled we toggle anyway: if it turns out there is a
// latch, the first bit to arrive overwrites this; if there is not, this WAS the
// state.
func (h *Handler) toggleNumLock() (changed bool, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.numLock.latchKnown && h.numLock.hasLatch {
		return false, h.numLock.on
	}
	h.numLock.on = !h.numLock.on
	return true, h.numLock.on
}

// NumLock reports whether the number pad is locked — whether its dual-legend
// caps mean their digits rather than their navigation actions.
//
// Answerable on every system, including the ones that have no NumLock: there
// the pad is permanently locked and this permanently reports true, which is
// what those caps do. A host drawing an on-screen indicator can therefore draw
// one everywhere, which is most useful exactly where the OS provides none.
//
// It starts out true, before anything has been typed, because a locked pad is
// what the printed legends promise and the overwhelmingly common state.
func (h *Handler) NumLock() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.numLock.on
}

// announceNumLock fires OnNumLock outside the handler's lock, so a consumer is
// free to call back in.
func (h *Handler) announceNumLock(on bool) {
	if h.OnNumLock != nil {
		h.OnNumLock(on)
	}
}
