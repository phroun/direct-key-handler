package keyboard

import "testing"

// The Glyph modifier is a private kitty-protocol extension: modifier bit value
// 256 (encoded as 257 in the 1-indexed modifier field) marks a key whose
// codepoint IS an AltGr / ISO_Level3_Shift-composed glyph. It must decode to a
// distinct "G-" chord so an application keymap can intercept it, while the
// codepoint carries the produced character.
func TestGlyphModifierDecode(t *testing.T) {
	cases := []struct{ raw, want string }{
		// € = U+20AC = 8364, Glyph alone (mod field 257 = bit 256 + 1).
		{"\x1b[8364;257u", "G-€"},
		// A Glyph-composed ASCII character still becomes a G- chord, not a
		// re-derived letter key.
		{"\x1b[64;257u", "G-@"},
		// Glyph co-held with Shift (bit 1): 257 + 1 = 258.
		{"\x1b[233;258u", "G-S-é"},
		// Release event: CSI 8364 ; 257 : 3 u, fed after its press — a release
		// arriving alone is dropped now, since it names a key never pressed.
		{"\x1b[8364;257u\x1b[8364;257:3u", "G-€:Release"},
	}
	for _, c := range cases {
		got := feedKeys(t, c.raw)
		if len(got) == 0 || got[len(got)-1] != c.want {
			t.Errorf("%q -> %v, want the last key to be %q", c.raw, got, c.want)
		}
	}
}

// Without the Glyph bit, the same codepoint decodes as an ordinary key, so the
// extension does not disturb standard kitty decoding.
func TestNonGlyphUnaffected(t *testing.T) {
	// € with no modifiers (mod field 1) is a plain unicode key.
	got := feedKeys(t, "\x1b[8364u")
	if len(got) != 1 || got[0] != "€" {
		t.Errorf(`plain €: %v, want [€]`, got)
	}
}
