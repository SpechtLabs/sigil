package lsp

import (
	"slices"

	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/token"
)

// place is the kind of place a cursor is at, as far as completion goes.
type place uint8

const (
	nowhere      place = iota // a comment, a string, a name being declared: nothing completes
	atDocStart                // where a document header goes
	atHeaderKind              // the kind after `policy name:`
	atHeaderPin               // the version after `policy name: Kind@`
	atStatement               // where a statement starts
	atUsePath                 // the dotted name after `use`
	atUseItem                 // a name inside `use path.{`
	atType                    // a param's type
	atBound                   // after a param's default, where `min:` or `max:` goes
	atArgName                 // an argument's name in a constructor or invocation
	atReason                  // the value of a constructor's `reason:`
	atExpr                    // an operand of an expression
	atMember                  // the name after `.` or `?.`
	atOperator                // after an operand, where an operator goes
)

// frameKind is what an open bracket, or the document itself, holds.
type frameKind uint8

const (
	frameDoc    frameKind = iota // the document's top level
	frameBody                    // a `when` body
	frameCall                    // a constructor's or invocation's arguments
	frameAssert                  // an assert's parentheses
	frameItems                   // the names of a selective import
	frameGroup                   // any other bracket: arguments of a host function, parentheses, a list, a map
)

// frame is one open bracket, or the document, while the scanner walks the
// tokens before the cursor.
type frame struct {
	name string // the called name, for frameCall
	open int    // the index of the opening token; for frameDoc, the first token after the header
	stmt int    // where statements are: the index of the current statement's first token, -1 between statements
	kind frameKind
}

// binder is a quantifier or filter whose variable is in scope at the
// cursor: the cursor is in its body.
type binder struct {
	name   string // the variable
	from   int    // the index of the range's first token
	colon  int    // the index of the `:` that starts the body
	depth  int    // the bracket depth of the keyword
	filter bool   // `filter`, not `any` or `all`
}

// cursor is what the tokens before a position say about it: where in a
// document it is, and what's being typed there. It reads tokens rather
// than the syntax tree, so it works on a statement that doesn't parse
// yet, which is the statement being typed.
type cursor struct {
	src     []byte
	toks    []token.Token // the tokens before the name being typed, comments left out
	binders []binder      // the quantifiers and filters around the cursor, outermost first
	prefix  string        // the name being typed, up to the cursor
	call    string        // the constructor or invocation whose arguments the cursor is in
	arg     string        // the argument whose value the cursor is in
	path    string        // the dotted name typed so far after `use`, the import path of `use path.{`, or the kind before `@`
	kind    string        // the kind the document's header names
	args    []string      // the argument names the call already gives
	given   []string      // the names a selective import already lists
	offset  int           // the cursor
	start   int           // where the name being typed starts; offset when nothing is
	end     int           // where the name the cursor is in ends, the part after the cursor included; offset when nothing is
	from    int           // where the dotted name after `use` starts
	header  int           // the index of the document's header keyword, or -1
	recv    [2]int        // the tokens of the operand before `.`, for atMember: first and last index
	left    [2]int        // the tokens of the operand before `==` or `!=`, for atExpr, or -1 and -1
	place   place
	inBody  bool // atStatement in a `when` body, rather than at the top level
	module  bool // the document is a module
	assert  bool // the cursor is in an assert's condition
}

// scan reads src up to offset.
func scan(src []byte, offset int) *cursor {
	c := &cursor{src: src, offset: offset, start: offset, end: offset, header: -1, left: [2]int{-1, -1}}
	l := lexer.New(src)
	for t := l.Next(); t.Kind != token.EOF; t = l.Next() {
		if t.Pos.Offset >= offset {
			if t.Pos.Offset == offset && c.start == offset && (t.Kind == token.Ident || t.Kind.IsKeyword()) {
				c.end = t.End.Offset // the cursor is at the start of a name, as after a `.`
			}
			break
		}
		switch {
		case t.Kind == token.Comment && offset <= t.End.Offset,
			(t.Kind == token.String || t.Kind == token.RawString) && offset < t.End.Offset:
			c.place = nowhere
			return c
		case (t.Kind == token.Ident || t.Kind.IsKeyword()) && offset <= t.End.Offset:
			c.start, c.end, c.prefix = t.Pos.Offset, t.End.Offset, string(src[t.Pos.Offset:offset])
		case t.Kind != token.Comment:
			c.toks = append(c.toks, t)
		}
	}
	c.classify()
	return c
}

// classify works out where the cursor is.
func (c *cursor) classify() {
	c.header = c.findHeader()
	if c.header < 0 {
		if len(c.toks) == 0 || c.last().Kind == token.Separator {
			c.place = atDocStart
		}
		return
	}
	switch c.toks[c.header].Kind {
	case token.KwKind:
		return // kind documents get no completion
	case token.KwModule:
		c.module = true
	}
	body, ok := c.headerEnd()
	if !ok {
		return
	}
	frames := c.frames(body)
	top := &frames[len(frames)-1]
	prev := c.last()
	c.assert = c.inAssert(frames)
	switch {
	case prev.Kind == token.Dot || prev.Kind == token.OptDot:
		c.member(frames)
	case top.kind == frameItems:
		c.items(top)
	case top.kind == frameCall:
		c.inCall(top)
	case top.kind == frameAssert:
		if c.lastIndex() != top.open {
			c.expr(c.stmtOf(frames))
		}
	case top.kind == frameGroup:
		c.expr(c.stmtOf(frames))
	default:
		c.statement(top)
	}
}

// last returns the last token before the cursor, or EOF when there's
// none.
func (c *cursor) last() token.Token {
	if len(c.toks) == 0 {
		return token.Token{Kind: token.EOF}
	}
	return c.toks[len(c.toks)-1]
}

// lastIndex returns the index of the last token before the cursor.
func (c *cursor) lastIndex() int { return len(c.toks) - 1 }

// findHeader returns the index of the keyword of the header of the
// document the cursor is in, or -1 before the first header or right
// after a `---`. A header keyword starts its line or follows a `---`; one
// after a `.` is a field name.
func (c *cursor) findHeader() int {
	for i, t := range slices.Backward(c.toks) {
		switch {
		case t.Kind == token.Separator:
			return -1
		case t.Kind != token.KwPolicy && t.Kind != token.KwModule && t.Kind != token.KwKind:
		case i == 0, c.toks[i-1].Kind == token.Separator:
			return i
		case c.toks[i-1].Kind != token.Dot && t.Pos.Column == 1:
			return i
		}
	}
	return -1
}

// headerEnd returns the index of the first token after a policy's or
// module's header, `policy a.b: Kind@N`, or false when the cursor is in
// the header, which it then classifies.
func (c *cursor) headerEnd() (int, bool) {
	i := c.header + 1
	for i < len(c.toks) && (c.toks[i].Kind == token.Ident || c.toks[i].Kind == token.Dot) {
		i++
	}
	if i >= len(c.toks) || c.toks[i].Kind != token.Colon {
		return 0, false // the name
	}
	i++
	if i >= len(c.toks) {
		c.place = atHeaderKind
		return 0, false
	}
	if c.toks[i].Kind != token.Ident {
		return i, true
	}
	c.kind = c.toks[i].Text
	i++
	if i < len(c.toks) && c.toks[i].Kind == token.At {
		if i == c.lastIndex() {
			c.place = atHeaderPin
			c.path = c.toks[i-1].Text
			return 0, false
		}
		i++
		if i < len(c.toks) && c.toks[i].Kind == token.Int {
			i++
		}
	}
	return i, true
}

// frames walks the body's tokens and returns the brackets still open at
// the cursor, the document first.
func (c *cursor) frames(body int) []frame {
	frames := []frame{{kind: frameDoc, open: body, stmt: -1}}
	for i := body; i < len(c.toks); i++ {
		top := &frames[len(frames)-1]
		if level(top.kind) && c.startsStatement(i, top) {
			top.stmt = i
		}
		switch c.toks[i].Kind {
		case token.LParen, token.LBracket, token.LBrace:
			frames = append(frames, c.opening(i, top))
		case token.RParen, token.RBracket, token.RBrace:
			frames = closeFrame(frames)
		}
	}
	return frames
}

// opening returns the frame the bracket at index i opens inside top. It
// tells a `when` body from a map literal, and a constructor's parentheses
// from a host function's, the way the parser does: by what comes before.
func (c *cursor) opening(i int, top *frame) frame {
	f := frame{kind: frameGroup, open: i, stmt: -1, name: c.toks[i-1].Text}
	if !level(top.kind) || top.stmt < 0 {
		return f
	}
	first, bracket := c.toks[top.stmt].Kind, c.toks[i].Kind
	switch {
	case bracket == token.LParen && top.stmt == i-1 && first == token.Ident:
		f.kind = frameCall
	case bracket == token.LParen && top.stmt == i-1 && first == token.KwAssert:
		f.kind = frameAssert
	case bracket == token.LBrace && first == token.KwUse && c.toks[i-1].Kind == token.Dot:
		f.kind = frameItems
	case bracket == token.LBrace && first == token.KwWhen && (i-1 == top.stmt || endsOperand(c.toks[i-1])):
		f.kind = frameBody
	}
	return f
}

// closeFrame closes the innermost frame. A rule, a call, an assert or an
// import ends with its bracket, so the statement around it is over. The
// document's frame never closes.
func closeFrame(frames []frame) []frame {
	if len(frames) == 1 {
		return frames
	}
	closed := frames[len(frames)-1]
	frames = frames[:len(frames)-1]
	if closed.kind != frameGroup {
		frames[len(frames)-1].stmt = -1
	}
	return frames
}

// level reports whether a frame holds statements.
func level(k frameKind) bool { return k == frameDoc || k == frameBody }

// startsStatement reports whether token i starts a statement in top:
// anything between statements, a statement keyword, or a name with `(`
// after an operand, which can't continue an expression.
func (c *cursor) startsStatement(i int, top *frame) bool {
	t := c.toks[i]
	switch {
	case top.stmt < 0:
		return true
	case t.Kind == token.KwLet && c.toks[i-1].Kind == token.KwPub:
		return false
	case t.Kind == token.KwUse, t.Kind == token.KwParam, t.Kind == token.KwLet, t.Kind == token.KwPub,
		t.Kind == token.KwWhen, t.Kind == token.KwAssert:
		return true
	case t.Kind == token.Ident && i+1 < len(c.toks) && c.toks[i+1].Kind == token.LParen:
		return endsOperand(c.toks[i-1])
	}
	return false
}

// endsOperand reports whether t can end an operand, so what follows it
// is an operator or the next statement.
func endsOperand(t token.Token) bool {
	switch t.Kind {
	case token.Ident, token.Int, token.Float, token.Duration, token.String, token.RawString,
		token.RParen, token.RBracket, token.RBrace, token.KwTrue, token.KwFalse, token.KwOutcome:
		return true
	}
	return false
}

// statement classifies a cursor directly in a statement-level frame.
func (c *cursor) statement(top *frame) {
	c.inBody = top.kind == frameBody
	prev := c.last()
	if top.stmt < 0 || endsOperand(prev) && prev.Pos.Line < c.lineOfStart() {
		c.place = atStatement
		return
	}
	first := c.toks[top.stmt]
	switch first.Kind {
	case token.KwUse:
		c.usePath(top.stmt)
	case token.KwParam:
		c.param(top.stmt)
	case token.KwLet, token.KwPub:
		if c.seen(top.stmt, token.Assign) {
			c.expr(top.stmt)
		}
	case token.KwWhen:
		c.expr(top.stmt)
	}
}

// lineOfStart returns the line the name being typed, or the cursor, is
// on, from 1.
func (c *cursor) lineOfStart() int {
	line := 1
	for _, b := range c.src[:c.start] {
		if b == '\n' {
			line++
		}
	}
	return line
}

// seen reports whether a token of kind k comes after token from.
func (c *cursor) seen(from int, k token.Kind) bool {
	for _, t := range c.toks[from+1:] {
		if t.Kind == k {
			return true
		}
	}
	return false
}

// usePath classifies a cursor in `use`, the statement at index stmt:
// the dotted name, while only names and dots follow the keyword.
func (c *cursor) usePath(stmt int) {
	for i, t := range c.toks[stmt+1:] {
		if t.Kind != token.Ident && t.Kind != token.Dot || i%2 == 1 && t.Kind != token.Dot {
			return
		}
	}
	if c.lastIndex() > stmt && c.last().Kind == token.Ident {
		return // a whole name, followed by a space: `as` comes next
	}
	c.place = atUsePath
	c.from = c.start
	if stmt+1 < len(c.toks) {
		c.from = c.toks[stmt+1].Pos.Offset
	}
	c.path = string(c.src[c.from:c.offset])
}

// param classifies a cursor in a param, the statement at index stmt:
// `param name: type = default, min: x, max: y`.
func (c *cursor) param(stmt int) {
	if stmt+2 > c.lastIndex() || c.toks[stmt+2].Kind != token.Colon {
		return // the name
	}
	angle := 0
	inType := true
	for _, t := range c.toks[stmt+3:] {
		switch t.Kind {
		case token.Lt:
			angle++
		case token.Gt:
			angle--
		case token.Assign:
			inType = false
		case token.Comma:
			inType = inType && angle > 0
		}
	}
	prev := c.last()
	switch {
	case inType && (c.lastIndex() == stmt+2 || prev.Kind == token.Question || prev.Kind == token.Lt || prev.Kind == token.Comma):
		c.place = atType
	case !inType && prev.Kind == token.Comma:
		c.place = atBound
	case !inType:
		c.expr(stmt)
	}
}

// member classifies a cursor after `.` or `?.`: a dotted name after
// `use`, or a field, a pub let, an enum value or a reason after an
// operand, whose tokens it records.
func (c *cursor) member(frames []frame) {
	top := &frames[len(frames)-1]
	if level(top.kind) && top.stmt >= 0 && c.toks[top.stmt].Kind == token.KwUse {
		c.usePath(top.stmt)
		return
	}
	dot := c.lastIndex()
	first := c.operandStart(dot - 1)
	if first < 0 {
		return
	}
	c.place = atMember
	c.recv = [2]int{first, dot - 1}
	c.binders = c.bindersAt(c.stmtOf(frames))
}

// operandStart returns the index of the first token of the operand that
// ends at token i: a name, `outcome` or a bracketed group, followed by any
// run of `.name`, `?.name`, `[index]` and `(arguments)`. It returns -1
// when token i can't end an operand.
func (c *cursor) operandStart(i int) int {
	for i >= 0 {
		t := c.toks[i]
		switch t.Kind {
		case token.RParen, token.RBracket:
			open := c.matching(i)
			if open < 0 {
				return -1
			}
			i = open
			if t.Kind == token.RParen && (i == 0 || !endsOperand(c.toks[i-1])) {
				return i // parentheses around an expression
			}
			i--
			continue
		case token.Ident, token.KwOutcome:
		default:
			if !t.Kind.IsKeyword() || i == 0 || c.toks[i-1].Kind != token.Dot && c.toks[i-1].Kind != token.OptDot {
				return -1
			}
		}
		if i > 0 && (c.toks[i-1].Kind == token.Dot || c.toks[i-1].Kind == token.OptDot) {
			i -= 2
			continue
		}
		return i
	}
	return -1
}

// matching returns the index of the bracket that opens the one closing
// at index i, or -1.
func (c *cursor) matching(i int) int {
	depth := 0
	for j := i; j >= 0; j-- {
		switch c.toks[j].Kind {
		case token.RParen, token.RBracket, token.RBrace:
			depth++
		case token.LParen, token.LBracket, token.LBrace:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// items classifies a cursor in `use path.{`, recording the path and the
// names listed.
func (c *cursor) items(top *frame) {
	if c.last().Kind == token.KwAs {
		return // naming the alias
	}
	stmt := top.open
	for stmt > 0 && c.toks[stmt].Kind != token.KwUse {
		stmt--
	}
	if stmt+1 < top.open-1 {
		c.path = string(c.src[c.toks[stmt+1].Pos.Offset:c.toks[top.open-2].End.Offset])
	}
	for i := top.open + 1; i < len(c.toks); i++ {
		if c.toks[i].Kind == token.Ident && c.toks[i-1].Kind != token.KwAs {
			c.given = append(c.given, c.toks[i].Text)
		}
	}
	c.place = atUseItem
}

// inCall classifies a cursor inside a constructor's or invocation's
// parentheses: an argument's name, a reason, or an argument's value.
func (c *cursor) inCall(top *frame) {
	c.call = top.name
	depth := 0
	for i := top.open + 1; i < len(c.toks); i++ {
		t := c.toks[i]
		switch t.Kind {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
		case token.Colon:
			if depth == 0 && i > top.open+1 {
				c.arg = c.toks[i-1].Text
				c.args = append(c.args, c.arg)
			}
		case token.Comma:
			if depth == 0 {
				c.arg = ""
			}
		}
	}
	prev := c.last()
	switch {
	case c.lastIndex() == top.open || prev.Kind == token.Comma && c.depthAfter(top.open) == 0:
		c.place = atArgName
	case c.arg == reasonArg && prev.Kind == token.Colon:
		c.place = atReason
	case c.arg != "" && c.arg != reasonArg:
		c.expr(top.open + 1)
	}
}

// depthAfter returns the bracket depth at the cursor, counted from the
// bracket at index open.
func (c *cursor) depthAfter(open int) int {
	depth := 0
	for _, t := range c.toks[open+1:] {
		switch t.Kind {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
		}
	}
	return depth
}

// expr classifies a cursor in an expression that starts at token from:
// an operand, or after one an operator, and the quantifiers around it. For
// the operand after `==` or `!=`, it records the one before, whose type
// the completions follow.
func (c *cursor) expr(from int) {
	c.binders = c.bindersAt(from)
	last := c.lastIndex()
	if endsOperand(c.last()) && last >= from {
		c.place = atOperator
		return
	}
	c.place = atExpr
	if k := c.last().Kind; (k == token.Eq || k == token.NotEq) && last > from {
		if first := c.operandStart(last - 1); first >= from {
			c.left = [2]int{first, last - 1}
		}
	}
}

// stmtOf returns where the expression the cursor is in starts, for a
// cursor in brackets inside a statement: the statement of the innermost
// statement-level frame.
func (c *cursor) stmtOf(frames []frame) int {
	for _, f := range slices.Backward(frames) {
		if level(f.kind) {
			if f.stmt >= 0 {
				return f.stmt
			}
			return f.open
		}
		if f.kind == frameCall || f.kind == frameAssert {
			return f.open + 1
		}
	}
	return 0
}

// inAssert reports whether the cursor is inside an assert's parentheses,
// however deep.
func (c *cursor) inAssert(frames []frame) bool {
	for i := len(frames) - 1; i >= 0 && !level(frames[i].kind); i-- {
		if frames[i].kind == frameAssert {
			return true
		}
	}
	return false
}

// bindersAt returns the quantifiers and filters, among the tokens from
// index from to the cursor, whose body the cursor is in: `any x in xs:`
// with the cursor after the colon, before a bracket or a `,` ends the
// body.
func (c *cursor) bindersAt(from int) []binder {
	var open []binder
	depth := 0
	for i := max(from, 0); i < len(c.toks); i++ {
		t := c.toks[i]
		switch t.Kind {
		case token.KwAny, token.KwAll, token.KwFilter:
			if i+2 < len(c.toks) && c.toks[i+1].Kind == token.Ident && c.toks[i+2].Kind == token.KwIn {
				open = append(open, binder{name: c.toks[i+1].Text, from: i + 3, colon: -1, depth: depth, filter: t.Kind == token.KwFilter})
			}
		case token.Colon:
			setColon(open, depth, i)
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
			open = closeBinders(open, func(b binder) bool { return b.depth > depth })
		case token.Comma:
			open = closeBinders(open, func(b binder) bool { return b.depth == depth && b.colon >= 0 })
		}
	}
	return closeBinders(open, func(b binder) bool { return b.colon < 0 })
}

// setColon gives the innermost binder at depth still waiting for its `:`
// the one at index i.
func setColon(open []binder, depth, i int) {
	for j := len(open) - 1; j >= 0; j-- {
		if open[j].colon < 0 && open[j].depth == depth {
			open[j].colon = i
			return
		}
	}
}

// closeBinders drops the binders done reports are over.
func closeBinders(open []binder, done func(binder) bool) []binder {
	out := open[:0]
	for _, b := range open {
		if !done(b) {
			out = append(out, b)
		}
	}
	return out
}
