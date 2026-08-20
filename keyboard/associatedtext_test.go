package keyboard

import "testing"

// A key reports what it TYPED, not only what it is called.
//
// With the keys reported as escape codes (flag 8), text stops arriving as text
// and the key's own name is all that is left. That is the wrong character
// whenever the two differ: Option+i then "u" composes "û" and reports the U
// KEY, so a "u" reached the document where the accented letter belonged.
func TestAssociatedTextNamesTheKey(t *testing.T) {
	for _, c := range []struct{ what, raw, want string }{
		{"a dead key's completion", "\x1b[117;1;251u", "û"},
		{"a plain letter, text and name agreeing", "\x1b[97;1;97u", "a"},
		{"several codepoints from one key", "\x1b[97;1;97:769u", "a\u0301"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) == 0 || got[len(got)-1] != c.want {
			t.Errorf("%s: %q -> %v, want %q", c.what, c.raw, got, c.want)
		}
	}
}

// A chord is named for its KEY, so a keymap can bind it. The protocol sends no
// text for one, but a terminal that does anyway must not rename the chord.
func TestAChordIsStillNamedForItsKey(t *testing.T) {
	// Ctrl+a: keycode 97, modifier field 5 (bit 4 + 1).
	if got := feedKeys(t, "\x1b[97;5;97u"); len(got) == 0 || got[len(got)-1] != "^A" {
		t.Errorf("Ctrl+a with text -> %v, want the chord kept", got)
	}
}

// Keycode 0 is the protocol's PURE TEXT EVENT: text with no key information
// behind it, which is what an input method delivers when it commits. Naming a
// key for it would put a phantom keystroke in the document beside the text.
func TestAPureTextEventIsTextAndNotAKey(t *testing.T) {
	// "今日" = U+4ECA U+65E5.
	got := feedKeys(t, "\x1b[0;;20170:26085u")
	if len(got) != 1 || got[0] != "今日" {
		t.Errorf("a pure text event -> %v, want one %q and nothing else", got, "今日")
	}
}

// An empty pure text event is consumed rather than declined: answering "not a
// key" sends the caller back to read the sequence byte by byte, which types the
// escape and its digits into the document.
func TestAnEmptyPureTextEventTypesNothing(t *testing.T) {
	if got := feedKeys(t, "\x1b[0u"); len(got) != 0 {
		t.Errorf("an empty pure text event -> %v, want nothing", got)
	}
}

// Nothing changes for a host that never asked for the text: every key is still
// named from its keycode alone.
func TestNoAssociatedTextIsTheOldBehaviour(t *testing.T) {
	if got := feedKeys(t, "\x1b[117u"); len(got) != 1 || got[0] != "u" {
		t.Errorf("a bare keycode -> %v, want [u]", got)
	}
}
