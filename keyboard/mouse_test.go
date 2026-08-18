package keyboard

import (
	"reflect"
	"testing"
)

// The scroll wheel encodes its axis/direction in the low two bits of the SGR
// button code: 64 = up, 65 = down, 66 = left, 67 = right. All four must decode
// to distinct actions — a decoder that only knows up/down makes a horizontal
// gesture scroll vertically.
func TestScrollWheelDirections(t *testing.T) {
	for _, c := range []struct {
		cb   int
		want string
	}{
		{64, "MouseScrollUp"},
		{65, "MouseScrollDown"},
		{66, "MouseScrollLeft"},
		{67, "MouseScrollRight"},
	} {
		got := decodeModifierKey(t, sgr(c.cb, 10, 5, false))
		want := []string{"Mouse@10,5", c.want}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scroll cb=%d decoded as %v, want %v", c.cb, got, want)
		}
	}
}

// A button reports where it happened and then what happened, and the release
// says which button it is the release OF by naming the press.
func TestMouseButtonsPressAndRelease(t *testing.T) {
	for _, c := range []struct {
		cb   int
		name string
	}{
		{0, "MouseLeft"},
		{1, "MouseMiddle"},
		{2, "MouseRight"},
	} {
		got := decodeModifierKey(t, sgr(c.cb, 3, 4, false)+sgr(c.cb, 3, 4, true))
		want := []string{"Mouse@3,4", c.name, "Mouse@3,4", c.name + ":Release"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("button %d pressed and released -> %v, want %v", c.cb, got, want)
		}
	}
}

// The modifiers held with a button are spelled in the same canonical order as
// everywhere else, and the release carries the name the PRESS was given —
// letting go of Control before the button must not rename the event.
func TestMouseModifiersAreCanonicalAndTheReleaseMatches(t *testing.T) {
	const ctrl, mega, shift = 16, 8, 4
	if got, want := decodeModifierKey(t, sgr(ctrl|mega|shift, 1, 1, false)),
		[]string{"Mouse@1,1", "C-M-S-MouseLeft"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Ctrl+Mega+Shift+left -> %v, want %v", got, want)
	}

	got := decodeModifierKey(t, sgr(ctrl, 1, 1, false)+sgr(0, 2, 2, true))
	want := []string{"Mouse@1,1", "C-MouseLeft", "Mouse@2,2", "C-MouseLeft:Release"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ctrl+left pressed, Control let go, then released -> %v, want %v", got, want)
	}
}

// A drag IS its position, so it carries one instead of reporting one first.
// With no button down it is just the pointer moving, and names no button.
func TestMouseDrag(t *testing.T) {
	for _, c := range []struct {
		cb   int
		want string
	}{
		{32, "MouseDragLeft@7,8"},
		{33, "MouseDragMiddle@7,8"},
		{34, "MouseDragRight@7,8"},
		{35, "MouseDrag@7,8"},
	} {
		got := decodeModifierKey(t, sgr(c.cb, 7, 8, false))
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("motion cb=%d -> %v, want [%s]", c.cb, got, c.want)
		}
	}
}

// X10's release does not say which button was let go. The buttons this package
// has reported down are the only honest answer, so it releases each of them —
// never a bare "Mouse:Release", which names no key at all.
func TestX10ReleaseNamesTheButtonsItPressed(t *testing.T) {
	got := decodeModifierKey(t, x10(0, 5, 6)+x10(2, 5, 6)+x10(3, 5, 6))
	want := []string{
		"Mouse@5,6", "MouseLeft",
		"Mouse@5,6", "MouseRight",
		"Mouse@5,6", "MouseLeft:Release", "MouseRight:Release",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("left and right down, then X10's button-agnostic release -> %v, want %v", got, want)
	}
}

// Releasing what was never pressed reports nothing but the position.
//
// A release for a button no press was emitted for would leave a consumer
// letting go of something it was never told it was holding, and the pointer
// having moved is the only part of that report this package can vouch for.
func TestMouseReleaseWithoutAPress(t *testing.T) {
	for _, raw := range []string{sgr(0, 9, 9, true), x10(3, 9, 9)} {
		got := decodeModifierKey(t, raw)
		want := []string{"Mouse@9,9"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("release with nothing held -> %v, want %v", got, want)
		}
	}
}

// The pointer going elsewhere releases the buttons for the same reason it
// releases the keys: the button-up will be delivered wherever the pointer went.
func TestReleaseHeldKeysReleasesTheMouseToo(t *testing.T) {
	_, _, h := numLockProbe(t, sgr(1, 2, 3, false))
	defer h.Stop()

	got := h.ReleaseHeldKeys()
	if len(got) != 1 || got[0] != "MouseMiddle:Release" {
		t.Errorf("ReleaseHeldKeys with the middle button down -> %v, want [MouseMiddle:Release]", got)
	}
	if again := h.ReleaseHeldKeys(); len(again) != 0 {
		t.Errorf("a second ReleaseHeldKeys -> %v, want nothing still held", again)
	}
}

// sgr spells one SGR mouse report: ESC [ < Cb ; Cx ; Cy M/m.
func sgr(cb, cx, cy int, release bool) string {
	final := "M"
	if release {
		final = "m"
	}
	return "\x1b[<" + itoaTest(cb) + ";" + itoaTest(cx) + ";" + itoaTest(cy) + final
}

// x10 spells one X10 mouse report: ESC [ M Cb Cx Cy, every field offset by 32.
func x10(cb, cx, cy int) string {
	return "\x1b[M" + string([]byte{byte(cb + 32), byte(cx + 32), byte(cy + 32)})
}
