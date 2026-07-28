package keyboard

import (
	"strings"
	"testing"
)

// A terminal delivers a keystroke and whatever the terminal itself emitted
// next in a single read. When the keystroke is Escape, the two land as
// "\x1b" + "\x1b[...". ESC restarts the sequence, so the Escape resolves on
// its own and the sequence behind it parses normally — rather than the pair
// accumulating into one unparseable buffer whose body gets typed out as text.
func TestEscRestartsTheSequence(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want []string
	}{
		// Each half on its own, for contrast.
		{"\x1b", []string{"Escape"}},
		{"\x1b[<35;12;5M", []string{"MouseDrag@12,5"}},
		{"\x1b[A", []string{"Up"}},

		// Together. Before the fix these emitted the Escape and then the
		// second sequence's body one character at a time ("[", "<", "3", ...).
		{"\x1b\x1b[<35;12;5M", []string{"Escape", "MouseDrag@12,5"}},
		{"\x1b\x1b[A", []string{"Escape", "Up"}},

		// A double-tap of Escape, which some editors bind.
		{"\x1b\x1b", []string{"Escape", "Escape"}},
	} {
		got := feedKeys(t, c.raw)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%q -> %v, want %v", c.raw, got, c.want)
		}
	}
}
