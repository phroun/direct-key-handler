package keyboard

import (
	"testing"
)

// F13 through F20 arrive on the legacy path, not only under the kitty protocol.
//
// The tilde table stopped at F12, and an unlisted number is not merely ignored
// — it falls through and is read byte by byte. So a terminal sending the xterm
// sequence for F15 delivered FIVE keystrokes, "Escape [ 2 8 ~", typing "[28~"
// into the application. The names existed and the kitty path produced them, so
// the constants looked reachable while the wire that actually carries them on
// most terminals could not produce one.
func TestHighFunctionKeysArriveOnTheLegacyPath(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"\x1b[25~", "F13", ""},
		{"\x1b[26~", "F14", ""},
		{"\x1b[28~", "F15", "the key a VT220 keyboard labels Help"},
		{"\x1b[29~", "F16", "the key it labels Do"},
		{"\x1b[31~", "F17", ""},
		{"\x1b[32~", "F18", ""},
		{"\x1b[33~", "F19", ""},
		{"\x1b[34~", "F20", ""},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q (%s) parsed as %v, want [%s]", tc.raw, tc.what, got, tc.want)
		}
	}
}

// A key is classified by its whole keycode, not by the last byte of it.
//
// isSymbolKey switched on byte(keycode), so it answered for anything congruent
// to one of its eleven characters modulo 256 — and it is consulted BEFORE the
// table of named keys, so it won. F20 (57383) typed an apostrophe, keypad 4
// (57403) typed a semicolon, keypad 6 (57405) typed an equals sign. Nothing
// looked broken: what came out was a real key that any keymap would bind.
//
// The property is asserted rather than the three cases, because the three are
// only the ones that currently collide. Any keycode this package learns later
// could land on the same trap.
func TestKeysAreClassifiedByTheWholeKeycode(t *testing.T) {
	for _, r := range []rune{'`', ',', '.', '/', ';', '\'', '[', ']', '\\', '-', '='} {
		if !isSymbolKey(int(r)) {
			t.Errorf("isSymbolKey(%q) is false; it is one of the symbols", r)
		}
		// The same character 256, 512 and 0xE000 higher is a different key.
		for _, offset := range []int{256, 512, 0xE000} {
			if isSymbolKey(int(r) + offset) {
				t.Errorf("isSymbolKey(%d) is true because its low byte is %q; "+
					"that keycode is not a symbol and this test runs before "+
					"the named-key table", int(r)+offset, r)
			}
		}
	}
}

// One key, one name, whichever channel reports it. A terminal with the kitty
// protocol and one without must not disagree about what was pressed — that is
// the whole reason a name exists rather than a number per transport.
func TestBothChannelsAgreeOnTheHighFunctionKeys(t *testing.T) {
	for _, tc := range []struct{ legacy, kitty string }{
		{"\x1b[25~", "\x1b[57376u"},
		{"\x1b[28~", "\x1b[57378u"},
		{"\x1b[29~", "\x1b[57379u"},
		{"\x1b[34~", "\x1b[57383u"},
	} {
		l, k := feedKeys(t, tc.legacy), feedKeys(t, tc.kitty)
		if len(l) != 1 || len(k) != 1 || l[0] != k[0] {
			t.Errorf("%q gave %v but %q gave %v; the same key has two names",
				tc.legacy, l, tc.kitty, k)
		}
	}
}

// Modifiers and event types ride on these exactly as they do on the rest of
// the tilde family — the sub-parameter carrying the event type is read here
// too, so a release is not delivered as a second press.
func TestHighFunctionKeysTakeModifiersAndEvents(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[28;5~", "C-F15"},
		{"\x1b[29;2~", "S-F16"},
		{"\x1b[25;3~", "M-F13"},
		// The event forms are fed after their press: a release names the key
		// its press named, and one arriving alone is dropped.
		{"\x1b[28;1~\x1b[28;1:3~", "F15:Release"},
		{"\x1b[29;5~\x1b[29;5:2~", "C-F16:Repeat"},
	} {
		if got := feedKeys(t, tc.raw); len(got) == 0 || got[len(got)-1] != tc.want {
			t.Errorf("%q parsed as %v, want the last key to be %q", tc.raw, got, tc.want)
		}
	}
}

// The gaps in xterm's numbering are real and must stay unclaimed.
//
// 16, 22, 27, 30 and 35 were left empty by DEC, and nothing sends them. Naming
// one would invent a key, and worse, would silently answer for whatever a
// future terminal decides to put there.
func TestTheGapsInTheTildeNumberingAreNotKeys(t *testing.T) {
	for _, raw := range []string{"\x1b[16~", "\x1b[22~", "\x1b[27~", "\x1b[30~", "\x1b[35~"} {
		got := feedKeys(t, raw)
		if len(got) == 1 {
			t.Errorf("%q produced the single key %q; that number names nothing", raw, got[0])
		}
	}
}

// The keys below the new ones still answer as they did. This table is on the
// path every ordinary navigation key takes, so an edit to it is an edit to
// Home, Insert, forward delete, End and the page keys.
func TestTheOriginalTildeKeysAreUnchanged(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1b[1~", "Home"}, // DEC called this key Find
		{"\x1b[2~", "Insert"},
		{"\x1b[3~", "FDel"}, // DEC called it Remove
		{"\x1b[4~", "End"},  // DEC called it Select
		{"\x1b[5~", "PageUp"},
		{"\x1b[6~", "PageDown"},
		{"\x1b[15~", "F5"},
		{"\x1b[24~", "F12"},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
		}
	}
}
