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
//
// What it BECOMES depends on the modifier. With none held nobody typed the
// character that arrived — no cap on the keyboard says û — so it is text with
// no key behind it and goes out prefixed. With Shift held the text IS that key
// wearing that modifier, and "A" stays a name a keymap can bind.
func TestAssociatedTextNamesTheKey(t *testing.T) {
	for _, c := range []struct{ what, raw, want string }{
		{"a dead key's completion", "\x1b[117;1;251u", TextPrefix + "û"},
		{"a plain letter, text and name agreeing", "\x1b[97;1;97u", "a"},
		{"a capital is the Shift key, not text", "\x1b[97;2;65u", "A"},
		{"several codepoints from one key", "\x1b[97;1;97:769u", TextPrefix + "a\u0301"},
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
// "û" is not in the Option table, so nothing claims it and it stays text.
func TestADeadKeyCompletionIsNotDecodedAsAChord(t *testing.T) {
	want := TextPrefix + "û"
	if got := feedDecoded(t, "\x1b[117;1;251u"); len(got) == 0 || got[len(got)-1] != want {
		t.Errorf("the dead key's completion -> %v, want %q", got, want)
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

// The three terminals captured for this deliver a press-and-hold palette's
// result three different ways, and all three have to arrive as text.
//
// iTerm2 reports it on a key event whose keycode is unrelated to the character
// — 44 is the comma that dismissed the palette, 97 the "a" key nobody touched —
// while kitty packs the whole thing into one event's text field. What they
// agree on is that no modifier is held, which is what separates a commit from a
// capital.
func TestAPaletteCommitIsTextOnEveryTerminalCaptured(t *testing.T) {
	for _, c := range []struct{ what, raw, want string }{
		{"iTerm2 and ghostty, dismissed by typing", "\x1b[44;;246u", "ö"},
		{"iTerm2, chosen by clicking", "\x1b[97;;246u", "ö"},
		{"kitty, the commit and the key that dismissed it", "\x1b[44;1:2;246:44u", "ö,"},
		{"every terminal, chosen with Return", "\x1b[13;1:2;246u", "ö"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) == 0 || got[len(got)-1] != TextPrefix+c.want {
			t.Errorf("%s: %q -> %v, want %q", c.what, c.raw, got, TextPrefix+c.want)
		}
	}
}

// The character the palette was open OVER is an ordinary key, and so is the
// digit that picked out of it. Both report text that IS their keycode, which is
// a key saying what it typed rather than an input method speaking.
func TestTheKeysAroundACommitAreStillKeys(t *testing.T) {
	for _, c := range []struct{ what, raw, want string }{
		{"the letter the palette opened over", "\x1b[111;;111u", "o"},
		{"its repeats", "\x1b[111;1:2;111u", "o:Repeat"},
		{"the selector digit, iTerm2 and ghostty", "\x1b[52;;52u", "4"},
		{"the selector digit, kitty, which reports no text", "\x1b[52u", "4"},
	} {
		got := feedKeys(t, c.raw)
		if len(got) == 0 || got[len(got)-1] != c.want {
			t.Errorf("%s: %q -> %v, want %q", c.what, c.raw, got, c.want)
		}
	}
}

// kitty and ghostty write the commit as BARE TEXT, outside the protocol
// entirely, with no key event of any kind around it.
//
// It can only be read as text once the terminal has shown it reports keys as
// escape sequences, which it does by sending one carrying associated text. A
// plain byte before that is what it has always been — somebody typing.
func TestBareTextIsAKeyUntilTheTerminalSaysOtherwise(t *testing.T) {
	if got := feedKeys(t, "ö"); len(got) != 1 || got[0] != "ö" {
		t.Errorf("bare text from a terminal that has said nothing -> %v, "+
			"want it named as the key it has always been", got)
	}

	// Now with the terminal having demonstrated the flag first.
	got := feedKeys(t, "\x1b[97;;97uö")
	if len(got) != 2 || got[0] != "a" || got[1] != TextPrefix+"ö" {
		t.Errorf("bare text after an associated-text event -> %v, want [a %sö]",
			got, TextPrefix)
	}
}

// A commit is not a key, so it is not HELD and its key's release is not the
// commit happening again.
//
// iTerm2 reports the text field on the way up as well as the way down. Recorded
// as a held key, the commit went down as "Text:ö" and came back up as
// "Text:ö:Release" — which a host reads as a composition committing the
// characters ö : R e l e a s e, and types them into the document.
func TestACommitIsNotHeldAndDoesNotRepeatOnRelease(t *testing.T) {
	got := feedKeys(t, "\x1b[97;;246u\x1b[97;1:3u")
	if len(got) != 2 {
		t.Fatalf("a commit and its key coming up -> %v, want two events", got)
	}
	if got[0] != TextPrefix+"ö" {
		t.Errorf("the commit -> %q, want %q", got[0], TextPrefix+"ö")
	}
	if got[1] != "a:Release" {
		t.Errorf("the key coming up -> %q, want the KEY, named from its keycode",
			got[1])
	}
}

// And a release that carries the text anyway commits nothing. A key coming up
// types no characters, whatever the terminal chooses to say alongside it.
func TestAReleaseCarryingTextIsStillJustAKeyComingUp(t *testing.T) {
	got := feedKeys(t, "\x1b[97;;246u\x1b[97;1:3;246u")
	if len(got) != 2 || got[1] != "a:Release" {
		t.Errorf("a release carrying the text -> %v, want the second to be "+
			"a:Release and the composition delivered once", got)
	}
}
