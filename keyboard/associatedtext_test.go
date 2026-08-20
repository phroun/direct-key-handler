package keyboard

import (
	"io"
	"testing"
	"time"
)

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
	if len(got) != 1 || got[0] != TextPrefix+"今日" {
		t.Errorf("a pure text event -> %v, want one %q and nothing else",
			got, TextPrefix+"今日")
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

// feedDecoded runs the input with macOS Option decoding forced ON, which is
// what an "= true" in a host's config asks for. The default is darwin-only, so
// a test that does not force it proves nothing anywhere else.
func feedDecoded(t *testing.T, raw string) []string {
	t.Helper()
	pr, pw := io.Pipe()
	manage, decode := false, true
	h := New(Options{InputReader: pr, ManageTerminal: &manage, DecodeMacOSOption: &decode})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	go func() { pw.Write([]byte(raw)); pw.Close() }()
	var keys []string
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case k := <-h.Keys:
			keys = append(keys, k)
		case <-deadline:
			return keys
		}
	}
}

// Associated text must not eat the Option chord.
//
// Option+a is "M-a" — a bindable chord — and it is the RECIPIENT's business
// whether an unbound one types "å". A terminal can report that keystroke three
// ways, and reading the text as the key's name must not turn any of them into
// the character.
func TestAssociatedTextLeavesTheOptionChordAlone(t *testing.T) {
	for _, c := range []struct{ what, raw string }{
		{"the key and the Mega, with the text alongside", "\x1b[97;3;229u"},
		{"the composed character as the keycode", "\x1b[229;1;229u"},
		{"the key alone, with the text alongside", "\x1b[97;1;229u"},
		{"and with no associated text at all", "\x1b[229u"},
	} {
		got := feedDecoded(t, c.raw)
		if len(got) == 0 || got[len(got)-1] != "M-a" {
			t.Errorf("%s: %q -> %v, want M-a", c.what, c.raw, got)
		}
	}
}

// A dead key's completion is still the character it composed, not a chord:
// "û" is not in the Option table, so nothing claims it.
func TestADeadKeyCompletionIsNotDecodedAsAChord(t *testing.T) {
	if got := feedDecoded(t, "\x1b[117;1;251u"); len(got) == 0 || got[len(got)-1] != "û" {
		t.Errorf("the dead key's completion -> %v, want û", got)
	}
}

// The associated text rides on the LEGACY-form keys too, and a named key is
// still named by its key.
//
// Asking for flag 16 makes a terminal append the text field to every family,
// not only the "u" one — including the cursor keys, Home/End, F1-F4 and the
// tilde set, whose parsers accepted exactly two parameters and declined a
// third. Declining sends the caller back to read the sequence byte by byte, so
// a Down arrow pressed with an input method's text in flight arrived as
// Escape, "[", "1", ";", ... — the Escape opening a command prompt and the
// rest typed into it.
//
// The text does not rename these: whatever an input method happened to hold
// while Down was pressed, the key is Down.
func TestLegacyFormKeysTolerateTheAssociatedText(t *testing.T) {
	for _, c := range []struct{ what, raw, want string }{
		{"a cursor key with text", "\x1b[1;1;250B", "Down"},
		{"a cursor key repeating with text", "\x1b[1;1:2;250B", "Down:Repeat"},
		{"a cursor key keeps its modifier", "\x1b[1;2;250B", "S-Down"},
		{"Home/End with text", "\x1b[1;1;250H", "Home"},
		{"F1-F4 with text", "\x1b[1;1;250P", "F1"},
		{"the tilde family with text", "\x1b[2;1;250~", "Insert"},
		{"and the tilde family keeps its modifier", "\x1b[2;2;250~", "S-Insert"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q -> %v, want [%s]", c.what, c.raw, got, c.want)
		}
	}
}

// And nothing moves for a terminal that sends no text field.
func TestLegacyFormKeysAreUnchangedWithoutText(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"\x1b[B", "Down"},
		{"\x1b[1;2B", "S-Down"},
		{"\x1b[1;1:2B", "Down:Repeat"},
		{"\x1b[1;2H", "S-Home"},
		{"\x1b[2~", "Insert"},
		{"\x1b[1;2P", "S-F1"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q -> %v, want [%s]", c.raw, got, c.want)
		}
	}
}
