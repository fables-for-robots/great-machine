package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// plain returns what a person reads of something that was drawn: the
// text without the escape sequences that colour it. Every test of what
// is drawn compares this.
func plain(s string) string {
	return ansi.Strip(s)
}

// unsafe returns the first thing in s that a terminal would do
// something with, or "" when s is text, line feeds and SGR sequences
// (ESC [ numbers m) alone. It is the check of Review Focus 2: what the
// terminal UI draws moves no cursor, sets no title and clears nothing.
func unsafe(s string) string {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n <= 1:
			return fmt.Sprintf("the byte %#x at %d, which is no UTF-8", s[i], i)
		case r == 0x1b:
			j := i + 1
			if j >= len(s) || s[j] != '[' {
				return fmt.Sprintf("an escape sequence that is no SGR at %d: %q", i, s[i:min(len(s), i+12)])
			}
			for j++; j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';'); j++ {
			}
			if j >= len(s) || s[j] != 'm' {
				return fmt.Sprintf("an escape sequence that is no SGR at %d: %q", i, s[i:min(len(s), i+12)])
			}
			i = j + 1
			continue
		case r == '\n':
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			return fmt.Sprintf("the control character %#x at %d", r, i)
		case unicode.Is(unicode.Cf, r):
			return fmt.Sprintf("the format character %U at %d", r, i)
		}
		i += n
	}
	return ""
}

// The worst that a zathras can write, and what is left of it.
var hostile = []struct {
	name, in, want string
}{
	{"plain text", "GET /api/orders 200", "GET /api/orders 200"},
	{"a title, ended by BEL", "a\x1b]0;pwned\x07b", "ab"},
	{"a title, ended by ST", "a\x1b]2;pwned\x1b\\b", "ab"},
	{"the clipboard", "a\x1b]52;c;cm0gLXJmIH4=\x07b", "ab"},
	{"a link", "\x1b]8;;http://evil.example\x1b\\click\x1b]8;;\x1b\\", "click"},
	{"a cursor move", "a\x1b[10;10Hb", "ab"},
	{"clearing the screen", "a\x1b[2J\x1b[H\x1b[3Jb", "ab"},
	{"leaving the alternate screen", "a\x1b[?1049l\x1b[?25hb", "ab"},
	{"a scroll region and a reset", "a\x1b[1;5r\x1bcb", "ab"},
	{"asking the terminal to answer", "a\x1b[6n\x1b[c\x1b]10;?\x07b", "ab"},
	{"a colour", "\x1b[1;31mERROR\x1b[0m failed", "ERROR failed"},
	{"a sequence with intermediates", "a\x1b[ qb\x1b[!pc", "abc"},
	{"a sequence cut off at the end", "abc\x1b[31", "abc"},
	{"an introducer at the end", "abc\x1b[", "abc"},
	{"an ESC at the end", "abc\x1b", "abc"},
	{"a title cut off at the end", "abc\x1b]0;half a tit", "abc"},
	{"the rest of a title that the buffer cut", "le\x07 and text", "le and text"},
	{"the rest of a colour that the buffer cut", "1;31mERROR\x1b[0m", "1;31mERROR"},
	{"nothing but ESC", strings.Repeat("\x1b", 1000), ""},
	{"ESC before a sequence", "\x1b\x1b[31mx", "x"},
	{"a sequence that another one interrupts", "\x1b[12\x1b[31mx", "x"},
	{"a title that a sequence interrupts", "\x1b]0;t\x1b[31mx", "x"},
	{"a line feed inside a sequence", "\x1b[1\n2Hx", " 2Hx"},
	{"a title ended by CAN", "\x1b]0;t\x18x", "x"},
	{"DCS", "a\x1bPq#0;2;0;0;0#0~~\x1b\\b", "ab"},
	{"APC, as Kitty's pictures", "a\x1b_Gf=100;AAAA\x1b\\b", "ab"},
	{"PM and SOS", "a\x1b^secret\x1b\\b\x1bXmore\x1b\\c", "abc"},
	{"DCS without an end", "a\x1bP1$r never ended", "a"},
	{"a character set", "a\x1b(0b\x1b)Bc", "abc"},
	{"saving the cursor, the alignment test", "a\x1b7\x1b8\x1b#8b", "ab"},
	{"C1: a control sequence", "a\u009b31mb\u009b2Jc", "abc"},
	{"C1: a title with its end", "a\u009d0;t\u009cb", "ab"},
	{"C1: a title without its end", "a\u009d0;t", "a"},
	{"C1: DCS, SOS, PM, APC", "a\u0090x\x1b\\b\u0098x\x07c\u009ex\x07d\u009fx\x07e", "abcde"},
	{"C1: the others", "a\u0080\u0084\u0099b", "ab"},
	{"C1: next line", "a\u0085b", "a b"},
	{"C0", "a\x00b\x07c\x08d\x7fe\x0ef\x0fg\x1ah", "abcdefgh"},
	{"a carriage return and a line feed", "a\r\nb", "a b"},
	{"line feeds", "a\n\n\nb\vc\fd", "a b c d"},
	{"a carriage return, which would write over the line", "the truth\rthe lie", "the truth the lie"},
	{"a backspace, which would write over a character", "rm -rf /\x08\x08\x08\x08\x08\x08\x08\x08ls      ", "rm -rf /ls      "},
	{"a tab", "a\tb", "a       b"},
	{"a tab at the start", "\tb", "        b"},
	{"a tab at a stop", "12345678\tb", "12345678        b"},
	{"a tab behind a wide character", "注\tb", "注      b"},
	{"a byte that is no UTF-8", "a\xffb", "a�b"},
	{"a character cut off", "a\xe2\x82", "a��"},
	{"C1 as a byte", "a\x9b31mb", "a�31mb"},
	{"an overlong ESC", "a\xc0\x9b[2Jb", "a��[2Jb"},
	{"a bidi override", "abc\u202edef\u202c", "abcdef"},
	{"bidi isolates and marks", "\u2066a\u2069\u200eb\u200f\u061cc", "abc"},
	{"zero-width characters", "a\u200bb\u200cc\u200dd\xef\xbb\xbfe\u00adf\u2060g", "abcdefg"},
	{"a family of emoji", "👨\u200d👩\u200d👧", "👨👩👧"},
	{"a variation selector", "✔\ufe0f ok", "✔ ok"},
	{"a skin tone", "👋\U0001f3fd", "👋"},
	{"tag characters", "a\U000e0041\U000e007fb", "ab"},
	{"an accent", "cafe\u0301", "cafe\u0301"},
	{"a hundred accents", "a" + strings.Repeat("\u0301", 100) + "b", "a\u0301\u0301\u0301b"},
	{"an accent on nothing", "\u0301a", "a"},
	{"an accent behind a sequence", "\x1b[31m\u0301a", "a"},
	{"spaces of every kind", "a\u00a0b\u3000c\u2003d\u2028e\u2029f", "a b c d e f"},
	{"a character nobody assigned", "a\u0378b", "a�b"},
	{"a noncharacter", "a\uffffb", "a�b"},
	{"private use", "\ue0b0 main", "\ue0b0 main"},
	{"wide characters", "注文 ｗｉｄｅ 🚀", "注文 ｗｉｄｅ 🚀"},
	{"a flag", "🇩🇪 and 🇪", "DE and E"},
	{"an accent behind something that was taken out", "a\x1b[0m\u0301b\x07\u0301c\u200b\u0301", "abc\u0301"},
	{"a megabyte of titles", strings.Repeat("\x1b]0;x\x07", 1<<17) + "end", "end"},
	{"a megabyte of cursor moves", strings.Repeat("\x1b[9999;9999H", 1<<17) + "end", "end"},
	{"the empty text", "", ""},
}

// Review Focus 2: bytes of a zathras on the operator's terminal. Nothing
// but printable text is left of them, whatever they were.
func TestCleanLeavesNothingATerminalActsOn(t *testing.T) {
	for _, tt := range hostile {
		got := clean(tt.in)
		if got != tt.want {
			t.Errorf("%s: clean(%.60q) = %.80q, want %.80q", tt.name, tt.in, got, tt.want)
		}
		if what := unsafe(got); what != "" {
			t.Errorf("%s: what is left has %s", tt.name, what)
		}
		if again := clean(got); again != got {
			t.Errorf("%s: cleaned twice it is %.80q, and once %.80q", tt.name, again, got)
		}
	}
}

// Whatever is made of these pieces: the result is safe, and cleaning it
// again changes nothing. And for these pieces, which are what the logs
// of the fixture and the demo are made of, the width this package
// counts is the renderer's, by characters as by graphemes. That is not
// so for every script: see TestNoLineIsWiderByTheRenderersCounts.
func TestCleanOfThesePieces(t *testing.T) {
	pieces := []string{
		"\x1b", "[", "]", "P", "_", "^", "X", "\\", "0", "1", "3", "8", ";", ":", "?", "m", "H", "J", " ", "!",
		"\x07", "\x18", "\x00", "\x08", "\t", "\n", "\r", "\x7f", "\u009b", "\u009d", "\u009c", "\u0090", "\x9b", "\xff", "\xe2", "\x82",
		"a", "Z", "é", "e\u0301", "\u0301", "注", "ｗ", "🚀", "👨", "\u200d", "\ufe0f", "\U0001f3fd", "\u202e", "\u00a0", "\u3000", "한", "ก", "\u0e31", "🇩", "🇪",
	}
	rng := rand.New(rand.NewPCG(3, 3))
	for range 20000 {
		var b strings.Builder
		for range 1 + rng.IntN(24) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		in := b.String()
		got := clean(in)
		if what := unsafe(got); what != "" {
			t.Fatalf("clean(%q) = %q has %s", in, got, what)
		}
		if again := clean(got); again != got {
			t.Fatalf("clean(%q) = %q, and cleaned again %q", in, got, again)
		}
		if w, g, wc := width(got), ansi.StringWidth(got), ansi.StringWidthWc(got); w != g || w != wc {
			t.Fatalf("clean(%q) = %q is %d cells wide, %d by graphemes and %d by characters", in, got, w, g, wc)
		}
		spans, _ := parseLine(in, 1<<20)
		var text strings.Builder
		for _, sp := range spans {
			text.WriteString(sp.text)
		}
		if text.String() != got {
			t.Fatalf("the pieces of %q are %q, and clean says %q", in, text.String(), got)
		}
		if drawn := drawSpans(spans); unsafe(drawn) != "" || plain(drawn) != got {
			t.Fatalf("%q is drawn as %q: %s", in, drawn, unsafe(drawn))
		}
	}
}

func TestWidthCutAndPad(t *testing.T) {
	for _, tt := range []struct {
		in    string
		width int
	}{
		{"", 0}, {"abc", 3}, {"注文", 4}, {"ｗｉｄｅ", 8}, {"cafe\u0301", 4}, {"🚀", 2}, {"a\tb", 9},
		{"\x1b[31mred\x1b[0m", 3}, {"👨\u200d👩\u200d👧", 6},
	} {
		if got := width(tt.in); got != tt.width {
			t.Errorf("width(%q) = %d, want %d", tt.in, got, tt.width)
		}
	}
	for _, tt := range []struct {
		in                        string
		w                         int
		cut, middle, pad, padLeft string
	}{
		{"web-0", 8, "web-0", "web-0", "web-0   ", "   web-0"},
		{"web-7d9f8b6c4-2xkqp", 10, "web-7d9f8…", "web-7…xkqp", "web-7d9f8…", "web-7d9f8…"},
		{"web-7d9f8b6c4-2xkqp", 4, "web…", "web…", "web…", "web…"},
		{"web", 1, "…", "…", "…", "…"},
		{"web", 0, "", "", "", ""},
		{"web", -3, "", "", "", ""},
		{"注文を受け付けました", 7, "注文を…", "注文…た", "注文を…", "注文を…"},
		{"注文", 3, "注…", "注…", "注…", "注…"},
		{"a\x1b]0;t\x07b\x1b[2J", 5, "ab", "ab", "ab   ", "   ab"},
		{"cafe\u0301s au lait", 6, "cafe\u0301s…", "caf…it", "cafe\u0301s…", "cafe\u0301s…"},
	} {
		if got := cut(tt.in, tt.w); got != tt.cut {
			t.Errorf("cut(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.cut)
		}
		if got := middle(tt.in, tt.w); got != tt.middle {
			t.Errorf("middle(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.middle)
		}
		if got := pad(tt.in, tt.w); got != tt.pad {
			t.Errorf("pad(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.pad)
		}
		if got := padLeft(tt.in, tt.w); got != tt.padLeft {
			t.Errorf("padLeft(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.padLeft)
		}
		for _, got := range []string{cut(tt.in, tt.w), middle(tt.in, tt.w), pad(tt.in, tt.w), padLeft(tt.in, tt.w)} {
			if width(got) > max(tt.w, 0) {
				t.Errorf("%q is wider than %d", got, tt.w)
			}
		}
	}
	// A wide character that does not fit leaves its cell empty: the
	// padded text is as wide as was asked for.
	if got := pad("注文", 3); width(got) != 3 {
		t.Errorf("pad(注文, 3) = %q, %d cells wide", got, width(got))
	}
}

func TestWrap(t *testing.T) {
	reason := "web-7d9f8b6c4-8hjwz: could not be started: fork/exec /nix/store/abc-web/bin/web: exec format error"
	for _, tt := range []struct {
		in      string
		w, rows int
		want    []string
	}{
		{"", 10, 0, nil},
		{"one", 10, 0, []string{"one"}},
		{reason, 40, 0, []string{"web-7d9f8b6c4-8hjwz: could not be", "started: fork/exec", "/nix/store/abc-web/bin/web: exec format", "error"}},
		{reason, 40, 2, []string{"web-7d9f8b6c4-8hjwz: could not be", "started: fork/exec…"}},
		{"abcdefghijklmnop", 5, 0, []string{"abcde", "fghij", "klmno", "p"}},
		{"ab cdefghijklmnop q", 5, 0, []string{"ab", "cdefg", "hijkl", "mnop", "q"}},
		{"a\nb\x1b[2Jc  d", 10, 0, []string{"a bc d"}},
		{"注文を受け付けました", 5, 0, []string{"注文", "を受", "け付", "けま", "した"}},
		{"注文", 1, 0, nil},
		{"abc", 0, 0, nil},
		{"one two three", 5, 1, []string{"one…"}},
		{"12345 12345", 5, 1, []string{"1234…"}},
	} {
		got := wrap(tt.in, tt.w, tt.rows)
		if !slices.Equal(got, tt.want) {
			t.Errorf("wrap(%q, %d, %d) = %q, want %q", tt.in, tt.w, tt.rows, got, tt.want)
		}
		for _, line := range got {
			if width(line) > tt.w {
				t.Errorf("wrap(%q, %d, %d): %q is wider than %d", tt.in, tt.w, tt.rows, line, tt.w)
			}
		}
	}
}

func TestParseLineReadsColours(t *testing.T) {
	red, green := uint8(1+1), uint8(2+1)
	for _, tt := range []struct {
		name, in string
		want     []span
	}{
		{"no colour", "plain", []span{{"plain", look{}}}},
		{"the colours of a service", "\x1b[2m12:00:00\x1b[0m \x1b[1;31mERROR\x1b[0m failed", []span{
			{"12:00:00", look{dim: true}}, {" ", look{}}, {"ERROR", look{fg: red, bold: true}}, {" failed", look{}},
		}},
		{"ESC [ m is a reset", "\x1b[32mok\x1b[m done", []span{{"ok", look{fg: green}}, {" done", look{}}}},
		{"an empty number is 0", "\x1b[32mok\x1b[;1mbold", []span{{"ok", look{fg: green}}, {"bold", look{bold: true}}}},
		{"a look that is never reset ends with the line", "\x1b[32mok", []span{{"ok", look{fg: green}}}},
		{"the bright colours and the grounds", "\x1b[91;44ma\x1b[39;102mb\x1b[49mc", []span{
			{"a", look{fg: 9 + 1, bg: 4 + 1}}, {"b", look{bg: 10 + 1}}, {"c", look{}},
		}},
		{"what is switched off again", "\x1b[1;2;3;4;7ma\x1b[22mb\x1b[23;24;27mc", []span{
			{"a", look{bold: true, dim: true, italic: true, underline: true, inverse: true}},
			{"b", look{italic: true, underline: true, inverse: true}}, {"c", look{}},
		}},
		{"one of 256: the sixteen are themselves", "\x1b[38;5;1ma\x1b[48;5;10mb", []span{{"a", look{fg: red}}, {"b", look{fg: red, bg: 10 + 1}}}},
		{"one of 256: the cube is the nearest of sixteen", "\x1b[38;5;196ma\x1b[38;5;28mb\x1b[38;5;240mc", []span{
			{"a", look{fg: 9 + 1}}, {"b", look{fg: green}}, {"c", look{fg: 8 + 1}},
		}},
		{"RGB is the nearest of sixteen", "\x1b[38;2;255;135;0ma\x1b[38;2;0;0;128mb\x1b[48;2;250;250;250mc", []span{
			{"a", look{fg: 11 + 1}}, {"b", look{fg: 4 + 1}}, {"c", look{fg: 4 + 1, bg: 15 + 1}},
		}},
		{"with colons", "\x1b[38:5:196ma", []span{{"a", look{fg: 9 + 1}}}},
		{"a colour of 256 that is cut off", "\x1b[38;5ma\x1b[1;38;2;1;2mb", []span{{"a", look{}}, {"b", look{bold: true}}}},
		{"numbers nobody knows", "\x1b[5;53;999;73ma", []span{{"a", look{}}}},
		{"numbers too large and too many", "\x1b[99999ma\x1b[" + strings.Repeat("1;", 40) + "mb", []span{{"ab", look{}}}},
		{"a private sequence that ends in m", "\x1b[?1ma\x1b[>4;2mb", []span{{"ab", look{}}}},
		{"a sequence that is no colour between", "\x1b[31ma\x1b[2Kb\x1b]0;t\x07c", []span{{"abc", look{fg: red}}}},
		{"C1", "\u009b31ma", []span{{"a", look{fg: red}}}},
		{"nothing printable", "\x1b[31m\x1b[0m", nil},
	} {
		got, cut := parseLine(tt.in, 1000)
		// Pieces of one look that follow each other are one piece to the reader.
		var merged []span
		for _, sp := range got {
			if n := len(merged); n > 0 && merged[n-1].look == sp.look {
				merged[n-1].text += sp.text
			} else {
				merged = append(merged, sp)
			}
		}
		if !slices.Equal(merged, tt.want) || cut {
			t.Errorf("%s: parseLine(%q) = %+v (cut: %v), want %+v", tt.name, tt.in, merged, cut, tt.want)
		}
	}
}

// A line as long as can be costs what is drawn of it, and no more is
// kept of it.
func TestParseLineStopsAtItsLimit(t *testing.T) {
	long := "\x1b[32m" + strings.Repeat("0123456789", 1<<20/10)
	spans, cut := parseLine(long, 200)
	if !cut || spansWidth(spans) != 200 || len(spans) != 1 || spans[0].look.fg != 3 {
		t.Errorf("a megabyte in 200 cells: %d cells in %d pieces, cut: %v", spansWidth(spans), len(spans), cut)
	}
	spans, cut = parseLine(strings.Repeat("注", 1000), 201)
	if !cut || spansWidth(spans) != 200 {
		t.Errorf("wide characters in 201 cells: %d cells, cut: %v; want 200, as half a character is none", spansWidth(spans), cut)
	}
	spans, cut = parseLine("exactly", 7)
	if cut || spansWidth(spans) != 7 {
		t.Errorf("a line of 7 cells in 7: %d cells, cut: %v", spansWidth(spans), cut)
	}
	if spans, cut = parseLine("exactly\x1b[0m\x1b]0;t\x07", 7); cut || spansWidth(spans) != 7 {
		t.Errorf("a line of 7 cells and sequences behind them in 7: %d cells, cut: %v", spansWidth(spans), cut)
	}
	if spans, cut = parseLine("anything", 0); !cut || len(spans) != 0 {
		t.Errorf("a line in 0 cells: %+v, cut: %v", spans, cut)
	}
}

func TestWrapSpans(t *testing.T) {
	red := look{fg: 2}
	texts := func(rows [][]span) []string {
		var out []string
		for _, row := range rows {
			var b strings.Builder
			for _, sp := range row {
				b.WriteString(sp.text)
			}
			out = append(out, b.String())
		}
		return out
	}
	for _, tt := range []struct {
		name    string
		in      []span
		w, rows int
		want    []string
		cut     bool
	}{
		{"nothing is one empty row", nil, 10, 4, []string{""}, false},
		{"what fits", []span{{"abc", red}, {"def", look{}}}, 10, 4, []string{"abcdef"}, false},
		{"what fills the row", []span{{"abcde", red}}, 5, 4, []string{"abcde"}, false},
		{"over two rows, through a piece", []span{{"abc", red}, {"defgh", look{}}}, 5, 4, []string{"abcde", "fgh"}, false},
		{"more rows than there may be", []span{{"abcdefghijkl", red}}, 5, 2, []string{"abcde", "fghij"}, true},
		{"exactly the rows there may be", []span{{"abcdefghij", red}}, 5, 2, []string{"abcde", "fghij"}, false},
		{"a wide character does not straddle", []span{{"ab注文", red}}, 3, 4, []string{"ab", "注", "文"}, false},
		{"a wide character and one cell", []span{{"注", red}}, 1, 4, nil, true},
		{"no room", []span{{"a", red}}, 0, 4, nil, true},
	} {
		got, cut := wrapSpans(tt.in, tt.w, tt.rows)
		if !slices.Equal(texts(got), tt.want) || cut != tt.cut {
			t.Errorf("%s: %q (cut: %v), want %q (cut: %v)", tt.name, texts(got), cut, tt.want, tt.cut)
		}
	}
	rows, _ := wrapSpans([]span{{"abc", red}, {"defgh", look{}}}, 5, 4)
	if len(rows) != 2 || len(rows[0]) != 2 || rows[0][0] != (span{"abc", red}) || rows[0][1] != (span{"de", look{}}) || rows[1][0] != (span{"fgh", look{}}) {
		t.Errorf("the pieces keep their looks over a break: %+v", rows)
	}
}

// Review Focus 2: a line of a log is drawn in colours the terminal UI
// makes, and nothing of the line's own sequences is in what is drawn.
func TestDrawSpansWritesItsOwnSequences(t *testing.T) {
	line := "\x1b]0;pwned\x07\x1b[2J\x1b[1;31mERROR\x1b[0m \x1b[38;5;196mdisk\x1b[0m \x1b[5;10Hfull\x1b[?1049l"
	spans, _ := parseLine(line, 1000)
	drawn := drawSpans(spans)
	if got := plain(drawn); got != "ERROR disk full" {
		t.Errorf("drawn, a person reads %q", got)
	}
	if what := unsafe(drawn); what != "" {
		t.Errorf("what is drawn has %s: %q", what, drawn)
	}
	if !strings.Contains(drawn, "\x1b[") || strings.Contains(drawn, "pwned") || strings.Contains(drawn, "38;5") || strings.Contains(drawn, "2J") {
		t.Errorf("what is drawn is %q: want colours of the sixteen, and nothing of the title", drawn)
	}
	if got := drawSpans([]span{{"plain", look{}}}); got != "plain" {
		t.Errorf("text without a look is drawn as %q", got)
	}
	// Every colour of the sixteen is one of the terminal's palette: 30
	// to 37 and 90 to 97, never a colour by its number or its parts.
	for c := range uint8(16) {
		drawn := drawSpans([]span{{"x", look{fg: c + 1}}})
		want := fmt.Sprintf("\x1b[%dm", 30+int(c))
		if c >= 8 {
			want = fmt.Sprintf("\x1b[%dm", 90+int(c)-8)
		}
		if !strings.HasPrefix(drawn, want) || plain(drawn) != "x" {
			t.Errorf("colour %d is drawn as %q, want it to begin with %q", c, drawn, want)
		}
	}
}

func FuzzClean(f *testing.F) {
	for _, tt := range hostile {
		if len(tt.in) < 4096 {
			f.Add(tt.in)
		}
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := clean(in)
		if what := unsafe(got); what != "" {
			t.Fatalf("clean(%q) = %q has %s", in, got, what)
		}
		if again := clean(got); again != got {
			t.Fatalf("clean(%q) = %q, and cleaned again %q", in, got, again)
		}
		spans, _ := parseLine(in, 1<<20)
		if drawn := drawSpans(spans); unsafe(drawn) != "" || plain(drawn) != got {
			t.Fatalf("%q is drawn as %q: %s", in, drawn, unsafe(drawn))
		}
	})
}
