package shell

import "strings"

// Ellipsis stands, in an outline, for the words a construct holds, such as
// a for loop's list or a test's expression.
const Ellipsis = "…"

// Outline returns src on one line with each simple command cut down to its
// name, as written, keeping the operators and compound commands between
// them. Arguments, redirects, comments, here-document bodies and the
// commands substituted in words are left out:
//
//	cd calc && go test ./... | grep -v ok; go vet ./...
//
// reads "cd && go | grep; go". A command with no name shows its first
// assignment's name, as "X=", or else its first redirect, as ">". Words a
// construct holds show as Ellipsis, as in "for f in …; do gofmt; done" and
// "[[ … ]] || exit".
//
// A wrapper, a command that runs a command it is given, as Wrappers,
// Shells, eval and find -exec do, shows its name followed by the outline of
// that command, without the wrapper's options: "sudo -E env A=1 go test"
// reads "sudo env go", and "bash -c 'cd x && make'" reads
// "bash -c 'cd && make'", a script in quotes when it holds more than one
// command. A script that is not literal, or would not parse, shows as
// Ellipsis.
//
// On a syntax error Outline returns "" and the error.
func Outline(src string) (string, error) {
	o := &OutlineBuilder{}
	if err := o.Read(src); err != nil {
		return "", err
	}
	return o.String(), nil
}

// OutlineBuilder writes an outline, as Outline describes it, from the
// tokens a Parser gives it. Tokens are a space apart, but for a separator
// pending before the next one and the tokens that Open and Close glue.
type OutlineBuilder struct {
	// Depth is how many wrappers deep the outline is, as the outline of a
	// script that bash -c runs is one deeper than the command's.
	Depth int
	// Compound reports whether the outline has more than a single simple
	// command: a separator or a compound command.
	Compound bool
	// B holds the outline so far.
	B strings.Builder
	// Sep is the separator to write before the next token, such as "; " or
	// " && ", or "" for a space.
	Sep string
	// Glue reports whether the next token follows the last with no space,
	// after "(".
	Glue bool
	// Opener reports whether the last token starts a list, such as "then",
	// "do" or "{", after which a ";" or newline is left out.
	Opener bool
}

// Word writes the token s.
func (o *OutlineBuilder) Word(s string) {
	if o.B.Len() > 0 {
		switch {
		case o.Sep != "":
			o.B.WriteString(o.Sep)
			o.Compound = true
		case !o.Glue:
			o.B.WriteByte(' ')
		}
	}
	o.B.WriteString(strings.ReplaceAll(s, "\n", " "))
	o.Sep, o.Glue, o.Opener = "", false, false
}

// Keyword writes the token s, which starts a list.
func (o *OutlineBuilder) Keyword(s string) {
	o.Word(s)
	o.Opener, o.Compound = true, true
}

// Open writes s, "(", which the next token follows with no space.
func (o *OutlineBuilder) Open(s string) {
	o.Keyword(s)
	o.Glue = true
}

// Close writes s, ")", right after the last token, dropping any pending
// separator.
func (o *OutlineBuilder) Close(s string) {
	o.Sep, o.Glue = "", true
	o.Word(s)
}

// Separator writes op, a control operator, before the next token: ";",
// "\n", "&", "&&", "||", "|", "|&", or a case item's ";;", ";&" or ";;&".
// A ";" or newline that ends nothing, at the start or after a token that
// starts a list, or after another separator, is left out.
func (o *OutlineBuilder) Separator(op string) {
	switch op {
	case ";;", ";&", ";;&":
		o.Sep = op + " "
		return
	}
	if o.B.Len() == 0 || o.Opener {
		return
	}
	switch op {
	case ";", "\n":
		if o.Sep == "" {
			o.Sep = "; "
		}
	case "&":
		o.Sep = " & "
	default:
		o.Sep = " " + op + " "
	}
}

// String returns the outline, ending with " &" when the source ends by
// running a command in the background.
func (o *OutlineBuilder) String() string {
	if o.Sep == " & " {
		return o.B.String() + " &"
	}
	return o.B.String()
}

// Read writes the outline of the script src, and returns the
// *SyntaxError it fails with, or nil.
func (o *OutlineBuilder) Read(src string) error {
	var cmds []Command
	p := &Parser{Src: src, Commands: &cmds, Out: o}
	return p.Run(p.ParseScript)
}

// Script returns the outline of the script src that a wrapper depth deep
// runs, quoted when it is more than one word, as "'cd && make'" or
// "'go test'", in double quotes when it holds single quotes, or Ellipsis
// when src would not parse.
func (o *OutlineBuilder) Script(src string, depth int) string {
	inner := &OutlineBuilder{Depth: depth + 1}
	if inner.Read(src) != nil {
		return Ellipsis
	}
	switch s := inner.String(); {
	case s == "":
		return "''"
	case !inner.Compound && !strings.Contains(s, " "):
		return s
	case strings.Contains(s, "'") && !strings.Contains(s, `"`):
		return `"` + s + `"`
	default:
		return "'" + s + "'"
	}
}

// Quiet stops the parser writing to its outline, as within a word, until
// the returned func is called.
func (p *Parser) Quiet() (restore func()) {
	out := p.Out
	p.Out = nil
	return func() { p.Out = out }
}

// Emit calls f with the parser's outline, when it writes one.
func (p *Parser) Emit(f func(*OutlineBuilder)) {
	if p.Out != nil {
		f(p.Out)
	}
}

// Name returns how the simple command c shows in the outline: its name, as
// written, and for a wrapper the command it runs, as Wrapped gives them;
// "[ … ]" for a [ test; or for a command with no name its first
// assignment's name, as "X=", or else its first redirect.
func (o *OutlineBuilder) Name(c Command) string {
	switch {
	case len(c.Words) > 0 && c.Words[0].Raw == "[":
		return "[ " + Ellipsis + " ]"
	case len(c.Words) > 0:
		return o.Wrapped(c.Words, o.Depth)
	case len(c.Assigns) > 0:
		name, _, _ := strings.Cut(c.Assigns[0].Raw, "=")
		return strings.TrimSuffix(name, "+") + "="
	case len(c.Redirects) > 0:
		return c.Redirects[0].N + c.Redirects[0].Op
	}
	return ""
}
