package keyboard

// Every modifier prefix in this package is written here, in one order.
//
// Order is not meaning — the sequence processor sorts a stack before comparing
// — but a producer that emits ONE fixed order is what lets a consumer compare
// strings without knowing which producer it is listening to.

// keyMods is the set of modifiers held with a key, as this vocabulary spells
// them. A caller that has SPENT one — Control becoming a caret, Shift absorbed
// into a shifted character — clears the field to say so, rather than omitting
// it from a list and leaving the next reader to wonder.
type keyMods struct {
	ctrl  bool // C-
	glyph bool // G-  AltGr / ISO_Level3_Shift
	mega  bool // M-
	micro bool // m-  the modifier a Space Cadet keyboard had its own key for
	shift bool // S-
	super bool // s-  Super / Command
	hyper bool // H-
}

// modsFromKitty decodes a kitty modifier value — 1-indexed, exactly as it
// arrives on the wire — into the set. The two LATCHES, CapsLock (64) and
// NumLock (128), are deliberately not among them: a latch is not something a
// hand is holding, and reading one as a modifier is the bug that made every
// second press of the pad's lock cap come out as a chord.
func modsFromKitty(mod int) keyMods {
	if mod < 1 {
		mod = 1
	}
	bits := mod - 1
	return keyMods{
		shift: bits&1 != 0,
		mega:  bits&2 != 0,
		ctrl:  bits&4 != 0,
		super: bits&8 != 0,
		hyper: bits&16 != 0,
		micro: bits&32 != 0,
		glyph: bits&256 != 0,
	}
}

// prefix spells a modifier set in the canonical order: C- G- M- m- S- s- H-.
//
// It follows the order macOS renders modifiers, extended with the ones a Mac
// keyboard has no cap for, and it matches modifierRank in the sequence
// processor exactly. What comes AFTER this prefix is the key's own origin and
// then the key: P- or p- for the pad, then the caret when Control is written
// against a character, then the base — C- G- M- m- S- s- H- P- p- ^Key.
func (m keyMods) prefix() string {
	s := ""
	if m.ctrl {
		s += "C-"
	}
	if m.glyph {
		s += "G-"
	}
	if m.mega {
		s += "M-"
	}
	if m.micro {
		s += "m-"
	}
	if m.shift {
		s += "S-"
	}
	if m.super {
		s += "s-"
	}
	if m.hyper {
		s += "H-"
	}
	return s
}

// sideMod names the family a bare modifier event belongs to, by which side of
// the keyboard the key sits on.
//
// A producer that cannot tell the sides apart — or a modifier that has only one
// key — says so by giving no side at all, and gets the bare "Mod". That is the
// modifier's version of the mouse having no button to name: not a default, an
// absence, and one a consumer can act on rather than having to guess.
func sideMod(side string) string {
	switch side {
	case "Left":
		return "LMod"
	case "Right":
		return "RMod"
	}
	return "Mod"
}
