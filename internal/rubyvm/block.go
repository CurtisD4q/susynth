package rubyvm

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// BlockRange is the 1-based inclusive span of source that ^E runs when the
// cursor is on cursorLine. That span is one top-level statement: a live_loop,
// do/end or brace block stays intact when it contains blank lines, and a blank
// line between statements separates them. A cursor on a blank or comment line
// selects the following statement (or the previous one, at the end of the buffer).
func BlockRange(code string, cursorLine int) (first, last int, ok bool) {
	lines := splitLines(code)
	if len(lines) == 0 || cursorLine < 1 {
		return 0, 0, false
	}
	spans := statementSpans(lines)
	if len(spans) == 0 {
		return 0, 0, false
	}
	if cursorLine > len(lines) {
		cursorLine = len(lines)
	}
	for _, sp := range spans {
		if cursorLine >= sp[0] && cursorLine <= sp[1] {
			return sp[0], sp[1], true
		}
	}
	for _, sp := range spans {
		if sp[0] > cursorLine {
			return sp[0], sp[1], true
		}
	}
	sp := spans[len(spans)-1]
	return sp[0], sp[1], true
}

// LiveLoopNames returns the live_loop names written in code. A name that only
// appears in a comment does not count: that loop is no longer in the set.
func LiveLoopNames(code string) map[string]bool {
	names := map[string]bool{}
	for _, line := range splitLines(code) {
		line = stripComment(line)
		rest := line
		for {
			i := strings.Index(rest, "live_loop")
			if i < 0 {
				break
			}
			if i > 0 && isIdentRune(rune(rest[i-1])) {
				rest = rest[i+len("live_loop"):]
				continue
			}
			tail := strings.TrimSpace(rest[i+len("live_loop"):])
			if strings.HasPrefix(tail, "(") {
				tail = strings.TrimSpace(tail[1:])
			}
			tail = strings.TrimLeft(tail, ":'\"")
			name, n := identAt(tail)
			if n > 0 {
				names[name] = true
			}
			rest = rest[i+len("live_loop"):]
		}
	}
	return names
}

func splitLines(code string) []string {
	code = strings.ReplaceAll(code, "\r\n", "\n")
	code = strings.ReplaceAll(code, "\r", "\n")
	code = strings.TrimRight(code, "\n")
	if code == "" {
		return nil
	}
	return strings.Split(code, "\n")
}

func stripComment(line string) string {
	var q rune
	esc := false
	for i, c := range line {
		if q != 0 {
			if esc {
				esc = false
				continue
			}
			if c == '\\' && q == '"' {
				esc = true
				continue
			}
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '#':
			return line[:i]
		case '\'', '"':
			q = c
		}
	}
	return line
}

func identAt(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	r, w := utf8.DecodeRuneInString(s)
	if r != '_' && !unicode.IsLetter(r) {
		return "", 0
	}
	i := w
	for i < len(s) {
		r, w = utf8.DecodeRuneInString(s[i:])
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		i += w
	}
	return s[:i], i
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// statementSpans finds each top-level statement as a 1-based inclusive line pair.
// Depth comes from do/end, braces and keywords, so a blank line inside a block
// does not split it. Modifier if/unless/while/until do not open a block.
func statementSpans(lines []string) [][2]int {
	s := scanner{r: []rune(strings.Join(lines, "\n")), line: 1}
	var spans [][2]int
	var stmtStart int
	var stack []frame
	prevExpr := false
	cont := false
	pendingDo := false // while/until/for may take an optional do
	doAtBol := false   // that do may sit on the next line
	var inStr rune

	finish := func(line int) {
		if stmtStart == 0 {
			return
		}
		if line < stmtStart {
			line = stmtStart
		}
		spans = append(spans, [2]int{stmtStart, line})
		stmtStart = 0
	}
	ensure := func(line int) {
		if stmtStart == 0 {
			stmtStart = line
		}
	}

	for {
		if s.done() {
			if stmtStart != 0 {
				finish(s.line)
			}
			break
		}
		c := s.peek()
		if c == '\n' {
			if inStr == 0 {
				prevExpr = false
				if pendingDo {
					doAtBol = true
					pendingDo = false
				} else {
					doAtBol = false
				}
				if len(stack) == 0 && !cont {
					finish(s.line)
				}
				cont = false
			}
			s.next()
			continue
		}
		if c == ' ' || c == '\t' || c == '\f' {
			s.next()
			continue
		}
		if inStr == 0 && c == '#' {
			for !s.done() && s.peek() != '\n' {
				s.next()
			}
			continue
		}
		line := s.line
		if inStr != 0 {
			ensure(line)
			if !s.skipString(&inStr, &stack) {
				break
			}
			prevExpr = true
			cont = false
			continue
		}
		if c == '\\' {
			ensure(line)
			s.next()
			cont = true
			prevExpr = false
			doAtBol = false
			continue
		}
		if c == ';' && len(stack) == 0 {
			finish(line)
			s.next()
			prevExpr = false
			cont = false
			doAtBol = false
			pendingDo = false
			continue
		}

		ensure(line)
		doAtBol = consumeToken(&s, &stack, &prevExpr, &cont, &pendingDo, doAtBol, &inStr)
	}
	return spans
}

type frame int

const (
	frEnd   frame = iota // closed by end
	frBrace              // closed by }
	frParen
	frBrack
	frInterp // #{ ... } inside a double-quoted string
)

type scanner struct {
	r    []rune
	i    int
	line int
}

func (s *scanner) done() bool { return s.i >= len(s.r) }
func (s *scanner) peek() rune {
	if s.done() {
		return 0
	}
	return s.r[s.i]
}
func (s *scanner) next() rune {
	if s.done() {
		return 0
	}
	c := s.r[s.i]
	s.i++
	if c == '\n' {
		s.line++
	}
	return c
}

// consumeToken reads one token. It returns the new doAtBol flag.
func consumeToken(s *scanner, stack *[]frame, prevExpr, cont, pendingDo *bool, doAtBol bool, inStr *rune) bool {
	c := s.peek()
	if c == '"' || c == '\'' {
		*inStr = c
		s.next()
		if !s.skipString(inStr, stack) {
			return false
		}
		*prevExpr = true
		*cont = false
		return false
	}
	if c == ':' && s.i+1 < len(s.r) && (isIdentRune(s.r[s.i+1]) || s.r[s.i+1] == '"' || s.r[s.i+1] == '\'') && (s.i == 0 || s.r[s.i-1] != ':') {
		s.next()
		if s.peek() == '"' || s.peek() == '\'' {
			*inStr = s.next()
			s.skipString(inStr, stack)
		} else {
			readIdent(s)
		}
		*prevExpr = true
		*cont = false
		return false
	}
	if c == '%' && !*prevExpr && isPercentString(s) {
		readPercent(s, stack)
		*prevExpr = true
		*cont = false
		return false
	}
	if unicode.IsDigit(c) || (c == '.' && s.i+1 < len(s.r) && unicode.IsDigit(s.r[s.i+1])) {
		readNumber(s)
		*prevExpr = true
		*cont = false
		return false
	}
	if c == '_' || unicode.IsLetter(c) {
		word := readIdent(s)
		return takeWord(word, stack, prevExpr, cont, pendingDo, doAtBol)
	}
	// one- or two-character operator / delimiter
	op := string(c)
	if s.i+1 < len(s.r) {
		two := string(s.r[s.i : s.i+2])
		switch two {
		case "==", "!=", "<=", ">=", "=>", "&&", "||", "<<", ">>", "**", "+=", "-=", "*=", "/=", "%=", "..", "::", "=~":
			op = two
		}
	}
	s.i += len([]rune(op))
	switch op {
	case "{":
		*stack = append(*stack, frBrace)
		*prevExpr = false
		*cont = false
	case "}":
		if top := popMatch(stack, frBrace); top == frInterp {
			*inStr = '"'
		}
		*prevExpr = true
		*cont = false
	case "(":
		*stack = append(*stack, frParen)
		*prevExpr = false
		*cont = false
	case ")":
		popMatch(stack, frParen)
		*prevExpr = true
		*cont = false
	case "[":
		*stack = append(*stack, frBrack)
		*prevExpr = false
		*cont = false
	case "]":
		popMatch(stack, frBrack)
		*prevExpr = true
		*cont = false
	default:
		*prevExpr = false
		*cont = true
	}
	return false
}

func takeWord(word string, stack *[]frame, prevExpr, cont, pendingDo *bool, doAtBol bool) bool {
	switch word {
	case "do":
		if *pendingDo || doAtBol {
			*pendingDo = false
			*prevExpr = false
			*cont = false
			return false
		}
		*stack = append(*stack, frEnd)
		*prevExpr = false
		*cont = false
		return false
	case "end":
		popMatch(stack, frEnd)
		*pendingDo = false
		*prevExpr = false
		*cont = false
		return false
	case "if", "unless", "while", "until":
		if *prevExpr { // modifier: sleep 1 if x
			*prevExpr = false
			*cont = true
			return false
		}
		*stack = append(*stack, frEnd)
		*prevExpr = false
		*cont = false
		if word == "while" || word == "until" {
			*pendingDo = true
		}
		return false
	case "for", "begin", "def", "class", "module", "case":
		*stack = append(*stack, frEnd)
		*prevExpr = false
		*cont = false
		if word == "for" {
			*pendingDo = true
		}
		return false
	case "and", "or", "not":
		*prevExpr = false
		*cont = true
		return false
	case "else", "elsif", "when", "then", "rescue", "ensure", "in":
		*prevExpr = false
		*cont = false
		return false
	default:
		*prevExpr = true
		*cont = false
		return false
	}
}

func popMatch(stack *[]frame, want frame) frame {
	if len(*stack) == 0 {
		return -1
	}
	top := (*stack)[len(*stack)-1]
	if want == frBrace && top == frInterp {
		*stack = (*stack)[:len(*stack)-1]
		return frInterp
	}
	if top == want {
		*stack = (*stack)[:len(*stack)-1]
		return top
	}
	// a mismatched closer: drop a same-kind frame deeper in the stack if the
	// top is an interpolation or just ignore, so one typo doesn't swallow the
	// rest of the buffer into one block
	return -1
}

func readIdent(s *scanner) string {
	start := s.i
	s.next()
	for !s.done() {
		c := s.peek()
		if c != '_' && !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			break
		}
		s.next()
	}
	// foo? and foo! are identifiers
	if !s.done() && (s.peek() == '?' || s.peek() == '!') {
		s.next()
	}
	return string(s.r[start:s.i])
}

func readNumber(s *scanner) {
	if s.peek() == '.' {
		s.next()
	}
	for !s.done() && unicode.IsDigit(s.peek()) {
		s.next()
	}
	if s.peek() == '.' && s.i+1 < len(s.r) && unicode.IsDigit(s.r[s.i+1]) {
		s.next()
		for !s.done() && unicode.IsDigit(s.peek()) {
			s.next()
		}
	}
}

// skipString consumes the inside of a string. *q is the closer, or 0 when the
// string has ended. It returns false at EOF.
func (s *scanner) skipString(q *rune, stack *[]frame) bool {
	closer := *q
	for !s.done() {
		c := s.next()
		if c == '\\' {
			if !s.done() {
				s.next()
			}
			continue
		}
		if closer == '"' && c == '#' && s.peek() == '{' {
			s.next() // {
			*stack = append(*stack, frInterp)
			*q = 0
			return true
		}
		if c == closer {
			*q = 0
			return true
		}
	}
	return false
}

func isPercentString(s *scanner) bool {
	if s.i+1 >= len(s.r) {
		return false
	}
	n := s.r[s.i+1]
	if n == '(' || n == '[' || n == '{' || n == '<' || n == '|' || n == '/' || n == '!' {
		return true
	}
	if unicode.IsLetter(n) && s.i+2 < len(s.r) {
		d := s.r[s.i+2]
		return d == '(' || d == '[' || d == '{' || d == '<' || d == '|' || d == '/' || d == '!'
	}
	return false
}

func readPercent(s *scanner, stack *[]frame) {
	s.next() // %
	if unicode.IsLetter(s.peek()) {
		s.next()
	}
	open := s.next()
	closer := open
	switch open {
	case '(':
		closer = ')'
	case '[':
		closer = ']'
	case '{':
		closer = '}'
	case '<':
		closer = '>'
	}
	depth := 1
	for !s.done() && depth > 0 {
		c := s.next()
		if c == '\\' {
			if !s.done() {
				s.next()
			}
			continue
		}
		if c == open && open != closer {
			depth++
		} else if c == closer {
			depth--
		}
	}
	_ = stack
}
