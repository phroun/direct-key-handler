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
func TestEventTypeIsReadFromEveryCSIFamily(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		// Cursor keys: CSI 1 ; <mod>:<event> <A-D>
		{"\x1b[1;1:3A", "Up:Release"},
		{"\x1b[1;1:2B", "Down:Repeat"},
		{"\x1b[1;1:1C", "Right"},
		{"\x1b[1;5:3D", "C-Left:Release"},
		// Home and End share the cursor-key shape.
		{"\x1b[1;1:3H", "Home:Release"},
		{"\x1b[1;2:3F", "S-End:Release"},
		// F1-F4 carry letter finals.
		{"\x1b[1;1:3P", "F1:Release"},
		{"\x1b[1;1:2S", "F4:Repeat"},
		// The tilde family: CSI <num> ; <mod>:<event> ~
		{"\x1b[15;1:3~", "F5:Release"},
		{"\x1b[5;1:3~", "PageUp:Release"},
		{"\x1b[3;5:3~", "C-FDel:Release"},
		// The "u" form already did this, and must go on doing it.
		{"\x1b[97;1:3u", "a:Release"},
		// A press is unmarked, however it is spelled.
		{"\x1b[1;1A", "Up"},
		{"\x1b[15;1~", "F5"},
	} {
		got := feedKeys(t, tc.raw)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
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
