package keyboard

import (
	"testing"
)

// A key comes up under the name it went down with, whatever the modifiers did
// in between.
//
// A release carries the modifier mask as it stands at the moment of release, so
// the name derived from it describes the chord that is held NOW, not the one
// that was struck. Fingers let go of Control a few milliseconds before the
// letter, so "^A" went down and "a" came up — two events nothing downstream
// could pair, leaving anything that tracks held keys holding "^A" forever.
func TestAKeyComesUpUnderTheNameItWentDownWith(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
		what string
	}{
		{"\x1b[97;5u\x1b[97;5:3u", []string{"^A", "^A:Release"},
			"Control still held at release, which always worked"},
		{"\x1b[97;5u\x1b[97;1:3u", []string{"^A", "^A:Release"},
			"Control let go first, which is what fingers do"},
		{"\x1b[53;6u\x1b[53;1:3u", []string{"^%", "^%:Release"},
			"Ctrl+Shift on a shown key, both modifiers gone by the release"},
		{"\x1b[97;2u\x1b[97;1:3u", []string{"A", "A:Release"},
			"Shift alone, released first"},
		{"\x1b[57406;5u\x1b[57406;1:3u", []string{"P-^7", "P-^7:Release"},
			"a keypad chord, where the name is built three ways at once"},
		{"\x1b[97u\x1b[97;1:3u", []string{"a", "a:Release"},
			"and an unmodified key, which needs nothing but must not break"},
	} {
		got := feedKeys(t, tc.raw)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %q -> %v, want %v", tc.what, tc.raw, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: %q -> %v, want %v", tc.what, tc.raw, got, tc.want)
				break
			}
		}
	}
}

// A repeat is the same key still down, so it reports the same name. A held key
// whose modifiers change mid-hold would otherwise start repeating under a name
// it was never pressed under.
func TestARepeatKeepsThePressName(t *testing.T) {
	got := feedKeys(t, "\x1b[97;5u\x1b[97;1:2u\x1b[97;1:2u\x1b[97;1:3u")
	want := []string{"^A", "^A:Repeat", "^A:Repeat", "^A:Release"}
	if len(got) != len(want) {
		t.Fatalf("held key with Control let go mid-hold -> %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("held key with Control let go mid-hold -> %v, want %v", got, want)
			break
		}
	}
}

// An unmatched release is never re-read as bytes, whichever way it is handled.
//
// Answering "not a key" sends the caller back to read the sequence byte by
// byte, which turns the release into a phantom Escape followed by its digits
// typed as text — worse than the mismatch it was meant to prevent, and in a
// modal editor it leaves the mode. Whether the release is dropped or falls back
// to its derived name, it must be CONSUMED either way.
func TestAnUnmatchedReleaseIsNeverReReadAsBytes(t *testing.T) {
	for _, raw := range []string{
		"\x1b[97;1:3u", // the "u" family: falls back to a derived name
		"\x1b[1;1:3A",  // the cursor family: dropped
		"\x1b[5;1:3~",  // the tilde family: dropped
		"\x1b[1;1:3P",  // F1-F4: dropped
	} {
		for _, got := range feedKeys(t, raw) {
			if len(got) > 1 && (got == "Escape" || got == "[") {
				t.Errorf("%q was re-read as bytes: %v", raw, feedKeys(t, raw))
			}
			if got == "Escape" {
				t.Errorf("%q produced a phantom Escape", raw)
			}
		}
	}

	// An orphan does not disturb the pair that follows it, whichever way it
	// was handled.
	got := feedKeys(t, "\x1b[98;1:3u\x1b[97;5u\x1b[97;1:3u")
	if len(got) < 2 || got[len(got)-2] != "^A" || got[len(got)-1] != "^A:Release" {
		t.Errorf("orphan release then a real pair -> %v, want it to end [^A ^A:Release]", got)
	}
}

// A press always starts fresh. It overwrites whatever was recorded for that
// key, so a press can never inherit an older chord — only a release consumes an
// entry, and only the entry its own press wrote.
func TestAPressNeverInheritsAnOlderEntry(t *testing.T) {
	got := feedKeys(t, "\x1b[97;5u\x1b[97;1:3u\x1b[97u\x1b[97;1:3u")
	want := []string{"^A", "^A:Release", "a", "a:Release"}
	if len(got) != len(want) {
		t.Fatalf("Ctrl+a then plain a -> %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Ctrl+a then plain a -> %v, want %v", got, want)
			break
		}
	}

	// Even with the first release never delivered, the second press stands on
	// its own rather than on the stale entry.
	got = feedKeys(t, "\x1b[97;5u\x1b[97u\x1b[97;1:3u")
	if len(got) != 3 || got[2] != "a:Release" {
		t.Errorf("press, re-press, release -> %v, want the release to match the "+
			"SECOND press", got)
	}
}

// Keys are held independently. Two down at once release in whatever order the
// user lets go, and each takes its own name with it.
func TestHeldKeysDoNotInterfere(t *testing.T) {
	got := feedKeys(t, "\x1b[97;5u\x1b[98;5u\x1b[98;1:3u\x1b[97;1:3u")
	want := []string{"^A", "^B", "^B:Release", "^A:Release"}
	if len(got) != len(want) {
		t.Fatalf("two keys interleaved -> %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("two keys interleaved -> %v, want %v", got, want)
			break
		}
	}
}

// The modifier keys report themselves in their own shape and are left alone.
// Their "release" is not the end of a chord — it IS the event — and they carry
// no chord name to remember.
func TestModifierKeysAreNotHeld(t *testing.T) {
	got := feedKeys(t, "\x1b[57441;1u\x1b[57441;1:3u")
	if len(got) != 2 {
		t.Fatalf("a modifier key down and up -> %v, want two events", got)
	}
	for _, g := range got {
		if g == "" {
			t.Errorf("a modifier event was swallowed: %v", got)
		}
	}
	// Specifically, the release must survive — dropping it as "unmatched"
	// would silence every modifier release in the system.
	if got[1] == got[0] {
		t.Errorf("a modifier's press and release are identical: %v", got)
	}
}

// A key whose PRESS is spelled literally is still remembered.
//
// The literal-sequence table is consulted before any parsing and returns
// straight away, so a press matched there never reached the reconciliation.
// Releases cannot be matched there — they carry the event sub-parameter, so
// they are never one of those strings — which means every literally-spelled
// key would have gone down unremembered and come up an orphan, and the orphan
// would have been dropped.
//
// The unmodified arrow keys are exactly that shape: DOWN as "\x1b[A", UP as
// "\x1b[1;1:3A". Losing them would have reinstated the missing key-up bug this
// whole line of work started from.
func TestAPressSpelledLiterallyIsStillHeld(t *testing.T) {
	for _, tc := range []struct{ raw, want, what string }{
		{"\x1b[A\x1b[1;1:3A", "Up:Release", "an unmodified arrow"},
		{"\x1b[B\x1b[1;1:3B", "Down:Release", ""},
		{"\x1b[5~\x1b[5;1:3~", "PageUp:Release", "and the tilde family"},
		{"\x1b[1;5D\x1b[1;5:3D", "C-Left:Release", "and a modified one, also literal"},
		// SS3 is the same key in application cursor mode, so a press sent that
		// way must answer for a release that arrives as CSI — a terminal picks
		// the mode for the press and has only one spelling for the release.
		{"\x1bOA\x1b[1;1:3A", "Up:Release", "pressed in application cursor mode"},
		{"\x1bOP\x1b[1;1:3P", "F1:Release", ""},
	} {
		got := feedKeys(t, tc.raw)
		if len(got) != 2 || got[1] != tc.want {
			t.Errorf("%q (%s) -> %v, want the release to be %q", tc.raw, tc.what, got, tc.want)
		}
	}
}

// A text key goes DOWN as a byte and comes UP as a sequence, and both halves
// have to find each other.
//
// With event reporting on but disambiguation off — what a host asks for when it
// wants presses to stay byte-identical — a letter's press is the byte "l" while
// its release is "CSI 108;1:3u". Nothing else in this package sees both. So
// every letter, digit and symbol went down unrecorded, and once an unmatched
// release was being dropped, every one of those releases vanished: a hosted
// browser was back to never seeing a keyup for ordinary typing, which is the
// bug the whole release chain was built to fix.
func TestATextKeyPressedAsAByteIsReleasedByItsSequence(t *testing.T) {
	for _, tc := range []struct {
		raw, want, what string
	}{
		{"l\x1b[108;1:3u", "l:Release", "a letter"},
		{"o\x1b[111;1:3u", "o:Release", ""},
		{".\x1b[46;1:3u", ".:Release", "and punctuation"},
		{"5\x1b[53;1:3u", "5:Release", "and a digit"},
		// The keycode is the BASE key, so a capital's release reports 108 —
		// the "l" key — with Shift in the modifier field. Recording under the
		// byte 'L' alone would never have matched it.
		{"L\x1b[108;2:3u", "L:Release", "a capital, whose keycode is the lowercase"},
		// Ctrl-L is byte 0x0C and the "l" key held with Control, so it records
		// under the same keycode again.
		{"\x0c\x1b[108;5:3u", "^L:Release", "a control byte"},
		// And the mismatch fix still holds across the split: Control let go
		// before the letter, so the release carries no modifier at all, and it
		// still comes up named for the press.
		{"\x0c\x1b[108;1:3u", "^L:Release", "with Control released first"},
	} {
		got := feedKeys(t, tc.raw)
		if len(got) != 2 || got[1] != tc.want {
			t.Errorf("%q (%s) -> %v, want the release to be %q",
				tc.raw, tc.what, got, tc.want)
		}
	}
}

// Dropping an unmatched release is right only where a press is ALWAYS a
// sequence.
//
// That is true of the cursor, tilde and F1-F4 families: every one of their
// presses is recorded, so no entry really does mean no press was emitted. It is
// NOT true of the "u" family, where a press may have been a byte and a shifted
// punctuation key cannot be mapped back to its keycode without knowing the
// layout — "%" is Shift+5 only on some keyboards. Dropping there would lose a
// real release, so the derived name stands instead.
func TestTheDropAppliesOnlyWhereAPressIsAlwaysASequence(t *testing.T) {
	// The "u" family falls back rather than dropping.
	if got := feedKeys(t, "\x1b[122;1:3u"); len(got) != 1 || got[0] != "z:Release" {
		t.Errorf("an unmatched u-form release -> %v, want [z:Release]; dropping it "+
			"would lose the release of any key whose press was a byte", got)
	}
	// The families whose presses are always sequences still drop.
	for _, raw := range []string{"\x1b[1;1:3A", "\x1b[5;1:3~", "\x1b[1;1:3P"} {
		if got := feedKeys(t, raw); len(got) != 0 {
			t.Errorf("%q -> %v, want nothing: no press was ever emitted for it", raw, got)
		}
	}
}

// Losing focus releases every key still down.
//
// This is the one case dropping an unmatched release cannot cover: the key-up
// for a key held across a focus change is delivered to whoever has the keyboard
// now, so waiting for it means waiting forever and the press stands for good.
// A browser releases its keys on blur for the same reason.
func TestLosingFocusReleasesTheKeysStillDown(t *testing.T) {
	got := feedKeys(t, "\x1b[97;5u\x1b[O")
	if len(got) != 2 || got[0] != "^A" || got[1] != "^A:Release" {
		t.Errorf("a held key then focus out -> %v, want [^A ^A:Release]", got)
	}

	// Everything down, not just the last one, and in a settled order.
	got = feedKeys(t, "\x1b[97;5u\x1b[1;1A\x1b[O")
	want := []string{"^A", "Up", "Up:Release", "^A:Release"}
	if len(got) != len(want) {
		t.Fatalf("two keys held then focus out -> %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("two keys held then focus out -> %v, want %v", got, want)
			break
		}
	}

	// A key-up arriving after the flush is an orphan. On the families whose
	// presses are always sequences it is dropped, so the key is released once.
	got = feedKeys(t, "\x1b[1;1A\x1b[O\x1b[1;1:3A")
	if len(got) != 2 {
		t.Errorf("flush then a late cursor release -> %v, want the key released once", got)
	}
	// On the "u" family it cannot be: a press there may have been a byte, so an
	// absent entry is not evidence of an absent press, and the derived name has
	// to stand. The cost is visible here — a key held across a blur is released
	// by the flush and again if its key-up ever arrives. In practice the
	// terminal has stopped sending us input by then, so the second one does not
	// come; the alternative is losing every release whose press was a byte,
	// which is the whole of ordinary typing.
	got = feedKeys(t, "\x1b[97;5u\x1b[O\x1b[97;1:3u")
	if len(got) != 3 || got[1] != "^A:Release" || got[2] != "a:Release" {
		t.Errorf("flush then a late u-form release -> %v, want [^A ^A:Release a:Release]", got)
	}

	// GAINING focus releases nothing: the keys are still down, and a key held
	// across a click back into the window must not be reported up.
	got = feedKeys(t, "\x1b[97;5u\x1b[I\x1b[97;1:3u")
	if len(got) != 2 || got[1] != "^A:Release" {
		t.Errorf("focus in mid-hold -> %v, want the real release to still work", got)
	}
}

// The focus reports are not keys, and are not typed as text either.
//
// Nothing handled CSI I and CSI O, so they fell through every parse and were
// re-read byte by byte: a phantom Escape, then "[" and "I" as characters. Any
// application that turned focus reporting on got that on every alt-tab.
func TestFocusReportsAreNotKeys(t *testing.T) {
	for _, raw := range []string{"\x1b[I", "\x1b[O"} {
		if got := feedKeys(t, raw); len(got) != 0 {
			t.Errorf("%q produced %v, want no keys at all", raw, got)
		}
	}
}

// The callback fires for both directions, and the releases are reported BEFORE
// it — a consumer reacting to the focus change should not still be holding keys
// when it does.
func TestOnFocusFiresAfterTheKeysAreReleased(t *testing.T) {
	f := false
	h := New(Options{ManageTerminal: &f})

	var order []string
	h.OnKey = func(k string) { order = append(order, "key:"+k) }
	h.OnFocus = func(focused bool) {
		order = append(order, map[bool]string{true: "focus:in", false: "focus:out"}[focused])
	}

	h.heldKeys["u:97"] = "^A"
	h.handleFocusReport(false)
	h.handleFocusReport(true)

	want := []string{"key:^A:Release", "focus:out", "focus:in"}
	if len(order) != len(want) {
		t.Fatalf("order was %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order was %v, want %v", order, want)
			break
		}
	}
}

// ReleaseHeldKeys is the way out of the one case dropping cannot cover: a key
// let go while the keyboard is somewhere else, whose release this package will
// never be sent. Without it the press stands forever downstream.
func TestReleaseHeldKeysReportsEverythingStillDown(t *testing.T) {
	f := false
	h := New(Options{ManageTerminal: &f})
	h.heldKeys["u:97"] = "^A"
	h.heldKeys["u:98"] = "S-B"

	var emitted []string
	h.OnKey = func(k string) { emitted = append(emitted, k) }

	got := h.ReleaseHeldKeys()
	if len(got) != 2 {
		t.Fatalf("ReleaseHeldKeys returned %v, want two releases", got)
	}
	// Sorted, because a map has no order and a release should not be a lottery.
	if got[0] != "S-B:Release" || got[1] != "^A:Release" {
		t.Errorf("ReleaseHeldKeys returned %v, want [S-B:Release ^A:Release]", got)
	}
	if len(emitted) != 2 {
		t.Errorf("the releases were returned but not EMITTED: %v", emitted)
	}

	// And the table is empty afterwards, so a second call reports nothing and
	// a later release of the same key is an orphan rather than a replay.
	if again := h.ReleaseHeldKeys(); len(again) != 0 {
		t.Errorf("a second ReleaseHeldKeys returned %v, want nothing", again)
	}
}
