package draal

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/fables-for-robots/great-machine/wire"
)

// What draal keeps of what a zathras sends: registration is open to every
// peer, and what one sends is kept and shown to every operator.
const (
	maxName      = 253  // bytes of the name of a zathras
	maxVersion   = 64   // bytes of the version of its program
	maxBinary    = 4096 // bytes of State.Binary
	maxArgs      = 128  // arguments of State.Args
	maxArg       = 1024 // bytes of one of them
	maxExitError = 1024 // bytes of Exit.Error
	maxSignal    = 32   // bytes of Exit.Signal
	maxPrepared  = 64   // generations of State.Prepared, the highest
	maxReason    = 4096 // bytes of the reason a zathras gives for a failure
)

// validName returns nil for a name a zathras may register with, and a
// bad-request otherwise: 1 to 253 bytes of A-Z, a-z, 0-9, '.', '_' and
// '-', beginning with a letter or a digit. A host name, which is the
// default, is one.
func validName(name string) error {
	if name == "" || len(name) > maxName {
		return wire.Errorf(wire.CodeBadRequest, "the name of a zathras has %d bytes: 1 to %d are allowed", len(name), maxName)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '.' || c == '_' || c == '-') && i > 0:
		default:
			return wire.Errorf(wire.CodeBadRequest, "%q is no name of a zathras: A to Z, a to z, 0 to 9, '.', '_' and '-' are allowed, and it begins with a letter or a digit", name)
		}
	}
	return nil
}

// validVersion returns nil for the version of a zathras' program, and a
// bad-request otherwise: at most 64 bytes of printable ASCII, 0x20 to
// 0x7E. It may be empty.
func validVersion(version string) error {
	if len(version) > maxVersion {
		return wire.Errorf(wire.CodeBadRequest, "the version of a zathras has %d bytes: %d are the most", len(version), maxVersion)
	}
	for i := 0; i < len(version); i++ {
		if c := version[i]; c < 0x20 || c > 0x7e {
			return wire.Errorf(wire.CodeBadRequest, "%q is no version of a zathras: printable ASCII alone is allowed", version)
		}
	}
	return nil
}

// knownPhase says whether p is one of the phases of a zathras.
func knownPhase(p wire.Phase) bool {
	switch p {
	case wire.PhaseIdle, wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff:
		return true
	}
	return false
}

// bounded returns st as draal keeps it, and whether anything of it had to
// be cut or replaced: every string valid UTF-8 without control characters,
// Binary cut at 4096 bytes, Args at 128 arguments of 1024 bytes, the
// error and the signal of LastExit at 1024 and 32 bytes, and of Prepared
// the 64 highest generations. The phase is not looked at: knownPhase is.
func bounded(st wire.State) (wire.State, bool) {
	changed := false
	cut := func(s string, max int) string {
		c := clean(s, max)
		changed = changed || c != s
		return c
	}
	st.Binary = cut(st.Binary, maxBinary)
	if st.Args != nil {
		args := make([]string, 0, min(len(st.Args), maxArgs))
		for _, arg := range st.Args[:min(len(st.Args), maxArgs)] {
			args = append(args, cut(arg, maxArg))
		}
		changed = changed || len(st.Args) > maxArgs
		st.Args = args
	}
	if st.LastExit != nil {
		exit := *st.LastExit
		exit.Error = cut(exit.Error, maxExitError)
		exit.Signal = cut(exit.Signal, maxSignal)
		st.LastExit = &exit
	}
	if len(st.Prepared) > maxPrepared {
		prepared := slices.Clone(st.Prepared)
		slices.Sort(prepared)
		st.Prepared = slices.Clip(prepared[len(prepared)-maxPrepared:])
		changed = true
	}
	return st, changed
}

// clean returns s as valid UTF-8, with every control character (U+0000 to
// U+001F, U+007F to U+009F) replaced by a space, and cut on a rune at
// most max bytes.
func clean(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
