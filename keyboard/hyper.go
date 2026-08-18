package keyboard

// Hyper, on a keyboard that has no Hyper key.
//
// Almost nothing ships one. X11 can map a spare cap to it and a Space Cadet
// keyboard had a real one, but on the machines people actually type at the
// modifier exists in the vocabulary and nowhere on the hardware — so a chord
// naming it can be written and never pressed.
//
// The graphical host has answered this for a while by SYNTHESIZING it: hold
// both the left and the right Ctrl, or both Alts, and the chord is promoted to
// Hyper. Two of the same modifier is a thing no ordinary binding means, so
// nothing is taken away by spending it.
//
// The terminal host could not do the same, for a plain reason: the modifier
// field a key arrives with says "Ctrl", not "left Ctrl", and the events that
// WOULD say which side — the modifier keys' own presses — were never sent. The
// report_all_keys enhancement is what starts sending them, and kitty numbers
// the sides apart (57442 left Ctrl, 57448 right). So the same rule can be kept
// here now, and the two hosts can mean one thing by Hyper.

// doubledSides tracks which side of each doubling modifier is currently held.
//
// Only Ctrl and Mega double. Shift is deliberately not among them: it produces
// text, so a doubled Shift would swallow ordinary capital letters. Super and
// Micro are left alone because a keyboard that has them at all rarely has two.
type doubledSides struct {
	ctrlLeft, ctrlRight bool
	megaLeft, megaRight bool
}

// noteModifierSide records a modifier key going down or coming up.
//
// Called for the modifier keys' OWN events, which arrive only under
// report_all_keys — so on a terminal that does not send them, nothing is ever
// recorded, no promotion ever fires, and the vocabulary degrades to exactly
// what it was before rather than to something wrong.
func (h *Handler) noteModifierSide(name, side string, eventType int) {
	down := eventType != 3 // press or repeat
	h.mu.Lock()
	defer h.mu.Unlock()
	switch name {
	case "Ctrl":
		if side == "Right" {
			h.sides.ctrlRight = down
		} else {
			h.sides.ctrlLeft = down
		}
	case "Mega":
		if side == "Right" {
			h.sides.megaRight = down
		} else {
			h.sides.megaLeft = down
		}
	}
}

// promoteHyper rewrites a modifier value when a doubled side modifier is held,
// spending the doubled one on Hyper and leaving everything else alone.
//
//	LCtrl+RCtrl+X      -> H-X     both Ctrl becomes Hyper
//	LMega+RMega+X      -> H-X     both Mega becomes Hyper
//	LMega+RMega+Ctrl+X -> H-^X    Hyper beside a single Ctrl
//	LCtrl+RCtrl+Mega+X -> M-H-x   Hyper beside a single Mega
//
// The doubled modifier is CONSUMED, which is what makes the promotion a
// promotion rather than an addition: LCtrl+RCtrl+X must not also be a Control
// chord, or every Hyper binding would shadow one.
func (h *Handler) promoteHyper(mod int) int {
	h.mu.Lock()
	bothCtrl := h.sides.ctrlLeft && h.sides.ctrlRight
	bothMega := h.sides.megaLeft && h.sides.megaRight
	h.mu.Unlock()
	if !bothCtrl && !bothMega {
		return mod
	}
	if mod < 1 {
		mod = 1
	}
	bits := mod - 1
	if bothCtrl {
		bits &^= 4 // ctrl
	}
	if bothMega {
		bits &^= 2 // mega
	}
	bits |= 16 // hyper
	return bits + 1
}

// forgetModifierSides drops every side this package believes is held.
//
// The keyboard has gone elsewhere, so the key-ups for anything still down are
// delivered there and never arrive here. Without this a user who tabbed away
// mid-chord would come back to a terminal that thought both Ctrls were still
// down, and every subsequent keystroke would arrive promoted to Hyper.
func (h *Handler) forgetModifierSides() {
	h.mu.Lock()
	h.sides = doubledSides{}
	h.mu.Unlock()
}
