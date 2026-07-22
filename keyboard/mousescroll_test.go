package keyboard

import "testing"

// The scroll wheel encodes its axis/direction in the low two bits of the SGR
// button code: 64 = up, 65 = down, 66 = left, 67 = right. All four must decode
// to distinct actions — a decoder that only knows up/down makes a horizontal
// gesture scroll vertically.
func TestScrollWheelDirections(t *testing.T) {
	cases := []struct {
		cb   int
		want string
	}{
		{64, "MouseScrollUp"},
		{65, "MouseScrollDown"},
		{66, "MouseScrollLeft"},
		{67, "MouseScrollRight"},
	}
	for _, c := range cases {
		_, action, ok := formatMouseEvent(c.cb, 10, 5, false)
		if !ok || action != c.want {
			t.Errorf("formatMouseEvent(cb=%d) = (%q, ok=%v), want %q", c.cb, action, ok, c.want)
		}
	}
}
