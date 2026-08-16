package keyboard

import (
	"sort"
	"strings"
)

// Key identifies a physical key whose emitted name an application can choose,
// independent of the spelling this package happens to default to.
//
// The names in Keys are a wire format an application often has to translate:
// an editor whose bindings are space-separated tokens needs "space" rather
// than a literal " ", and one whose binding syntax grew from WordStar calls
// forward-delete "fdel". Doing that downstream means re-parsing a string this
// package just built — stripping the modifier prefixes it just added — and it
// matches on spelling, so a rename here breaks a consumer silently.
//
// A Key is stable identity instead. Name it in Options.KeyNames and this
// package emits your spelling directly, prefixes and event suffixes intact.
// Names are presentation; the constant is what your code should switch on.
//
// KeyReturn and KeyKeypadEnter are deliberately separate: they are two
// physical keys, and an application that wants them equivalent should say so
// by mapping both to one name.
type Key int

const (
	KeyNone Key = iota

	KeyEscape
	KeyTab
	KeySpace

	// The two erase bytes, which are two keys on the wire and one key in most
	// people's hands. A terminal sends BS (8) or DEL (127) for its backspace
	// depending on its lineage — terminfo still carries both answers, kbs=^H
	// for vt100/vt220/ansi and kbs=^? for xterm, linux, screen, tmux, rxvt —
	// so an application that maps input to key events cannot know which it
	// will get. Naming them apart is the only way it can tell; folding them
	// together here would throw the distinction away before anyone could.
	//
	// Note KeyDEL is what a MODERN terminal's backspace key produces. The
	// kitty protocol reports keys rather than bytes, so it resolves the
	// ambiguity on its own and gives KeyBackspace directly.
	KeyBackspace   // BS (8), Ctrl-H
	KeyDEL         // DEL (127), the byte most terminals send for backspace
	KeyReturn      // the home-row key: CR, Ctrl-M
	KeyKeypadEnter // the smaller key on the numeric keypad

	KeyInsert
	// KeyDelete is forward delete — the key labelled Del, which sends CSI 3 ~
	// and erases ahead of the cursor. It is NOT KeyDEL: that is the DEL
	// character, which erases behind. They are named "FDel" and "Delete" to
	// keep the pair from reading as each other.
	KeyDelete
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown

	KeyUp
	KeyDown
	KeyLeft
	KeyRight

	KeyCapsLock
	KeyScrollLock
	KeyNumLock
	KeyPrintScreen
	KeyPause
	KeyMenu

	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20

	keyMax // sentinel; keep last
)

// defaultKeyNames is the spelling this package emits with no override — the
// canonical name every internal table produces, and what DefaultName reports.
var defaultKeyNames = map[Key]string{
	KeyEscape:      "Escape",
	KeyTab:         "Tab",
	KeySpace:       "Space",
	KeyBackspace:   "Backspace",
	KeyDEL:         "Delete",
	KeyReturn:      "Return",
	KeyKeypadEnter: "Enter",

	KeyInsert:   "Insert",
	KeyDelete:   "FDel",
	KeyHome:     "Home",
	KeyEnd:      "End",
	KeyPageUp:   "PageUp",
	KeyPageDown: "PageDown",

	KeyUp:    "Up",
	KeyDown:  "Down",
	KeyLeft:  "Left",
	KeyRight: "Right",

	KeyCapsLock:    "CapsLock",
	KeyScrollLock:  "ScrollLock",
	KeyNumLock:     "NumLock",
	KeyPrintScreen: "PrintScreen",
	KeyPause:       "Pause",
	KeyMenu:        "Menu",

	KeyF1:  "F1",
	KeyF2:  "F2",
	KeyF3:  "F3",
	KeyF4:  "F4",
	KeyF5:  "F5",
	KeyF6:  "F6",
	KeyF7:  "F7",
	KeyF8:  "F8",
	KeyF9:  "F9",
	KeyF10: "F10",
	KeyF11: "F11",
	KeyF12: "F12",
	KeyF13: "F13",
	KeyF14: "F14",
	KeyF15: "F15",
	KeyF16: "F16",
	KeyF17: "F17",
	KeyF18: "F18",
	KeyF19: "F19",
	KeyF20: "F20",
}

// keyByDefaultName is the reverse of defaultKeyNames: it turns the base name a
// parse path produced back into its Key so an override can be applied once, at
// the point of emission, rather than at every table.
var keyByDefaultName = func() map[string]Key {
	m := make(map[string]Key, len(defaultKeyNames))
	for k, name := range defaultKeyNames {
		m[name] = k
	}
	return m
}()

// DefaultName returns the spelling this package emits for k when it is not
// renamed. Useful for diagnostics and for reporting gaps in a name table.
func (k Key) DefaultName() string { return defaultKeyNames[k] }

// String makes a Key printable in test failures and logs.
func (k Key) String() string {
	if name, ok := defaultKeyNames[k]; ok {
		return name
	}
	return "Key(?)"
}

// AllKeys returns every nameable Key, ordered by their default names, so an
// application can assert its own table covers them:
//
//	for _, k := range keyboard.AllKeys() {
//		if _, ok := myNames[k]; !ok {
//			t.Errorf("no name for %v (emits %q)", k, k.DefaultName())
//		}
//	}
//
// That test is what catches a key silently reaching an application under this
// package's spelling instead of its own.
func AllKeys() []Key {
	keys := make([]Key, 0, len(defaultKeyNames))
	for k := range defaultKeyNames {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return defaultKeyNames[keys[i]] < defaultKeyNames[keys[j]]
	})
	return keys
}

// namePrefixes are the modifier prefixes this package puts in front of a base
// name. Renaming splits them off and puts them back, so an override applies to
// "M-PageUp" as readily as to "PageUp".
//
// There is no "A-". "M-" is Mega and "m-" is Micro: two modifiers that both
// have a real claim to the name Meta, so neither is given it and they split by
// the case of their prefix instead. Mega is the one Emacs calls Meta, which a
// PC keyboard puts under the Alt cap; Micro is the one X11 and the Space Cadet
// call Meta, which the kitty protocol reports on its own bit.
var namePrefixes = []string{"S-", "M-", "m-", "C-", "s-", "H-", "G-"}

// nameSuffixes are the event/side suffixes that can trail a base name.
var nameSuffixes = []string{":Release", ":Repeat", ":Left", ":Right"}

// copyKeyNames snapshots a caller's name table, dropping empty names, so a map
// the caller keeps mutating can't change what the handler emits mid-run.
func copyKeyNames(names map[Key]string) map[Key]string {
	if len(names) == 0 {
		return nil
	}
	out := make(map[Key]string, len(names))
	for k, name := range names {
		if name != "" {
			out[k] = name
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// displayKey applies the application's names to a fully-formed key string,
// leaving the modifier prefixes and event suffix around it alone: "M-PageUp"
// becomes "M-pgup" when KeyPageUp is named "pgup".
//
// Splitting here rather than at each parse path is deliberate: every route in
// (kitty protocol, legacy CSI, SS3, control bytes) converges on emitKey having
// produced a canonical base name, so one place covers them all, and this
// package owns the prefix vocabulary it is splitting off. Keys with no name of
// their own — control chords like "^A", printable characters, modifier
// press/release events — pass through untouched.
func (h *Handler) displayKey(key string) string {
	if len(h.keyNames) == 0 {
		return key
	}

	base, suffix := key, ""
	for _, s := range nameSuffixes {
		if strings.HasSuffix(base, s) {
			base, suffix = base[:len(base)-len(s)], s
			break
		}
	}

	prefix := ""
	for {
		matched := false
		for _, p := range namePrefixes {
			if strings.HasPrefix(base, p) {
				prefix, base = prefix+p, base[len(p):]
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}

	k, ok := keyByDefaultName[base]
	if !ok {
		return key
	}
	name, ok := h.keyNames[k]
	if !ok {
		return key
	}
	return prefix + name + suffix
}
