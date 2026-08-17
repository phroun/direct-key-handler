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

// A release with no press is dropped, and dropped means CONSUMED.
//
// Safe by construction rather than by luck: the table holds exactly what this
// package emitted, so no entry means no press was ever reported for that key
// and nothing downstream can be holding it.
//
// Consumed is the other half. Answering "not a key" sends the caller back to
// re-read the sequence byte by byte, which turns a dropped release into a
// phantom Escape followed by its digits typed as text — worse than the mismatch
// it was meant to prevent, and in a modal editor it leaves the mode.
func TestAnUnmatchedReleaseIsDroppedNotReReadAsBytes(t *testing.T) {
	if got := feedKeys(t, "\x1b[97;1:3u"); len(got) != 0 {
		t.Errorf("a release with no press produced %v, want nothing at all", got)
	}
	// A double release: the first consumes the entry, the second has none.
	got := feedKeys(t, "\x1b[97;5u\x1b[97;1:3u\x1b[97;1:3u")
	want := []string{"^A", "^A:Release"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("press and two releases -> %v, want %v", got, want)
	}
	// An orphan release must not disturb the pair that follows it.
	got = feedKeys(t, "\x1b[98;1:3u\x1b[97;5u\x1b[97;1:3u")
	if len(got) != 2 || got[0] != "^A" || got[1] != "^A:Release" {
		t.Errorf("orphan release then a real pair -> %v, want [^A ^A:Release]", got)
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
