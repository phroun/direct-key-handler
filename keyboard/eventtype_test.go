package keyboard

import (
	"testing"
)

// The kitty protocol's event type rides in the MODIFIER field, and it does so
// in every CSI form — not only the "u" one.
//
// Only the "u" form was read. Everywhere else the sub-parameter was parsed as
// part of the modifier number, the parse gave up at the colon and answered
// "no modifiers", and what came out was an ordinary press. So a release of an
// arrow key, of Home, of F1 or of F5 did not go missing — it arrived as a
// SECOND press of that key, indistinguishable from the real one. A terminal
// asked for event reporting therefore made every held key act twice.
// Each case feeds the PRESS first, because a release is now reported under the
// name its press was given, and one arriving alone is dropped as non-conformant
// — you cannot release a key you never pressed. Reading the event type out of
// the sequence is what makes that pairing possible at all, which is still what
// this tests; the press is scaffolding.
func TestEventTypeIsReadFromEveryCSIFamily(t *testing.T) {
	for _, tc := range []struct{ press, raw, want string }{
		// Cursor keys: CSI 1 ; <mod>:<event> <A-D>
		{"\x1b[1;1A", "\x1b[1;1:3A", "Up:Release"},
		{"\x1b[1;1B", "\x1b[1;1:2B", "Down:Repeat"},
		{"", "\x1b[1;1:1C", "Right"},
		{"\x1b[1;5D", "\x1b[1;5:3D", "C-Left:Release"},
		// Home and End share the cursor-key shape.
		{"\x1b[1;1H", "\x1b[1;1:3H", "Home:Release"},
		{"\x1b[1;2F", "\x1b[1;2:3F", "S-End:Release"},
		// F1-F4 carry letter finals.
		{"\x1b[1;1P", "\x1b[1;1:3P", "F1:Release"},
		{"\x1b[1;1S", "\x1b[1;1:2S", "F4:Repeat"},
		// The tilde family: CSI <num> ; <mod>:<event> ~
		{"\x1b[15;1~", "\x1b[15;1:3~", "F5:Release"},
		{"\x1b[5;1~", "\x1b[5;1:3~", "PageUp:Release"},
		{"\x1b[3;5~", "\x1b[3;5:3~", "C-FDel:Release"},
		// The "u" form already did this, and must go on doing it.
		{"\x1b[97;1u", "\x1b[97;1:3u", "a:Release"},
		// A press is unmarked, however it is spelled.
		{"", "\x1b[1;1A", "Up"},
		{"", "\x1b[15;1~", "F5"},
	} {
		got := feedKeys(t, tc.press+tc.raw)
		if len(got) == 0 || got[len(got)-1] != tc.want {
			t.Errorf("%q parsed as %v, want the last key to be %q", tc.raw, got, tc.want)
		}
	}
}

// The CSI families do not share a number space, so one key cannot consume
// another's entry.
//
// They identify a key three different ways — the "u" form by keycode, the "~"
// form by number, the cursor and F1-F4 forms by their final letter alone — and
// those numbers overlap: "CSI 3 ~" is forward delete while "CSI 3 ; 5 u" is
// Ctrl-C. Remembered under a bare integer, pressing one would answer for the
// other's release.
func TestTheCSIFamiliesAreHeldSeparately(t *testing.T) {
	// The cursor keys, Home, End and F1-F4 ALL carry "1" as their first
	// parameter — the final letter is the only thing telling them apart. Held
	// under that parameter they would share one entry, so pressing Up would
	// answer for Left's release, and the four arrows would be one key.
	got := feedKeys(t, "\x1b[1;1A\x1b[1;1:3D")
	if len(got) != 1 || got[0] != "Up" {
		t.Errorf("Up press then LEFT release -> %v, want just [Up]; the arrows "+
			"are sharing one entry", got)
	}
	got = feedKeys(t, "\x1b[1;1A\x1b[1;1:3P")
	if len(got) != 1 || got[0] != "Up" {
		t.Errorf("Up press then F1 release -> %v, want just [Up]", got)
	}
	// And across families: a tilde number must not answer for a cursor final.
	got = feedKeys(t, "\x1b[15;1~\x1b[1;1:3A")
	if len(got) != 1 || got[0] != "F5" {
		t.Errorf("F5 press then Up release -> %v, want just [F5]", got)
	}
	// Each still releases correctly under its own identity, so the separation
	// is not just a way of dropping everything.
	got = feedKeys(t, "\x1b[1;1A\x1b[1;1B\x1b[1;1:3B\x1b[1;1:3A")
	want := []string{"Up", "Down", "Down:Release", "Up:Release"}
	if len(got) != 4 {
		t.Fatalf("Up and Down held together -> %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Up and Down held together -> %v, want %v", got, want)
			break
		}
	}
}

// The erase-left key carries one name whichever channel reports it.
//
// It is a single physical key. A terminal sends BS (8) or DEL (127) for it and
// they are told apart so neither is confused with forward delete — that is what
// "Backspace" is for. Under the kitty protocol no byte is reported at all: the
// key is, as keycode 127, and its name is "Delete".
//
// This table said "Backspace" there, so the same key arrived as "del" over a
// legacy terminal and "back" over kitty. Those sit in different fallback groups
// in an application's keymap, so a binding written against one went silent when
// the other channel delivered it — and switching terminals is all it took.
func TestTheEraseLeftKeyIsNamedTheSameOnEveryChannel(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"\x7f", "Delete", "the DEL byte"},
		{"\x1b[127u", "Delete", "the kitty keycode for the same key"},
		{"\x1b[127;5u", "C-Delete", "and it keeps its name under a modifier"},
		// The byte-8 convention stays distinct: that is the whole point of
		// having a second name.
		{"\x08", "Backspace", "the BS byte, a terminal using the other convention"},
		// kitty's own DELETE is forward delete, and arrives by the tilde path.
		{"\x1b[3~", "FDel", "forward delete, a different key entirely"},
	} {
		got := feedKeys(t, tc.raw)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q (%s) parsed as %v, want [%s]", tc.raw, tc.what, got, tc.want)
		}
	}
}
