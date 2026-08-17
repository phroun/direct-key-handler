package keyboard

import (
	"fmt"
	"testing"
)

// A control byte under Mega is the name that byte already has, with the prefix
// in front. Nothing else.
//
// The ESC-prefixed path used to restate controlKeys by hand — a switch for five
// bytes plus a second copy of the ^A-^Z half — and the copy had drifted from the
// original on two entries. Asserting the PROPERTY rather than a list of expected
// strings is the point: a table restated once will drift again, and the only
// durable guard is that the two agree by construction.
func TestAControlByteUnderMegaIsItsOwnNamePrefixed(t *testing.T) {
	for b, want := range controlKeys {
		raw := "\x1b" + string([]byte{b})
		// ESC ESC is a sequence in its own right — a bare Escape, resolved
		// before this path — so it is not Mega-anything.
		if b == 0x1B {
			continue
		}
		got := feedKeys(t, raw)
		if len(got) != 1 || got[0] != "M-"+want {
			t.Errorf("ESC + byte %d parsed as %v, want [M-%s] (the bare byte is %q)",
				b, got, want, want)
		}
	}
}

// The two entries the hand-copied switch had wrong, named outright so the
// regression is legible without reasoning about the property above.
//
// M-Delete could not be produced at all: 0x7F said "M-Backspace" while the bare
// byte says "Delete". Alt+Backspace is the delete-previous-word chord and it
// arrives as ESC 0x7F from every terminal whose kbs is ^? — which is most of
// them — so a keymap binding M-Delete never fired, while M-Backspace answered
// for both keyboards. That is the conflation naming the two bytes apart exists
// to prevent, reappearing the moment a modifier was held.
//
// M-Enter named a key that no longer exists unprefixed: "Enter" is the KEYPAD's
// name, and the home-row key is Return.
func TestTheTwoMegaFormsThatHadDrifted(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"\x1b\x7f", "M-Delete", "ESC DEL, which is Alt+Backspace on most terminals"},
		{"\x1b\x08", "M-Backspace", "ESC BS, the other erase byte, still itself"},
		{"\x1b\r", "M-Return", "ESC CR is the home-row key"},
		// And the two that were right by luck rather than construction.
		{"\x1b\t", "M-Tab", ""},
		{"\x1b ", "M-Space", ""},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q (%s) parsed as %v, want [%s]", tc.raw, tc.what, got, tc.want)
		}
	}
}

// The erase bytes stay apart under Mega, which is the whole reason they are two
// names. Bound separately, a keymap has to be able to reach either.
func TestTheEraseBytesStayApartUnderMega(t *testing.T) {
	bs, del := feedKeys(t, "\x1b\x08"), feedKeys(t, "\x1b\x7f")
	if len(bs) != 1 || len(del) != 1 {
		t.Fatalf("ESC BS -> %v, ESC DEL -> %v; want one key each", bs, del)
	}
	if bs[0] == del[0] {
		t.Errorf("ESC BS and ESC DEL both parsed as %q; an application can no "+
			"longer tell which erase byte its terminal sent", bs[0])
	}
}

// The control bytes above ^Z reach a Mega form too.
//
// The hand-written loop covered 0x01 through 0x1A and stopped, and the printable
// test below it required 0x20 or more, so ESC followed by 0x00 or by 0x1C-0x1F
// fell out of the branch entirely and was re-read as two keystrokes: a bare
// Escape, then the control key. A phantom Escape is worse than a dropped key in
// a modal editor, where it leaves the mode. Deriving from controlKeys picks
// these up because they were always in it.
func TestTheControlBytesPastTheLetterRangeAreNotDropped(t *testing.T) {
	for _, tc := range []struct {
		b    byte
		want string
	}{
		{0x00, "M-^@"},
		{0x1C, "M-^\\"},
		{0x1D, "M-^]"},
		{0x1E, "M-^^"},
		{0x1F, "M-^_"},
	} {
		raw := "\x1b" + string([]byte{tc.b})
		if got := feedKeys(t, raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("ESC + %#02x parsed as %v, want [%s]", tc.b, got, tc.want)
		}
	}
}

// Every control byte reaches SOME Mega form. A byte that names a key unmodified
// and nothing at all with Mega held is a keystroke that vanishes, which is how
// the five above went unnoticed.
func TestNoControlByteVanishesUnderMega(t *testing.T) {
	for b := 0; b < 0x20; b++ {
		if b == 0x1B {
			continue // ESC ESC is a sequence of its own
		}
		raw := "\x1b" + string([]byte{byte(b)})
		if got := feedKeys(t, raw); len(got) != 1 {
			t.Errorf("ESC + %#02x produced %v, want exactly one key", b, got)
		}
	}
	if got := feedKeys(t, "\x1b\x7f"); len(got) != 1 {
		t.Errorf("ESC + DEL produced %v, want exactly one key", got)
	}
}

// A printable under Mega is still just itself with the prefix — the branch the
// derived lookup falls through to.
func TestPrintablesUnderMegaAreUnchanged(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"\x1bx", "M-x"},
		{"\x1b5", "M-5"},
		{"\x1b-", "M--"},
		{"\x1b/", "M-/"},
		// An uppercase letter carries Shift in its CASE, as every other shown
		// key does. This said "M-S-x", decomposing a distinction the wire never
		// made: ESC 'X' is two bytes, 0x1B 0x58, with no modifier field
		// anywhere, so the uppercase byte is the whole report. Every shifted
		// symbol on this path already arrived as itself — "%" for Shift+5, "?"
		// for Shift+/ — and letters were the only exception.
		{"\x1bX", "M-X"},
		{"\x1b%", "M-%"}, // the symbol beside it, which was always right
		{"\x1b?", "M-?"},
	} {
		if got := feedKeys(t, tc.raw); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q parsed as %v, want [%s]", tc.raw, got, tc.want)
		}
	}
	// Every printable reaches SOME single key, which is what the derived
	// control lookup sitting in front of this branch must not disturb.
	for c := 0x21; c < 0x7f; c++ {
		raw := "\x1b" + string([]byte{byte(c)})
		got := feedKeys(t, raw)
		if len(got) != 1 {
			t.Errorf("ESC + %q produced %v, want exactly one key", rune(c), got)
			continue
		}
		if want := fmt.Sprintf("M-%c", c); got[0] != want {
			t.Errorf("ESC + %q parsed as %q, want %q", rune(c), got[0], want)
		}
	}
}

// Case is the only thing that separates the two forms of a letter under Mega,
// and it separates them — the pair must not collapse onto one name, or a keymap
// that binds them apart cannot be honoured.
//
// The wire says it in one byte: 0x1B 0x78 and 0x1B 0x58 differ by the letter's
// case and by nothing else, because there is no modifier field on this path to
// say more. Reading that case back out is the whole of the report.
func TestMegaLettersKeepTheirCase(t *testing.T) {
	for c := 'a'; c <= 'z'; c++ {
		lower := feedKeys(t, "\x1b"+string(c))
		upper := feedKeys(t, "\x1b"+string(c-32))
		if len(lower) != 1 || len(upper) != 1 {
			t.Errorf("ESC %q -> %v, ESC %q -> %v; want one key each",
				c, lower, c-32, upper)
			continue
		}
		if lower[0] != "M-"+string(c) {
			t.Errorf("ESC %q parsed as %q, want %q", c, lower[0], "M-"+string(c))
		}
		if upper[0] != "M-"+string(c-32) {
			t.Errorf("ESC %q parsed as %q, want %q", c-32, upper[0], "M-"+string(c-32))
		}
		if lower[0] == upper[0] {
			t.Errorf("ESC %q and ESC %q both parsed as %q", c, c-32, lower[0])
		}
	}
}
