package keyboard

import "testing"

// The kitty protocol reports eight modifier bits. Four of them were decoded and
// two were dropped on the floor, so a Hyper or a true-Meta chord arrived
// indistinguishable from the unmodified key — which made "H-" look vestigial to
// every consumer downstream when it was really unimplemented here.
//
// mod is the wire value: the bitmask plus one.
func TestModifierPrefixDecodesEveryBit(t *testing.T) {
	for _, c := range []struct {
		name string
		bits int
		want string
	}{
		{"none", 0, ""},
		{"shift", 1, "S-"},
		{"mega", 2, "M-"},
		{"ctrl", 4, "C-"},
		{"super", 8, "s-"},
		{"hyper", 16, "H-"},
		{"meta", 32, "m-"},
	} {
		if got := modifierPrefix(c.bits + 1); got != c.want {
			t.Errorf("%s (bit %d): modifierPrefix = %q, want %q", c.name, c.bits, got, c.want)
		}
	}
}

// Modifiers are emitted in one fixed order — C- G- M- m- S- s- H- ^ — whatever
// order the bits happen to be tested in. A consumer that sorts before matching
// does not care, but one that compares strings should not have to know which
// producer it is listening to.
func TestModifierPrefixCanonicalOrder(t *testing.T) {
	for _, c := range []struct {
		bits int
		want string
	}{
		{1 | 2, "M-S-"},       // shift+mega, NOT "S-M-"
		{1 | 4, "C-S-"},       // shift+ctrl
		{2 | 4, "C-M-"},       // mega+ctrl
		{1 | 8, "S-s-"},       // shift+super
		{1 | 2 | 4, "C-M-S-"}, // ctrl+mega+shift
		{1 | 2 | 4 | 8, "C-M-S-s-"},
		{16 | 32, "m-H-"}, // meta+hyper
		{1 | 16, "S-H-"},  // shift+hyper
		{2 | 32, "M-m-"},  // both metas at once
	} {
		if got := modifierPrefix(c.bits + 1); got != c.want {
			t.Errorf("bits %d: modifierPrefix = %q, want %q", c.bits, got, c.want)
		}
	}
}

// How Control is spelled depends on the key it pairs with, never on what else
// is held. A letter is caret-natural, so Control always takes the caret there
// and hugs the letter — adding Shift or Meta does not push it back out to the
// "C-" form.
func TestControlSpellingFollowsTheBaseKey(t *testing.T) {
	const a = 'a'
	for _, c := range []struct {
		name string
		bits int
		want string
	}{
		{"ctrl", 4, "^A"},
		{"ctrl+shift", 4 | 1, "S-^A"},
		{"ctrl+mega", 4 | 2, "M-^A"},
		{"ctrl+mega+shift", 4 | 2 | 1, "M-S-^A"},
		{"ctrl+super", 4 | 8, "s-^A"},
		{"ctrl+mega+super", 4 | 2 | 8, "M-s-^A"},
		{"ctrl+hyper", 4 | 16, "H-^A"},
		{"ctrl+meta", 4 | 32, "m-^A"},
		{"everything", 4 | 2 | 1 | 8 | 16 | 32, "M-m-S-s-H-^A"},

		// No Control: Shift is carried by the letter's own case.
		{"plain", 0, "a"},
		{"shift", 1, "A"},
		{"mega", 2, "M-a"},
		{"mega+shift", 2 | 1, "M-A"},
	} {
		if got := formatLetterKey(a, c.bits+1); got != c.want {
			t.Errorf("%s: formatLetterKey = %q, want %q", c.name, got, c.want)
		}
	}
}

// Caps Lock (64) and Num Lock (128) are reported by the protocol but are not
// chord modifiers: folding them into a key name would make every keystroke
// typed with Caps Lock on miss its binding.
func TestLockBitsAreNotModifiers(t *testing.T) {
	for _, bits := range []int{64, 128, 64 | 128} {
		if got := modifierPrefix(bits + 1); got != "" {
			t.Errorf("lock bits %d produced prefix %q, want none", bits, got)
		}
	}
	if got := modifierPrefix(64 | 1 + 1); got != "S-" {
		t.Errorf("shift with caps lock held = %q, want S-", got)
	}
}
