package tui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Everything draal or a zathras sent is text of someone else: a name, a
// phase, a reason, a refusal, a line of a log. None of it reaches the
// terminal as it came. All of it goes through scan, which lets printable
// characters through and nothing else: no escape sequence, no control
// character. A line of a log keeps its colours by being read: scan
// reports what its SGR sequences ask for, and the terminal UI draws the
// text again in colours of its own making.
//
// What scan lets through, and what it makes of the rest:
//
//   - An escape sequence is taken out whole: CSI (ESC [ … final), the
//     strings OSC, DCS, SOS, PM and APC up to their terminator (BEL, ST,
//     CAN, SUB) or, without one, to the end of the text, and any other
//     sequence of ESC, intermediates and a final byte. One that is cut
//     off is taken out as far as it goes, and a lone ESC by itself. The
//     C1 forms of the same (U+009B, U+0090, U+009D, …) likewise.
//   - A tab is spaces up to the next column that is a multiple of 8.
//   - A line feed, a carriage return and their like are one space: the
//     words of two lines stay two words.
//   - Every other control character (C0, DEL, C1) is left out.
//   - A byte that is no UTF-8 is drawn as U+FFFD, as is a character that
//     Unicode has not assigned.
//   - Characters that draw nothing and move things are left out: the
//     bidi marks and overrides, with which a line could show another
//     order than it has, the zero-width space and joiners, the byte
//     order mark, the soft hyphen, the tag characters (all of the class
//     Cf), the variation selectors and the skin tones of emoji. Without
//     them a family of emoji is its members side by side, and every
//     terminal counts the same width for what is left.
//   - A regional indicator is its letter: two of them are a flag in one
//     terminal and two boxes in another, of a width nobody agrees on.
//   - A combining mark is kept on the character before it, three at
//     most: a letter can have its accents, and cannot be buried under a
//     hundred. One with nothing before it, or behind something that was
//     taken out, is left out.
//   - Any kind of space is the space.

// marksKept is how many combining marks a character keeps.
const marksKept = 3

// tabStop is how far apart the columns are that a tab moves to.
const tabStop = 8

// look is how a piece of text is drawn. The zero look is the terminal's
// own.
type look struct {
	fg, bg    uint8 // 1 to 16 for the terminal's colours 0 to 15; 0 for its default
	bold      bool
	dim       bool
	italic    bool
	underline bool
	inverse   bool
}

// span is a piece of text with one look. Its text is clean: it went
// through scan.
type span struct {
	text string
	look look
}

// runeWidth returns how many cells a printable character takes: 2 for
// the wide ones; 0 for a mark that takes no cell of its own (a
// nonspacing or an enclosing one); for every other character the larger
// of the two counts the renderer has, by graphemes and by characters, as
// the terminal has it, so that a column is never counted narrower than a
// terminal draws it. Where the two differ (a spacing mark of Devanagari
// or Myanmar, which a terminal that joins it to its base draws in the
// base's cell) a column is padded a cell short on such a terminal, and
// that is all.
func runeWidth(r rune) int {
	switch {
	case r < 0x7f:
		return 1
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
		return 0
	}
	return min(max(ansi.StringWidth(string(r)), ansi.StringWidthWc(string(r))), 2)
}

// dropped says whether r is a character that draws nothing and is left
// out: see the list above.
func dropped(r rune) bool {
	switch {
	case unicode.Is(unicode.Cf, r):
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // variation selectors
		return true
	case r >= 0x1f3fb && r <= 0x1f3ff: // skin tones
		return true
	}
	return false
}

// scanner is one run of scan.
type scanner struct {
	limit int // cells after which it stops; below 0 for never
	col   int // cells so far
	marks int // marks on the last character
	base  bool
	space bool // the last thing written was the space of a line's end
	run   strings.Builder
	text  func(string)
	sgr   func(params []int)
}

// full says whether the limit is reached.
func (sc *scanner) full() bool {
	return sc.limit >= 0 && sc.col >= sc.limit
}

// put writes a character of the given width, if there is room for it.
func (sc *scanner) put(r rune, width int) {
	if sc.limit >= 0 && sc.col+width > sc.limit {
		sc.col = sc.limit
		return
	}
	sc.run.WriteRune(r)
	sc.col += width
	sc.marks, sc.base, sc.space = 0, true, false
}

// flush hands the text gathered so far to the caller.
func (sc *scanner) flush() {
	if sc.run.Len() > 0 {
		sc.text(sc.run.String())
		sc.run.Reset()
	}
}

// scan reads s as the terminal would, and calls text with every run of
// what is printable and, if sgr is set, sgr with the numbers of every
// SGR sequence, in order. It stops when it has let limit cells through;
// a limit below zero is none. Its time is linear in what it reads.
func scan(s string, limit int, text func(string), sgr func(params []int)) {
	sc := scanner{limit: limit, text: text, sgr: sgr}
	for i := 0; i < len(s) && !sc.full(); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n <= 1 {
			sc.put(utf8.RuneError, 1)
			i++
			continue
		}
		i += n
		switch {
		case r == 0x1b:
			i = sc.escape(s, i)
			sc.base = false
		case r == 0x9b:
			i = sc.csi(s, i)
			sc.base = false
		case r == 0x90, r == 0x98, r == 0x9d, r == 0x9e, r == 0x9f:
			i = endOfString(s, i)
			sc.base = false
		case r == '\t':
			for range tabStop - sc.col%tabStop {
				sc.put(' ', 1)
			}
		case r == '\n', r == '\r', r == '\v', r == '\f', r == 0x85, r == 0x2028, r == 0x2029:
			if !sc.space {
				sc.put(' ', 1)
				sc.space = true
			}
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			// A control character: nothing.
			sc.base = false
		case dropped(r):
		case r >= 0x1f1e6 && r <= 0x1f1ff:
			sc.put('A'+r-0x1f1e6, 1) // a regional indicator: its letter
		case unicode.Is(unicode.Zs, r):
			sc.put(' ', 1)
		case unicode.Is(unicode.Co, r):
			sc.put(r, 1) // private use: the glyphs of a font, one cell each
		case !unicode.IsPrint(r):
			sc.put(utf8.RuneError, 1)
		default:
			w := runeWidth(r)
			if w > 0 {
				sc.put(r, w)
			} else if sc.base && sc.marks < marksKept {
				sc.run.WriteRune(r)
				sc.marks++
			}
		}
	}
	sc.flush()
}

// escape reads what follows an ESC at s[i:] and returns where the text
// goes on.
func (sc *scanner) escape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return sc.csi(s, i+1)
	case ']', 'P', 'X', '^', '_':
		return endOfString(s, i+1)
	}
	// Any other sequence: intermediates and one final byte.
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// csi reads a control sequence whose introducer ended before s[i:], and
// returns where the text goes on. One that sets colours (its final byte
// is m) is reported; one that is cut off is taken out as far as it goes.
func (sc *scanner) csi(s string, i int) int {
	start := i
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	params := s[start:i]
	intermediates := false
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
		intermediates = true
	}
	if i >= len(s) || s[i] < 0x40 || s[i] > 0x7e {
		return i
	}
	if s[i] == 'm' && !intermediates && sc.sgr != nil {
		if numbers, ok := sgrNumbers(params); ok {
			sc.flush()
			sc.sgr(numbers)
		}
	}
	return i + 1
}

// sgrNumbers reads the parameters of an SGR sequence: numbers with ; or
// : between them, an empty one being 0. More than 32 numbers, or
// anything else among them, is no SGR sequence the terminal UI reads.
func sgrNumbers(params string) ([]int, bool) {
	numbers := make([]int, 0, 4)
	n, digits := 0, 0
	for i := 0; i <= len(params); i++ {
		switch {
		case i == len(params) || params[i] == ';' || params[i] == ':':
			if len(numbers) == 32 {
				return nil, false
			}
			numbers = append(numbers, n)
			n, digits = 0, 0
		case params[i] >= '0' && params[i] <= '9':
			if digits++; digits > 4 {
				return nil, false
			}
			n = n*10 + int(params[i]-'0')
		default:
			return nil, false
		}
	}
	return numbers, true
}

// endOfString returns where the text goes on after a string sequence
// (OSC, DCS, SOS, PM, APC) whose introducer ended before s[i:]: behind
// its terminator, at an ESC that begins something else, or at the end.
func endOfString(s string, i int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case 0x07, 0x18, 0x1a:
			return i + 1
		case 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return i
		case 0xc2:
			if i+1 < len(s) && s[i+1] == 0x9c { // ST as a C1 character
				return i + 2
			}
		}
	}
	return i
}

// clean returns s as it may be drawn: see scan.
func clean(s string) string {
	// Most of what is drawn is plain already.
	plain := true
	for i := 0; i < len(s) && plain; i++ {
		plain = s[i] >= 0x20 && s[i] < 0x7f
	}
	if plain {
		return s
	}
	var b strings.Builder
	scan(s, -1, func(t string) { b.WriteString(t) }, nil)
	return b.String()
}

// fieldCap is how many bytes are kept of something draal or a zathras
// said in words: a name, a reason, an argument, a refusal. What a
// person reads of one is some lines; a megabyte of it would be cleaned
// and wrapped anew with every message.
const fieldCap = 4 << 10

// capped returns s, or its first fieldCap bytes and how many more there
// were. It is applied where the words come in; what is drawn of them
// still goes through clean.
func capped(s string) string {
	if len(s) <= fieldCap || len(s) <= fieldCap+48 && strings.HasSuffix(s, " bytes more)") {
		return s // short, or capped before
	}
	keep := fieldCap
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + fmt.Sprintf(" … (%d bytes more)", len(s)-keep)
}

// named returns a name, clean, or words that say that there is none: a
// name of which nothing printable is left would be a hole in a line.
func named(s string) string {
	if s = clean(capped(s)); strings.TrimSpace(s) != "" {
		return s
	}
	return "(no name)"
}

// width returns how many cells s takes once it is clean.
func width(s string) int {
	n := 0
	for _, r := range clean(s) {
		n += runeWidth(r)
	}
	return n
}

// cutClean returns the start of the clean text s that fits into w
// cells, and how many cells it takes.
func cutClean(s string, w int) (string, int) {
	n := 0
	for i, r := range s {
		rw := runeWidth(r)
		if n+rw > w {
			return s[:i], n
		}
		n += rw
	}
	return s, n
}

// tailClean returns the end of the clean text s that fits into w cells,
// and how many cells it takes. It begins with a character, not with the
// marks of one that was cut away.
func tailClean(s string, w int) (string, int) {
	n, from := 0, len(s)
	for i := len(s); i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		rw := runeWidth(r)
		if n+rw > w {
			break
		}
		i -= size
		n += rw
		if rw > 0 {
			from = i
		}
	}
	return s[from:], n
}

// ellipsis stands where text was cut.
const ellipsis = "…"

// cut returns s, clean, in at most w cells: what is longer ends in an
// ellipsis.
func cut(s string, w int) string {
	s = clean(s)
	if w <= 0 {
		return ""
	}
	if all, _ := cutClean(s, w); len(all) == len(s) {
		return s
	}
	head, _ := cutClean(s, w-1)
	return head + ellipsis
}

// middle returns s, clean, in at most w cells: of what is longer the
// start and the end are kept, with an ellipsis between them. The names
// of the zathrases of one service differ at their ends.
func middle(s string, w int) string {
	s = clean(s)
	if w <= 0 {
		return ""
	}
	if all, _ := cutClean(s, w); len(all) == len(s) {
		return s
	}
	if w < 5 {
		head, _ := cutClean(s, w-1)
		return head + ellipsis
	}
	tail, back := tailClean(s, (w-1)/2)
	head, _ := cutClean(s, w-1-back)
	return head + ellipsis + tail
}

// pad returns s, clean, in exactly w cells: cut as cut does, or with
// spaces behind it.
func pad(s string, w int) string {
	s = cut(s, w)
	if n := w - width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// padLeft returns s, clean, in exactly w cells: cut as cut does, or with
// spaces before it.
func padLeft(s string, w int) string {
	s = cut(s, w)
	if n := w - width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// wrap returns s, clean, as lines of at most w cells, broken at spaces
// where there are any, and at most rows of them: what does not fit ends
// in an ellipsis. No rows is no limit.
func wrap(s string, w, rows int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	line, used := "", 0
	end := func() {
		lines = append(lines, line)
		line, used = "", 0
	}
	for _, word := range strings.Fields(clean(s)) {
		ww := width(word)
		if used > 0 && used+1+ww > w {
			end()
		}
		if used > 0 {
			line += " "
			used++
		}
		for ww > w-used { // a word longer than a line is broken
			head, n := cutClean(word, w-used)
			if n == 0 && used > 0 {
				end()
				continue
			}
			if n == 0 {
				// A wide character and one cell: nothing fits, ever.
				return lines
			}
			line += head
			word, ww = word[len(head):], ww-n
			end()
		}
		line += word
		used += ww
	}
	if used > 0 {
		end()
	}
	if rows > 0 && len(lines) > rows {
		lines = lines[:rows]
		last, _ := cutClean(lines[rows-1], w-1)
		lines[rows-1] = last + ellipsis
	}
	return lines
}

// apply returns the look after an SGR sequence with these numbers.
func (l look) apply(numbers []int) look {
	for i := 0; i < len(numbers); i++ {
		switch n := numbers[i]; {
		case n == 0:
			l = look{}
		case n == 1:
			l.bold = true
		case n == 2:
			l.dim = true
		case n == 3:
			l.italic = true
		case n == 4:
			l.underline = true
		case n == 7:
			l.inverse = true
		case n == 22:
			l.bold, l.dim = false, false
		case n == 23:
			l.italic = false
		case n == 24:
			l.underline = false
		case n == 27:
			l.inverse = false
		case n >= 30 && n <= 37:
			l.fg = uint8(n - 30 + 1)
		case n == 39:
			l.fg = 0
		case n >= 40 && n <= 47:
			l.bg = uint8(n - 40 + 1)
		case n == 49:
			l.bg = 0
		case n >= 90 && n <= 97:
			l.fg = uint8(n - 90 + 8 + 1)
		case n >= 100 && n <= 107:
			l.bg = uint8(n - 100 + 8 + 1)
		case n == 38 || n == 48:
			// 38;5;N is one of 256 colours, 38;2;R;G;B one of all: the
			// terminal UI draws the nearest of the terminal's sixteen.
			var c uint8
			switch {
			case i+2 < len(numbers) && numbers[i+1] == 5:
				c = nearest256(numbers[i+2])
				i += 2
			case i+4 < len(numbers) && numbers[i+1] == 2:
				c = nearestRGB(numbers[i+2], numbers[i+3], numbers[i+4])
				i += 4
			default:
				// No form the terminal UI knows: the rest of the
				// sequence is not guessed at.
				return l
			}
			if n == 38 {
				l.fg = c
			} else {
				l.bg = c
			}
		}
		// Any other number (blinking, fonts, …) changes nothing here.
	}
	return l
}

// cube is the steps of the colour cube of the 256 colours.
var cube = [6]int{0, 95, 135, 175, 215, 255}

// nearest256 returns the one of the terminal's sixteen colours, as a
// look has them, that is nearest to the colour n of 256.
func nearest256(n int) uint8 {
	switch {
	case n < 0 || n > 255:
		return 0
	case n < 16:
		return uint8(n + 1)
	case n < 232:
		c := n - 16
		return nearestRGB(cube[c/36], cube[c/6%6], cube[c%6])
	}
	grey := 8 + (n-232)*10
	return nearestRGB(grey, grey, grey)
}

// nearestRGB returns the one of the terminal's sixteen colours, as a
// look has them, that is nearest to a colour by its parts: the colour
// whose parts are on where these are above the middle, the bright one of
// it where one of them is near the top, and the greys by how light they
// are.
func nearestRGB(r, g, b int) uint8 {
	r, g, b = min(max(r, 0), 255), min(max(g, 0), 255), min(max(b, 0), 255)
	top, bottom := max(r, g, b), min(r, g, b)
	if top-bottom < 32 { // a grey
		switch {
		case top < 64:
			return 0 + 1 // black
		case top < 128:
			return 8 + 1 // bright black
		case top < 208:
			return 7 + 1 // white
		}
		return 15 + 1 // bright white
	}
	half := top / 2
	c := 0
	if r > half {
		c |= 1
	}
	if g > half {
		c |= 2
	}
	if b > half {
		c |= 4
	}
	if top >= 224 {
		c += 8
	}
	return uint8(c + 1)
}

// parseLine reads a line of a log into its pieces: the printable text
// of at most limit cells, each piece with the look its SGR sequences
// asked for. cut says that the line had more than limit cells.
func parseLine(line string, limit int) (spans []span, cut bool) {
	var l look
	// One cell more than the limit is asked for, to know of a line that
	// is longer.
	scan(line, limit+1, func(t string) {
		spans = append(spans, span{text: t, look: l})
	}, func(numbers []int) {
		l = l.apply(numbers)
	})
	total := 0
	for i := range spans {
		head, n := cutClean(spans[i].text, limit-total)
		if len(head) < len(spans[i].text) {
			spans[i].text = head
			spans = spans[:i+1]
			if head == "" {
				spans = spans[:i]
			}
			return spans, true
		}
		total += n
	}
	return spans, false
}

// spansWidth returns how many cells the pieces take.
func spansWidth(spans []span) int {
	n := 0
	for _, sp := range spans {
		for _, r := range sp.text {
			n += runeWidth(r)
		}
	}
	return n
}

// wrapSpans returns the pieces as rows of at most w cells, broken where
// a row is full, and at most rows of them. cut says that there was more.
func wrapSpans(spans []span, w, rows int) (out [][]span, cut bool) {
	if w <= 0 || rows <= 0 {
		return nil, len(spans) > 0
	}
	var row []span
	used := 0
	for _, sp := range spans {
		text := sp.text
		for text != "" {
			head, n := cutClean(text, w-used)
			if head != "" {
				row = append(row, span{text: head, look: sp.look})
				used += n
				text = text[len(head):]
			}
			if text == "" {
				break
			}
			if used == 0 {
				return out, true // a wide character and one cell
			}
			out = append(out, row)
			row, used = nil, 0
			if len(out) == rows {
				return out, true
			}
		}
	}
	if len(row) > 0 || len(out) == 0 {
		out = append(out, row)
	}
	return out, false
}

// colours are the terminal's sixteen, as Lip Gloss names them.
var colours = [16]ansi.BasicColor{
	lipgloss.Black, lipgloss.Red, lipgloss.Green, lipgloss.Yellow,
	lipgloss.Blue, lipgloss.Magenta, lipgloss.Cyan, lipgloss.White,
	lipgloss.BrightBlack, lipgloss.BrightRed, lipgloss.BrightGreen, lipgloss.BrightYellow,
	lipgloss.BrightBlue, lipgloss.BrightMagenta, lipgloss.BrightCyan, lipgloss.BrightWhite,
}

// style returns the look as a style of Lip Gloss, in the terminal's own
// colours.
func (l look) style() lipgloss.Style {
	st := lipgloss.NewStyle()
	if l.fg > 0 && l.fg <= 16 {
		st = st.Foreground(colours[l.fg-1])
	}
	if l.bg > 0 && l.bg <= 16 {
		st = st.Background(colours[l.bg-1])
	}
	return st.Bold(l.bold).Faint(l.dim).Italic(l.italic).Underline(l.underline).Reverse(l.inverse)
}

// drawSpans draws the pieces, each in its look. Nothing of what it
// returns but the text of the pieces is of the line they were read
// from: the escape sequences are those Lip Gloss writes.
func drawSpans(spans []span) string {
	var b strings.Builder
	for _, sp := range spans {
		if sp.look == (look{}) {
			b.WriteString(sp.text)
			continue
		}
		b.WriteString(sp.look.style().Render(sp.text))
	}
	return b.String()
}
