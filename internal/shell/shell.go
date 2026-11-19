// Package shell parses Bash source far enough to tell the commands it runs:
// every simple command, with its name, arguments, assignments and redirects,
// wherever it stands — in a pipeline or an && / || list, a subshell, a group,
// a function body or a compound command, or nested in a command, process or
// arithmetic substitution, a parameter expansion, an array, or the body of an
// unquoted here-document.
//
// It reads the grammar bash reads, as mvdan.cc/sh's syntax package lexes it,
// without building a syntax tree, and it expands nothing: a word whose value
// depends on an expansion has Word.Literal false, so that callers can tell
// "rm" from "$cmd" or "r*".
//
// Commands that run other commands, such as sudo, env, xargs, eval or
// bash -c, are reported under their own name, with the command they run
// among their arguments. Aliases and functions are not resolved: a call of a
// function is reported under the function's name, and the commands of its
// body where it is defined.
package shell

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxDepth is how deeply lists may nest, in substitutions, subshells and
// compound commands, before Parse fails.
const MaxDepth = 1000

// Word is a word of a command, as written and after quote removal.
type Word struct {
	// Raw is the word as written.
	Raw string
	// Value is the word after quote removal, with its expansions left as
	// written: "$HOME"/b'i'n reads $HOME/bin. It is what the word stands
	// for only when Literal is true.
	Value string
	// Literal reports whether the word stands for Value alone: it holds no
	// parameter, command, arithmetic or process substitution, and no glob,
	// brace or tilde expansion that bash would apply to it.
	Literal bool
	// Quoted reports whether any part of the word is quoted or escaped.
	Quoted bool
	// Offset is the byte offset of the word in the source.
	Offset int
}

// Redirect is a redirect of a simple command.
type Redirect struct {
	// N is the file descriptor written before the operator, such as "2"
	// or "{fd}", or "" for the operator's default.
	N string
	// Op is the operator, such as ">", ">>", "<", "<<", "<<<", ">&" or "&>".
	Op string
	// Target is the word after the operator: a file, a file descriptor, a
	// here-document's delimiter or a here-string.
	Target Word
	// Offset is the byte offset of the redirect in the source.
	Offset int
}

// Command is a simple command: a name and its arguments, with the
// assignments before them and the redirects among them.
type Command struct {
	// Words are the command's name and arguments; there is at least one.
	Words []Word
	// Assigns are the NAME=value words before the name.
	Assigns []Word
	// Redirects are the command's redirects, in order.
	Redirects []Redirect
}

// Name returns the word that names the command.
func (c Command) Name() Word { return c.Words[0] }

// Args returns the command's arguments, sharing storage with Words.
func (c Command) Args() []Word { return c.Words[1:] }

// Offset returns the byte offset of the command's name in the source.
func (c Command) Offset() int { return c.Words[0].Offset }

// SyntaxError is source that bash would not run.
type SyntaxError struct {
	// Offset is the byte offset of the error in the source.
	Offset int
	// Line and Col are the 1-based line and byte column of Offset.
	Line, Col int
	// Msg describes the error.
	Msg string
}

// Error returns the error as "line:col: msg".
func (e *SyntaxError) Error() string { return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg) }

// Parse reads src as Bash and returns the simple commands it holds, in a new
// slice ordered by the offsets of their names. On a syntax error it returns
// the commands read before the error, and a *SyntaxError.
func Parse(src string) ([]Command, error) {
	var cmds []Command
	p := &Parser{Src: src, Commands: &cmds}
	err := p.Run(p.ParseScript)
	slices.SortStableFunc(cmds, func(a, b Command) int { return cmp.Compare(a.Offset(), b.Offset()) })
	return cmds, err
}

// Run calls parse, one of the parser's Parse methods, and returns the
// *SyntaxError it fails with, with its line and column in Src, or nil.
func (p *Parser) Run(parse func()) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		se, ok := r.(*SyntaxError)
		if !ok {
			panic(r)
		}
		se.Line = 1 + strings.Count(p.Src[:se.Offset], "\n")
		se.Col = se.Offset - strings.LastIndexByte(p.Src[:se.Offset], '\n')
		err = se
	}()
	parse()
	return nil
}

// Parser reads Bash source. Parse sets one up for its source; one for
// a nested source, such as a backquoted command, comes from Nested.
type Parser struct {
	// Src is the source being read.
	Src string
	// Pos is the offset in Src of the next byte to read.
	Pos int
	// Origin maps each offset in Src, and the one past its end, to an offset
	// in the source Parse was given. It is nil when Src is that source.
	Origin []int
	// Commands collects the simple commands read, shared with the parsers
	// of nested sources.
	Commands *[]Command
	// Heredocs are the here-documents whose bodies start after the next
	// newline.
	Heredocs []Heredoc
	// Depth is how deeply the list being read nests.
	Depth int
	// Out, when set, is written the outline of what is read, as Outline
	// gives it. The parsers of nested sources write none.
	Out *OutlineBuilder
}

// Heredoc is a here-document whose body is yet to be read.
type Heredoc struct {
	// Delim is the line that ends the body.
	Delim string
	// Quoted reports whether the delimiter was quoted, which leaves the
	// body unexpanded.
	Quoted bool
	// StripTabs reports whether the operator was <<-, which strips leading
	// tabs from the body's lines and its delimiter line.
	StripTabs bool
}

// Checkpoint is a state of a Parser to go back to.
type Checkpoint struct {
	// Pos is the parser's Pos.
	Pos int
	// Commands is how many commands had been read.
	Commands int
	// Heredocs is how many here-documents were pending.
	Heredocs int
}

// Checkpoint returns the parser's state, for Restore.
func (p *Parser) Checkpoint() Checkpoint {
	return Checkpoint{Pos: p.Pos, Commands: len(*p.Commands), Heredocs: len(p.Heredocs)}
}

// Restore goes back to the state c, forgetting what was read since.
func (p *Parser) Restore(c Checkpoint) {
	p.Pos = c.Pos
	*p.Commands = (*p.Commands)[:c.Commands]
	p.Heredocs = p.Heredocs[:c.Heredocs]
}

// Nested returns a parser of src, which stands for the bytes of p.Src
// at origin, offsets into p.Src that number one more than src's bytes.
func (p *Parser) Nested(src string, origin []int) *Parser {
	for i, o := range origin {
		origin[i] = p.Offset(o)
	}
	return &Parser{Src: src, Origin: origin, Commands: p.Commands, Depth: p.Depth}
}

// Offset returns the offset in the source Parse was given of offset i in
// Src.
func (p *Parser) Offset(i int) int {
	if p.Origin == nil {
		return i
	}
	return p.Origin[min(i, len(p.Origin)-1)]
}

// Fail stops parsing with a *SyntaxError at offset at of Src.
func (p *Parser) Fail(at int, format string, args ...any) {
	panic(&SyntaxError{Offset: p.Offset(at), Msg: fmt.Sprintf(format, args...)})
}

// EOF reports whether all of Src has been read.
func (p *Parser) EOF() bool { return p.Pos >= len(p.Src) }

// HasPrefix reports whether the unread source starts with s.
func (p *Parser) HasPrefix(s string) bool { return strings.HasPrefix(p.Src[p.Pos:], s) }

// Peek returns the byte k bytes past Pos, or 0 past the end of Src.
func (p *Parser) Peek(k int) byte {
	if i := p.Pos + k; i < len(p.Src) {
		return p.Src[i]
	}
	return 0
}

// SkipBlanks skips spaces, tabs, escaped newlines and a comment, but no
// newline.
func (p *Parser) SkipBlanks() {
	for !p.EOF() {
		switch c := p.Src[p.Pos]; {
		case c == ' ' || c == '\t' || c == '\r':
			p.Pos++
		case c == '\\' && p.Peek(1) == '\n':
			p.Pos += 2
		case c == '#':
			if i := strings.IndexByte(p.Src[p.Pos:], '\n'); i >= 0 {
				p.Pos += i
			} else {
				p.Pos = len(p.Src)
			}
		default:
			return
		}
	}
}

// SkipLinebreaks skips blanks and newlines, reading the bodies of the
// here-documents each newline ends the line of.
func (p *Parser) SkipLinebreaks() {
	for p.SkipBlanks(); p.HasPrefix("\n"); p.SkipBlanks() {
		p.Newline()
	}
}

// Newline reads the newline at Pos and the bodies of the pending
// here-documents after it.
func (p *Parser) Newline() {
	p.Pos++
	heredocs := p.Heredocs
	p.Heredocs = nil
	for _, h := range heredocs {
		p.ReadHeredoc(h)
	}
}

// ReadHeredoc reads the body of h, from Pos to its delimiter line or the
// end of Src, as bash does, and the commands substituted in it unless its
// delimiter was quoted.
func (p *Parser) ReadHeredoc(h Heredoc) {
	start, end := p.Pos, len(p.Src)
	for !p.EOF() {
		line, _, found := strings.Cut(p.Src[p.Pos:], "\n")
		next := p.Pos + len(line) + 1
		if h.StripTabs {
			line = strings.TrimLeft(line, "\t")
		}
		if line == h.Delim {
			end = p.Pos
			p.Pos = min(next, len(p.Src))
			break
		}
		if !found {
			next = len(p.Src)
		}
		p.Pos = next
	}
	if h.Quoted || start == end {
		return
	}
	origin := make([]int, end-start+1)
	for i := range origin {
		origin[i] = start + i
	}
	p.Nested(p.Src[start:end], origin).ScanHeredocBody()
}

// isMeta reports whether c ends an unquoted word.
func isMeta(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '|', '&', ';', '(', ')', '<', '>':
		return true
	}
	return false
}

// reserved holds bash's reserved words.
var reserved = map[string]bool{
	"!": true, "{": true, "}": true, "[[": true, "]]": true,
	"case": true, "coproc": true, "do": true, "done": true, "elif": true,
	"else": true, "esac": true, "fi": true, "for": true, "function": true,
	"if": true, "in": true, "select": true, "then": true, "time": true,
	"until": true, "while": true,
}

// Reserved returns the reserved word that the unread source starts with, as
// a whole unquoted word, or "".
func (p *Parser) Reserved() string {
	i := p.Pos
	for i < len(p.Src) && !isMeta(p.Src[i]) && !strings.ContainsRune("'\"`$\\", rune(p.Src[i])) {
		i++
	}
	if i < len(p.Src) && !isMeta(p.Src[i]) {
		return ""
	}
	if w := p.Src[p.Pos:i]; reserved[w] {
		return w
	}
	return ""
}

// WordIs reports whether the unread source starts with the unquoted word w.
func (p *Parser) WordIs(w string) bool {
	return p.HasPrefix(w) && (p.Pos+len(w) == len(p.Src) || isMeta(p.Src[p.Pos+len(w)]))
}

// operators are bash's control operators, longest first.
var operators = []string{";;&", ";;", ";&", "&&", "||", "|&", ";", "&", "|", "(", ")", "\n"}

// Operator returns the control operator that the unread source starts with,
// or "".
func (p *Parser) Operator() string {
	if p.HasPrefix("&>") {
		return ""
	}
	for _, op := range operators {
		if p.HasPrefix(op) {
			return op
		}
	}
	return ""
}

// redirectOps are bash's redirect operators, longest first.
var redirectOps = []string{"&>>", "&>", "<<<", "<<-", "<<", "<>", "<&", ">>", ">&", ">|", "<", ">"}

// RedirectOp returns the redirect that the unread source starts with, as the
// file descriptor written before it and its operator, or "", "".
func (p *Parser) RedirectOp() (n, op string) {
	i := p.Pos
	j := i
	for j < len(p.Src) && p.Src[j] >= '0' && p.Src[j] <= '9' {
		j++
	}
	if j == i && j < len(p.Src) && p.Src[j] == '{' {
		if k := strings.IndexByte(p.Src[j:], '}'); k > 1 && validName(p.Src[j+1:j+k]) {
			j += k + 1
		}
	}
	if j > i && j < len(p.Src) && (p.Src[j] == '<' || p.Src[j] == '>') {
		n, i = p.Src[i:j], j
	}
	for _, op := range redirectOps {
		if !strings.HasPrefix(p.Src[i:], op) {
			continue
		}
		if len(op) == 1 && i+1 < len(p.Src) && p.Src[i+1] == '(' {
			return "", "" // a process substitution
		}
		return n, op
	}
	return "", ""
}

// AtWord reports whether a word starts at Pos.
func (p *Parser) AtWord() bool {
	if p.EOF() {
		return false
	}
	c := p.Src[p.Pos]
	return !isMeta(c) || (c == '<' || c == '>') && p.Peek(1) == '('
}

// Describe names the token at Pos, for an error.
func (p *Parser) Describe() string {
	if p.EOF() {
		return "end of input"
	}
	if op := p.Operator(); op == "\n" {
		return "newline"
	} else if op != "" {
		return strconv.Quote(op)
	}
	if w := p.Reserved(); w != "" {
		return strconv.Quote(w)
	}
	if n, op := p.RedirectOp(); op != "" {
		return strconv.Quote(n + op)
	}
	r, _ := utf8.DecodeRuneInString(p.Src[p.Pos:])
	return strconv.QuoteRune(r)
}

// Expect reads the reserved word w that closes the construct opener opened
// at offset open, and fails when it is not there.
func (p *Parser) Expect(w string, open int, opener string) {
	if p.Reserved() == w {
		p.Pos += len(w)
		return
	}
	if p.EOF() {
		p.Fail(open, "%q without %q", opener, w)
	}
	p.Fail(p.Pos, "unexpected %s, want %q", p.Describe(), w)
}

// Close reads the operator op that closes the construct what opened at
// offset open, and fails when it is not there.
func (p *Parser) Close(op string, open int, what string) {
	if p.HasPrefix(op) {
		p.Pos += len(op)
		return
	}
	if p.EOF() {
		p.Fail(open, "unclosed %s", what)
	}
	p.Fail(p.Pos, "unexpected %s in %s", p.Describe(), what)
}

// ParseScript reads all of Src as a list of commands.
func (p *Parser) ParseScript() {
	p.ParseList()
	if !p.EOF() {
		p.Fail(p.Pos, "unexpected %s", p.Describe())
	}
}

// AtListEnd reports whether a list ends at Pos: at the end of Src, a ")",
// a case item's terminator, or one of the reserved words stops.
func (p *Parser) AtListEnd(stops []string) bool {
	if p.EOF() {
		return true
	}
	switch p.Operator() {
	case ")", ";;", ";&", ";;&":
		return true
	}
	w := p.Reserved()
	return w != "" && slices.Contains(stops, w)
}

// ParseList reads and-or lists separated by ";", "&" or newlines, up to the
// end of the list (see AtListEnd).
func (p *Parser) ParseList(stops ...string) {
	if p.Depth++; p.Depth > MaxDepth {
		p.Fail(p.Pos, "nesting deeper than %d", MaxDepth)
	}
	defer func() { p.Depth-- }()
	for {
		p.SkipLinebreaks()
		if p.AtListEnd(stops) {
			return
		}
		p.ParseAndOr()
		p.SkipBlanks()
		switch op := p.Operator(); op {
		case ";", "&":
			p.Emit(func(o *OutlineBuilder) { o.Separator(op) })
			p.Pos++
			continue
		case "\n":
			p.Emit(func(o *OutlineBuilder) { o.Separator(op) })
			continue
		}
		if p.AtListEnd(stops) {
			return
		}
		p.Fail(p.Pos, "unexpected %s", p.Describe())
	}
}

// ParseAndOr reads pipelines joined by && or ||.
func (p *Parser) ParseAndOr() {
	for p.ParsePipeline(); ; p.ParsePipeline() {
		p.SkipBlanks()
		op := p.Operator()
		if op != "&&" && op != "||" {
			return
		}
		p.Emit(func(o *OutlineBuilder) { o.Separator(op) })
		p.Pos += 2
		p.SkipLinebreaks()
	}
}

// ParsePipeline reads commands joined by | or |&, after any time [-p] and !.
func (p *Parser) ParsePipeline() {
	p.SkipBlanks()
	if p.Reserved() == "time" {
		p.Pos += len("time")
		p.Emit(func(o *OutlineBuilder) { o.Keyword("time") })
		if p.SkipBlanks(); p.WordIs("-p") {
			p.Pos += len("-p")
		}
		if p.SkipBlanks(); p.AtListEnd(nil) || p.Operator() != "" && p.Operator() != "(" {
			p.Emit(func(o *OutlineBuilder) { o.Opener = false })
			return // time alone times nothing
		}
	}
	for p.Reserved() == "!" {
		p.Pos++
		p.Emit(func(o *OutlineBuilder) { o.Keyword("!") })
		p.SkipBlanks()
	}
	for p.ParseCommand(); ; p.ParseCommand() {
		p.SkipBlanks()
		op := p.Operator()
		if op != "|" && op != "|&" {
			return
		}
		p.Emit(func(o *OutlineBuilder) { o.Separator(op) })
		p.Pos += len(op)
		p.SkipLinebreaks()
	}
}

// IsCompoundStart reports whether a compound command or a function
// definition by the function keyword starts at Pos.
func (p *Parser) IsCompoundStart() bool {
	switch p.Reserved() {
	case "{", "if", "while", "until", "for", "select", "case", "[[", "function":
		return true
	}
	return p.Operator() == "("
}

// ParseCommand reads a simple command, a compound command with its
// redirects, or a function definition.
func (p *Parser) ParseCommand() {
	p.SkipBlanks()
	open := p.Pos
	switch w := p.Reserved(); w {
	case "{":
		p.Pos++
		p.ParseBody("{", "}", open, "{")
	case "if":
		p.ParseIf()
	case "while", "until":
		p.Pos += len(w)
		p.ParseBody(w, "do", open, w)
		p.ParseBody("do", "done", open, w)
	case "for", "select":
		p.ParseFor(w)
	case "case":
		p.ParseCase()
	case "[[":
		p.ParseTest()
	case "function":
		p.ParseFunction()
	case "coproc":
		p.ParseCoproc()
	case "then", "elif", "else", "fi", "do", "done", "esac", "}", "]]":
		p.Fail(p.Pos, "unexpected %q", w)
	default:
		if p.Operator() != "(" {
			p.ParseSimple()
			return
		}
		if p.Peek(1) == '(' {
			c := p.Checkpoint()
			p.Pos += 2
			if p.ScanArith("))") {
				p.Emit(func(o *OutlineBuilder) { o.Word("((" + Ellipsis + "))") })
				break
			}
			p.Restore(c) // not arithmetic but a subshell in a subshell
		}
		p.Pos++
		p.Emit(func(o *OutlineBuilder) { o.Open("(") })
		p.ParseList()
		p.Close(")", open, "subshell")
		p.Emit(func(o *OutlineBuilder) { o.Close(")") })
	}
	for p.SkipBlanks(); ; p.SkipBlanks() {
		n, op := p.RedirectOp()
		if op == "" {
			return
		}
		p.ParseRedirect(n, op)
	}
}

// ParseSimple reads a simple command, or a function definition by a name
// and (), and adds the command to Commands when it has a name.
func (p *Parser) ParseSimple() {
	start := p.Pos
	var c Command
	for {
		p.SkipBlanks()
		if n, op := p.RedirectOp(); op != "" {
			c.Redirects = append(c.Redirects, p.ParseRedirect(n, op))
			continue
		}
		if !p.AtWord() {
			break
		}
		mode := WordArg
		if len(c.Words) == 0 {
			mode = WordCommand
		}
		w, _ := p.ScanWord(mode)
		if len(c.Words) == 0 && isAssignment(w.Raw) {
			c.Assigns = append(c.Assigns, w)
			continue
		}
		if len(c.Words) == 0 && len(c.Assigns) == 0 && len(c.Redirects) == 0 {
			if p.SkipBlanks(); p.HasPrefix("(") {
				p.Pos++
				p.SkipBlanks()
				p.Close(")", p.Pos, "function definition")
				p.Emit(func(o *OutlineBuilder) { o.Word(w.Raw + "()") })
				p.ParseFunctionBody()
				return
			}
		}
		c.Words = append(c.Words, w)
	}
	if p.Pos == start {
		p.Fail(p.Pos, "unexpected %s", p.Describe())
	}
	p.Emit(func(o *OutlineBuilder) { o.Word(o.Name(c)) })
	if len(c.Words) > 0 {
		*p.Commands = append(*p.Commands, c)
	}
}

// ParseRedirect reads the redirect at Pos, with the file descriptor n and
// operator op that RedirectOp returned, and queues a here-document's body.
func (p *Parser) ParseRedirect(n, op string) Redirect {
	r := Redirect{N: n, Op: op, Offset: p.Offset(p.Pos)}
	p.Pos += len(n) + len(op)
	p.SkipBlanks()
	if !p.AtWord() {
		p.Fail(p.Pos, "unexpected %s after %q", p.Describe(), op)
	}
	// Digits before < or > are a file descriptor, which bash takes as the
	// target of a duplication but of no other redirect: 2>&1>x, not >1>x.
	if n, _ := p.RedirectOp(); n != "" && (op != ">&" && op != "<&" || !isDigit(n[0])) {
		p.Fail(p.Pos, "unexpected %q after %q", n, op)
	}
	if op != "<<" && op != "<<-" {
		r.Target, _ = p.ScanWord(WordArg)
		return r
	}
	// A here-document's delimiter is not expanded.
	c := p.Checkpoint()
	r.Target, _ = p.ScanWord(WordArg)
	*p.Commands = (*p.Commands)[:c.Commands]
	p.Heredocs = append(p.Heredocs, Heredoc{Delim: r.Target.Value, Quoted: r.Target.Quoted, StripTabs: op == "<<-"})
	return r
}

// ParseBody writes kw, the reserved word just read that starts a list, to
// the outline, then reads the list up to the reserved word end, or one of
// more stops, and then end itself, which closes the construct opener opened
// at offset open.
func (p *Parser) ParseBody(kw, end string, open int, opener string, more ...string) {
	p.Emit(func(o *OutlineBuilder) { o.Keyword(kw) })
	p.ParseList(append(more, end)...)
	if slices.Contains(more, p.Reserved()) {
		return
	}
	p.Expect(end, open, opener)
	if end == "then" || end == "do" {
		return // the next body writes it
	}
	p.Emit(func(o *OutlineBuilder) { o.Word(end) })
}

// ParseIf reads an if clause.
func (p *Parser) ParseIf() {
	open := p.Pos
	p.Pos += len("if")
	p.ParseBody("if", "then", open, "if")
	p.ParseBody("then", "fi", open, "if", "elif", "else")
	for p.Reserved() == "elif" {
		p.Pos += len("elif")
		p.ParseBody("elif", "then", open, "elif")
		p.ParseBody("then", "fi", open, "if", "elif", "else")
	}
	if p.Reserved() == "else" {
		p.Pos += len("else")
		p.ParseBody("else", "fi", open, "if")
	}
}

// ParseFor reads a for or select loop, as kw tells, over words or, for for,
// by an arithmetic (( ; ; )).
func (p *Parser) ParseFor(kw string) {
	open := p.Pos
	p.Pos += len(kw)
	p.SkipBlanks()
	p.Emit(func(o *OutlineBuilder) { o.Word(kw) })
	if kw == "for" && p.HasPrefix("((") {
		p.Pos += 2
		if !p.ScanArith("))") {
			p.Fail(open, "unclosed %q", "for ((")
		}
		p.Emit(func(o *OutlineBuilder) { o.Word("((" + Ellipsis + "))") })
	} else {
		if !p.AtWord() {
			p.Fail(p.Pos, "unexpected %s after %q", p.Describe(), kw)
		}
		name, _ := p.ScanWord(WordArg)
		p.Emit(func(o *OutlineBuilder) { o.Word(name.Raw) })
		p.SkipBlanks()
		if p.Operator() == ";" {
			p.Pos++
		} else if p.SkipLinebreaks(); p.Reserved() == "in" {
			p.Pos += len("in")
			p.Emit(func(o *OutlineBuilder) { o.Word("in") })
			for p.SkipBlanks(); p.AtWord(); p.SkipBlanks() {
				p.ScanWord(WordArg)
			}
			p.Emit(func(o *OutlineBuilder) { o.Word(Ellipsis) })
			switch p.Operator() {
			case ";":
				p.Pos++
			case "\n":
			default:
				if !p.EOF() {
					p.Fail(p.Pos, "unexpected %s in %q", p.Describe(), kw)
				}
			}
		}
	}
	if p.SkipBlanks(); p.Operator() == ";" {
		p.Pos++
	}
	p.SkipLinebreaks()
	switch p.Reserved() {
	case "do":
		p.Emit(func(o *OutlineBuilder) { o.Separator(";") })
		p.Pos += len("do")
		p.ParseBody("do", "done", open, kw)
	case "{":
		p.Pos++
		p.ParseBody("{", "}", open, kw)
	default:
		p.Expect("do", open, kw)
	}
}

// ParseCase reads a case clause.
func (p *Parser) ParseCase() {
	open := p.Pos
	p.Pos += len("case")
	p.SkipBlanks()
	if !p.AtWord() {
		p.Fail(p.Pos, "unexpected %s after %q", p.Describe(), "case")
	}
	p.ScanWord(WordArg)
	p.SkipLinebreaks()
	p.Expect("in", open, "case")
	p.Emit(func(o *OutlineBuilder) { o.Word("case " + Ellipsis + " in") })
	for {
		p.SkipLinebreaks()
		if p.Reserved() == "esac" {
			p.Pos += len("esac")
			p.Emit(func(o *OutlineBuilder) { o.Word("esac") })
			return
		}
		if p.EOF() {
			p.Fail(open, "%q without %q", "case", "esac")
		}
		if p.Operator() == "(" {
			p.Pos++
		}
		var patterns []string
		for {
			p.SkipBlanks()
			if !p.AtWord() {
				p.Fail(p.Pos, "unexpected %s in case pattern", p.Describe())
			}
			w, _ := p.ScanWord(WordArg)
			patterns = append(patterns, w.Raw)
			if p.SkipBlanks(); p.Operator() != "|" {
				break
			}
			p.Pos++
		}
		p.Close(")", open, "case pattern")
		p.Emit(func(o *OutlineBuilder) { o.Keyword(strings.Join(patterns, "|") + ")") })
		p.ParseList("esac")
		switch op := p.Operator(); op {
		case ";;", ";&", ";;&":
			p.Emit(func(o *OutlineBuilder) { o.Separator(op) })
			p.Pos += len(op)
		default:
			p.Expect("esac", open, "case")
			p.Emit(func(o *OutlineBuilder) { o.Word("esac") })
			return
		}
	}
}

// ParseTest reads a [[ ]] test.
func (p *Parser) ParseTest() {
	open := p.Pos
	p.Pos += len("[[")
	mode := WordArg
	for {
		p.SkipBlanks()
		switch {
		case p.EOF():
			p.Fail(open, "%q without %q", "[[", "]]")
		case p.HasPrefix("\n"):
			p.Newline()
		case p.Reserved() == "]]":
			p.Pos += len("]]")
			p.Emit(func(o *OutlineBuilder) { o.Word("[[ " + Ellipsis + " ]]") })
			return
		case p.HasPrefix("&&"), p.HasPrefix("||"):
			p.Pos += 2
		case strings.IndexByte("()<>", p.Src[p.Pos]) >= 0 && mode != WordRegex, p.Reserved() == "!":
			p.Pos++
		default:
			w, ok := p.ScanWord(mode)
			if !ok {
				p.Fail(p.Pos, "unexpected %s in %q", p.Describe(), "[[")
			}
			mode = WordArg
			if w.Raw == "=~" {
				mode = WordRegex
			}
			continue
		}
		mode = WordArg
	}
}

// ParseFunction reads a function definition by the function keyword.
func (p *Parser) ParseFunction() {
	p.Pos += len("function")
	p.SkipBlanks()
	if !p.AtWord() {
		p.Fail(p.Pos, "unexpected %s after %q", p.Describe(), "function")
	}
	name, _ := p.ScanWord(WordArg)
	p.Emit(func(o *OutlineBuilder) { o.Word(name.Raw + "()") })
	if p.SkipBlanks(); p.HasPrefix("(") {
		p.Pos++
		p.SkipBlanks()
		p.Close(")", p.Pos, "function definition")
	}
	p.ParseFunctionBody()
}

// ParseFunctionBody reads the body of a function definition, after any
// newlines: a compound command, with its redirects.
func (p *Parser) ParseFunctionBody() {
	p.SkipLinebreaks()
	if !p.IsCompoundStart() || p.Reserved() == "function" {
		p.Fail(p.Pos, "unexpected %s, want a compound command", p.Describe())
	}
	p.ParseCommand()
}

// ParseCoproc reads a coprocess: a command, after a name when the command
// is compound.
func (p *Parser) ParseCoproc() {
	p.Pos += len("coproc")
	p.Emit(func(o *OutlineBuilder) { o.Keyword("coproc") })
	p.SkipBlanks()
	if !p.IsCompoundStart() && p.AtWord() {
		c := p.Checkpoint()
		name, _ := p.ScanWord(WordArg)
		if p.SkipBlanks(); !p.IsCompoundStart() {
			p.Restore(c) // no name: the word starts a simple command
		} else {
			p.Emit(func(o *OutlineBuilder) { o.Keyword(name.Raw) })
		}
	}
	p.ParseCommand()
}

// isAssignment reports whether the word raw is an assignment: a name, an
// optional [subscript], and = or +=.
func isAssignment(raw string) bool {
	i := 0
	for i < len(raw) && (raw[i] == '_' || isAlpha(raw[i]) || i > 0 && isDigit(raw[i])) {
		i++
	}
	if i == 0 {
		return false
	}
	if i < len(raw) && raw[i] == '[' {
		j := strings.IndexByte(raw[i:], ']')
		if j < 0 {
			return false
		}
		i += j + 1
	}
	if i < len(raw) && raw[i] == '+' {
		i++
	}
	return i < len(raw) && raw[i] == '='
}

// validName reports whether s is a shell variable name.
func validName(s string) bool {
	for i := range len(s) {
		if c := s[i]; c != '_' && !isAlpha(c) && (i == 0 || !isDigit(c)) {
			return false
		}
	}
	return s != ""
}

func isAlpha(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
