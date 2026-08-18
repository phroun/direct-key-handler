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

// A modifier key pressed on its own is named for the side it sits on and the
// letter its prefix uses: LMod:S is the left Shift.
//
// The side leads because it is what the event is FOR — a chord already carries
// which modifiers were held, so a modifier reporting itself is only worth
// hearing when you want to know which of the two caps it was. The letter has to
// agree with the prefix because a consumer learns the vocabulary once:
// something that knows "M-x" is Mega must not meet a second spelling here.
func TestModifierKeysNameTheirSideAndPrefixLetter(t *testing.T) {
	for _, c := range []struct{ raw, want, what string }{
		{"\x1b[57441;1:1u", "LMod:S", "Shift"},
		{"\x1b[57442;1:1u", "LMod:C", "Control"},
		{"\x1b[57443;1:1u", "LMod:M", "Mega"},
		{"\x1b[57444;1:1u", "LMod:s", "Super"},
		{"\x1b[57445;1:1u", "LMod:H", "Hyper"},
		{"\x1b[57446;1:1u", "LMod:m", "Micro"},

		{"\x1b[57449;1:1u", "RMod:M", "Mega, right side"},
		{"\x1b[57452;1:1u", "RMod:m", "Micro, right side"},

		{"\x1b[57443;1:3u", "LMod:M:Release", "Mega release"},
	} {
		got := decodeModifierKey(t, c.raw)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q decoded as %v, want [%s]", c.what, c.raw, got, c.want)
		}
	}
}

// A held modifier does not repeat.
//
// The terminal repeats it as readily as any other key, but nothing has changed
// between one report and the next: the modifier was down and it still is. A
// repeat is worth hearing when the key produces something each time; a modifier
// produces nothing on its own, so its repeats are only noise on the channel.
func TestModifierKeysDoNotRepeat(t *testing.T) {
	for _, raw := range []string{
		"\x1b[57443;1:2u", // Mega, left
		"\x1b[57441;1:2u", // Shift, left
		"\x1b[57448;1:2u", // Control, right
	} {
		if got := decodeModifierKey(t, raw); len(got) != 0 {
			t.Errorf("%q repeated as %v, want nothing", raw, got)
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
// "A-" is not in namePrefixes and nothing downstream can parse it: it is not a
// modifier to key-sequence-processor, so it would become part of the base name
// and match nothing. Alt is spelled "M-", for Mega.
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
