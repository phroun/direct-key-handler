package keyboard

import (
	"bytes"
	"testing"
	"time"
)

// decodeModifierKey feeds one kitty modifier-key report and returns what came
// out.
func decodeModifierKey(t *testing.T, raw string) []string {
	t.Helper()
	h := New(Options{InputReader: bytes.NewReader([]byte(raw))})
	if err := h.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer h.Stop()

	var got []string
	for {
		select {
		case k := <-h.Keys:
			got = append(got, k)
		case <-time.After(120 * time.Millisecond):
			return got
		}
	}
}

// A modifier key's own press and release name that key with the SAME letter
// its prefix uses.
//
// Two were wrong. Mega emitted "A", a prefix this vocabulary does not have at
// all — key-sequence-processor has no rank for "A-" and a test asserting it
// never gains one, so a Mega press could not match a binding no matter how it
// was written. Micro emitted "M", taking the letter that belongs to Mega, so
// the two keys collided in the one place they are reported separately.
//
// The letters have to agree with the prefixes because a consumer learns the
// vocabulary once: something that knows "M-x" is Mega must not meet a second
// spelling for the same key here.
func TestModifierKeysUseTheirOwnPrefixLetter(t *testing.T) {
	for _, c := range []struct{ raw, want, what string }{
		{"\x1b[57441;1:1u", "S-Press:Left", "Shift"},
		{"\x1b[57442;1:1u", "C-Press:Left", "Control"},
		{"\x1b[57443;1:1u", "M-Press:Left", "Mega — was \"A-\", a prefix that does not exist"},
		{"\x1b[57444;1:1u", "s-Press:Left", "Super"},
		{"\x1b[57445;1:1u", "H-Press:Left", "Hyper"},
		{"\x1b[57446;1:1u", "m-Press:Left", "Micro — was \"M-\", which belongs to Mega"},

		{"\x1b[57449;1:1u", "M-Press:Right", "Mega, right side"},
		{"\x1b[57452;1:1u", "m-Press:Right", "Micro, right side"},

		{"\x1b[57443;1:3u", "M-Release:Left", "Mega release"},
		{"\x1b[57443;1:2u", "M-Repeat:Left", "Mega repeat"},
	} {
		got := decodeModifierKey(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q decoded as %v, want [%s]", c.what, c.raw, got, c.want)
		}
	}
}

// Mega and Micro never share a spelling, on any side or event.
//
// They are two different keys and the kitty protocol is the only place that
// says which was pressed; collapsing them here would throw away the single
// piece of information these reports exist to carry.
func TestMegaAndMicroStayApart(t *testing.T) {
	for _, side := range []struct{ mega, micro, what string }{
		{"\x1b[57443;1:1u", "\x1b[57446;1:1u", "left press"},
		{"\x1b[57449;1:1u", "\x1b[57452;1:1u", "right press"},
		{"\x1b[57443;1:3u", "\x1b[57446;1:3u", "left release"},
	} {
		mega := decodeModifierKey(t, side.mega)
		micro := decodeModifierKey(t, side.micro)
		if len(mega) != 1 || len(micro) != 1 {
			t.Errorf("%s: got %v and %v, want one key each", side.what, mega, micro)
			continue
		}
		if mega[0] == micro[0] {
			t.Errorf("%s: Mega and Micro both name themselves %q", side.what, mega[0])
		}
	}
}

// No key this package emits carries an "A-" prefix.
//
// "A-" is not in namePrefixes and never was; it reached the wire only from the
// modifier-key table, which had been written in the protocol's vocabulary
// ("alt") rather than this one. Nothing downstream can parse it: it is not a
// modifier to key-sequence-processor, so it becomes part of the base name and
// matches nothing.
func TestNothingEmitsAnAPrefix(t *testing.T) {
	for code := 57441; code <= 57452; code++ {
		for _, event := range []string{"1", "2", "3"} {
			raw := "\x1b[" + itoaTest(code) + ";1:" + event + "u"
			for _, k := range decodeModifierKey(t, raw) {
				if len(k) >= 2 && k[0] == 'A' && k[1] == '-' {
					t.Errorf("keycode %d event %s emitted %q, which begins with a "+
						"prefix this vocabulary does not have", code, event, k)
				}
			}
		}
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
