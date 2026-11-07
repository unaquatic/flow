package ui

import (
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// treeMaxRows is the most session tree rows shown at once. A short terminal
// shows fewer.
const treeMaxRows = 12

// HiddenLabel names, in the tree's status line, the forks off the
// conversation that the tree shows while ShowHidden.
const HiddenLabel = "hidden"

// treeTextLimit bounds the bytes of text a tree row keeps from a message, and
// from each tool call's input. The row is cut to fit the panel anyway.
const treeTextLimit = 200

// TreeFilter selects the messages the session tree lists.
type TreeFilter int

const (
	// TreeFilterDefault lists every message.
	TreeFilterDefault TreeFilter = iota
	// TreeFilterNoTools hides replies that only call tools.
	TreeFilterNoTools
	// TreeFilterUserOnly lists the prompts alone.
	TreeFilterUserOnly
)

// TreeFilters are the filters in the order Ctrl+O cycles through them.
var TreeFilters = []TreeFilter{TreeFilterDefault, TreeFilterNoTools, TreeFilterUserOnly}

// Next returns the filter after f in TreeFilters, wrapping to the first.
func (f TreeFilter) Next() TreeFilter {
	i := slices.Index(TreeFilters, f)
	return TreeFilters[(i+1)%len(TreeFilters)]
}

// Label names the filter in the tree's status line. The default has no label.
func (f TreeFilter) Label() string {
	switch f {
	case TreeFilterNoTools:
		return "no-tools"
	case TreeFilterUserOnly:
		return "user"
	default:
		return ""
	}
}

// TreeEntry is one message of the session tree, summarized for its row.
type TreeEntry struct {
	// ID identifies the message.
	ID message.ID
	// Parent identifies the message it continues, and is empty at a root.
	Parent message.ID
	// Role is the message's author.
	Role message.Role
	// Type is what the message is for: empty for the conversation, or the
	// type of the fork it belongs to, such as message.TypeTitle, which the
	// tree hides until ShowHidden.
	Type message.Type
	// Text is the message's text on one line, or empty when it holds none.
	Text string
	// Tools are the message's tool calls, in order.
	Tools []TreeTool
	// Prompt is the whole text of a user message, which choosing the message
	// puts in the input to edit and send again. It is empty for other
	// messages.
	Prompt string
}

// NewTreeEntry summarizes m for its row in the session tree.
func NewTreeEntry(m message.Content) TreeEntry {
	e := TreeEntry{ID: m.ID, Role: m.Role, Type: m.Type}
	if e.Type.IsChat() {
		e.Type = ""
	}
	if m.ParentID != nil {
		e.Parent = *m.ParentID
	}
	var texts []string
	for _, part := range m.Parts {
		switch p := part.(type) {
		case message.TextPart:
			if strings.TrimSpace(p.Text) != "" {
				texts = append(texts, p.Text)
			}
		case message.ToolPart:
			e.Tools = append(e.Tools, NewTreeTool(p))
		}
	}
	text := strings.Join(texts, "\n")
	e.Text = Truncate(OneLine(text), treeTextLimit)
	if m.Role == message.RoleUser {
		e.Prompt = text
	}
	return e
}

// OneLine collapses each run of whitespace in s, line breaks included, to
// one space, and trims s.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TreeTool is a tool call listed on a tree row, which draws it as
// "read: calc.go", or as "read" alone when the call has no main input.
type TreeTool struct {
	// Name is the tool's name, such as "read".
	Name string
	// Input is the call's main input on one line, such as a path or a
	// command, or empty when it has none.
	Input string
}

// NewTreeTool summarizes p for a tree row.
func NewTreeTool(p message.ToolPart) TreeTool {
	return TreeTool{
		Name:  p.Name,
		Input: Truncate(OneLine(ToolInputSummary(string(p.Input()))), treeTextLimit),
	}
}

// ToolsOnly reports whether e is a reply that holds tool calls and no text.
func (e TreeEntry) ToolsOnly() bool {
	return e.Role == message.RoleAgent && e.Text == "" && len(e.Tools) > 0
}

// Split returns the entries of e's rows: one for each tool call of a
// message of tool calls alone, each holding that call, and e itself
// otherwise.
func (e TreeEntry) Split() []TreeEntry {
	if !e.ToolsOnly() || len(e.Tools) < 2 {
		return []TreeEntry{e}
	}
	entries := make([]TreeEntry, len(e.Tools))
	for i, call := range e.Tools {
		entries[i] = e
		entries[i].Tools = []TreeTool{call}
	}
	return entries
}

// Hidden reports whether e belongs to a fork off the conversation, which the
// tree lists only while ShowHidden is set.
func (e TreeEntry) Hidden() bool { return e.Type != "" }

// Matches reports whether e's type, role, text, or tool calls contain every
// one of tokens, which are lower case.
func (e TreeEntry) Matches(tokens []string) bool {
	words := []string{string(e.Type), e.Role.String(), e.Text}
	for _, call := range e.Tools {
		words = append(words, call.Name, call.Input)
	}
	text := strings.ToLower(strings.Join(words, " "))
	return !slices.ContainsFunc(tokens, func(tok string) bool { return !strings.Contains(text, tok) })
}

// TreeRow is one row of the drawn session tree.
type TreeRow struct {
	// Entry is the message the row lists.
	Entry TreeEntry
	// Prefix holds the connectors and gutters drawn before the row's text,
	// such as "│  ├─ ".
	Prefix string
	// Active reports that the message is on the session's transcript: the
	// path from its root to the head.
	Active bool
}

// TreeGutter is a vertical line that a branch draws past the rows of its
// earlier siblings' subtrees.
type TreeGutter struct {
	// Level is the indent level the line is drawn at.
	Level int
	// Line reports that the branch has a later sibling, so the line runs on.
	// Otherwise the gutter is blank.
	Line bool
}

// TreePlacement is where a row sits in the drawn tree.
//
// A chain of single children stays at one indent, so the messages of a branch
// line up under its first one. The branches of a fork indent one level, each
// opening behind a connector; the roots of a session with many are the
// branches of a fork above them all. The asides of a message, the forks off
// the conversation that hang from it, indent one level behind connectors of
// their own, and leave a message that the conversation goes on with alone
// at its indent.
type TreePlacement struct {
	// Indent is the row's indent level, each three columns wide.
	Indent int
	// Connector reports that the row opens a branch of a fork, and is drawn
	// behind a connector at the last indent level.
	Connector bool
	// Last reports that the row is the last branch of its fork.
	Last bool
	// Gutters are the lines of the forks above the row.
	Gutters []TreeGutter
}

// TreePrefix returns the connectors and gutters drawn before a row placed at
// p.
func TreePrefix(p TreePlacement) string {
	var b strings.Builder
	for level := range p.Indent {
		i := slices.IndexFunc(p.Gutters, func(g TreeGutter) bool { return g.Level == level })
		switch {
		case i >= 0 && p.Gutters[i].Line:
			b.WriteString("│  ")
		case i >= 0:
			b.WriteString("   ")
		case p.Connector && level == p.Indent-1 && p.Last:
			b.WriteString("└─ ")
		case p.Connector && level == p.Indent-1:
			b.WriteString("├─ ")
		default:
			b.WriteString("   ")
		}
	}
	return b.String()
}

// SessionTree is the /tree view of a session's history: every branch drawn
// as a tree, with one row selected. A filter and the words of a search narrow
// the rows; a listed message is drawn under the nearest listed message it
// descends from.
type SessionTree struct {
	// Entries are the session's messages in the order they were stored.
	Entries []TreeEntry
	// Head is the message the session's transcript ends at.
	Head message.ID
	// Filter selects the messages listed.
	Filter TreeFilter
	// ShowHidden lists the messages of the forks off the conversation, such
	// as the exchanges that titled the session, which Ctrl+T toggles.
	ShowHidden bool
	// Query is the search the rows match.
	Query string
	// Rows are the rows listed, in drawing order.
	Rows []TreeRow
	// Selected is the index in Rows of the selected row.
	Selected int
	// Last is the message last selected. A search that lists nothing keeps
	// it, to select again once the search lists it or its ancestors.
	Last message.ID

	// Index maps a message to its place in Entries.
	Index map[message.ID]int
	// Order lists the indices of Entries depth first, as OrderBelow orders
	// each message's children: its forks, then the branch that holds the
	// head, then its other branches.
	Order []int
	// Active marks the messages on the transcript.
	Active map[message.ID]bool
	// RowIndex maps a listed message to its place in Rows: its first row,
	// for a message that takes one row for each of its tool calls.
	RowIndex map[message.ID]int
	// ListedParent maps a listed message to the nearest listed message it
	// descends from, and to the empty ID when there is none.
	ListedParent map[message.ID]message.ID
	// ListedChildren maps a listed message, or the empty ID for the roots,
	// to the listed messages that ListedParent maps to it, in drawing order.
	ListedChildren map[message.ID][]message.ID
}

// NewSessionTree returns the session tree of t with the head selected, or
// the nearest message above it that the tree lists. Its entries are t's
// messages and then its forks, which it hides until ShowHidden; a fork
// message whose parent t does not hold, as when the fork was read just after
// its prompt was stored, is left out.
func NewSessionTree(t app.Tree) *SessionTree {
	st := &SessionTree{
		Head:   t.Head,
		Index:  make(map[message.ID]int, len(t.Messages)+len(t.Forks)),
		Active: make(map[message.ID]bool),
		Last:   t.Head,
	}
	for _, m := range t.Messages {
		st.Index[m.ID] = len(st.Entries)
		st.Entries = append(st.Entries, NewTreeEntry(m))
	}
	for _, m := range t.Forks {
		if m.ParentID == nil {
			continue
		}
		if _, ok := st.Index[*m.ParentID]; !ok {
			continue
		}
		st.Index[m.ID] = len(st.Entries)
		st.Entries = append(st.Entries, NewTreeEntry(m))
	}
	for id := t.Head; id != ""; {
		i, ok := st.Index[id]
		if !ok {
			break
		}
		st.Active[id] = true
		id = st.Entries[i].Parent
	}
	children := make(map[message.ID][]int)
	for i, e := range st.Entries {
		children[e.Parent] = append(children[e.Parent], i)
	}
	st.Order = make([]int, 0, len(st.Entries))
	st.OrderBelow(children, "")
	st.Refresh()
	return st
}

// OrderBelow appends the subtrees below parent to Order, depth first.
// children maps each message to the indices of its children in Entries, in
// creation order. The forks off the conversation go first, so that each
// sits right under the prompt it forked from, ahead of the conversation
// that goes on from there; then the child on the transcript, of which there
// is at most one; and then the other children. Each group keeps creation
// order.
func (t *SessionTree) OrderBelow(children map[message.ID][]int, parent message.ID) {
	rank := func(k int) int {
		switch e := t.Entries[k]; {
		case e.Hidden():
			return 0
		case t.Active[e.ID]:
			return 1
		}
		return 2
	}
	kids := slices.Clone(children[parent])
	slices.SortStableFunc(kids, func(a, b int) int { return rank(a) - rank(b) })
	for _, i := range kids {
		t.Order = append(t.Order, i)
		t.OrderBelow(children, t.Entries[i].ID)
	}
}

// Lists reports whether the tree's filter and the search tokens, which are
// lower case, let e have a row. The no-tools filter always lists the head,
// so that the transcript's end stays visible. A fork's message has a row
// only while ShowHidden.
func (t *SessionTree) Lists(e TreeEntry, tokens []string) bool {
	if e.Hidden() && !t.ShowHidden {
		return false
	}
	switch t.Filter {
	case TreeFilterUserOnly:
		if e.Role != message.RoleUser {
			return false
		}
	case TreeFilterNoTools:
		if e.ToolsOnly() && e.ID != t.Head {
			return false
		}
	}
	return e.Matches(tokens)
}

// Search narrows the rows to those matching every word of query.
func (t *SessionTree) Search(query string) {
	t.Query = query
	t.Refresh()
}

// CycleFilter moves to the next filter.
func (t *SessionTree) CycleFilter() {
	t.Filter = t.Filter.Next()
	t.Refresh()
}

// ToggleHidden shows or hides the messages of the forks off the
// conversation.
func (t *SessionTree) ToggleHidden() {
	t.ShowHidden = !t.ShowHidden
	t.Refresh()
}

// Refresh lays the rows out again for the filter and search, keeping the
// last selected message selected, or the nearest listed message above it.
func (t *SessionTree) Refresh() {
	tokens := strings.Fields(strings.ToLower(t.Query))
	listed := make(map[message.ID]bool)
	t.ListedParent = make(map[message.ID]message.ID)
	t.ListedChildren = make(map[message.ID][]message.ID)
	for _, i := range t.Order {
		e := t.Entries[i]
		if !t.Lists(e, tokens) {
			continue
		}
		listed[e.ID] = true
		parent := t.ListedAncestor(e.Parent, listed)
		t.ListedParent[e.ID] = parent
		t.ListedChildren[parent] = append(t.ListedChildren[parent], e.ID)
	}

	t.Rows = t.Rows[:0]
	t.PlaceChildren("", 0, nil)
	t.RowIndex = make(map[message.ID]int, len(t.Rows))
	for i, row := range slices.Backward(t.Rows) {
		t.RowIndex[row.Entry.ID] = i
	}
	t.Select(t.NearestRow(t.Last))
}

// ListedAncestor returns id, or the nearest message above it, that listed
// marks, or the empty ID when none is.
func (t *SessionTree) ListedAncestor(id message.ID, listed map[message.ID]bool) message.ID {
	for id != "" && !listed[id] {
		i, ok := t.Index[id]
		if !ok {
			return ""
		}
		id = t.Entries[i].Parent
	}
	return id
}

// Place appends the rows of id, placed at p, and then the rows below it. A
// row behind a connector leaves a gutter at the connector's level for the
// rows below it, which runs on to the next branch of its fork. A message of
// tool calls alone takes a row for each call, as the transcript shows them;
// the rows after its first are drawn as the rows below it are.
func (t *SessionTree) Place(id message.ID, p TreePlacement) {
	gutters := p.Gutters
	if p.Connector {
		gutters = append(slices.Clone(gutters), TreeGutter{Level: p.Indent - 1, Line: !p.Last})
	}
	e := t.Entries[t.Index[id]]
	for i, entry := range e.Split() {
		prefix := TreePrefix(p)
		if i > 0 {
			prefix = TreePrefix(TreePlacement{Indent: p.Indent, Gutters: gutters})
		}
		t.Rows = append(t.Rows, TreeRow{Entry: entry, Prefix: prefix, Active: t.Active[id]})
	}
	t.PlaceChildren(id, p.Indent, gutters)
}

// PlaceChildren appends the rows below parent, the empty ID for the roots,
// whose row is indent levels deep and passes gutters on. A single child
// continues at parent's indent; the children of a fork indent one level,
// behind connectors. Parent's asides, which come first, indent one level
// behind connectors of their own, so that a single child the conversation
// goes on with still continues at parent's indent under them; beside
// several, they are branches of the same fork.
func (t *SessionTree) PlaceChildren(parent message.ID, indent int, gutters []TreeGutter) {
	kids := t.ListedChildren[parent]
	branches := t.Branches(parent)
	if len(branches) > 1 || len(branches) == len(kids) {
		t.PlaceFork(kids, indent, gutters)
		return
	}
	t.PlaceFork(kids[:len(kids)-len(branches)], indent, gutters)
	for _, kid := range branches {
		t.Place(kid, TreePlacement{Indent: indent, Gutters: gutters})
	}
}

// PlaceFork appends the rows of kids, children of a row indent levels deep
// that passes gutters on: a single one at that indent, or, when there are
// several or kids are asides, each one level deeper behind a connector.
func (t *SessionTree) PlaceFork(kids []message.ID, indent int, gutters []TreeGutter) {
	if len(kids) == 0 {
		return
	}
	fork := len(kids) > 1 || t.Aside(kids[0])
	if fork {
		indent++
	}
	for i, kid := range kids {
		t.Place(kid, TreePlacement{Indent: indent, Connector: fork, Last: i == len(kids)-1, Gutters: gutters})
	}
}

// Aside reports whether id hangs off the conversation as an aside: a
// message of a fork whose listed parent is a message of the conversation,
// such as the request that titled the session, below its prompt.
func (t *SessionTree) Aside(id message.ID) bool {
	parent := t.ListedParent[id]
	if parent == "" || !t.Entries[t.Index[id]].Hidden() {
		return false
	}
	return !t.Entries[t.Index[parent]].Hidden()
}

// Branches returns the listed children of id that go on from it, leaving out
// its asides, in drawing order: those the conversation goes on with, or, for
// a message of a fork, the rest of its fork.
func (t *SessionTree) Branches(id message.ID) []message.ID {
	return slices.DeleteFunc(slices.Clone(t.ListedChildren[id]), t.Aside)
}

// NearestRow returns the row of id, or of the nearest listed message above
// it, or the first row when there is neither, as for an empty transcript.
func (t *SessionTree) NearestRow(id message.ID) int {
	for id != "" {
		if row, ok := t.RowIndex[id]; ok {
			return row
		}
		i, ok := t.Index[id]
		if !ok {
			break
		}
		id = t.Entries[i].Parent
	}
	return 0
}

// Select selects row i, when there is one.
func (t *SessionTree) Select(i int) {
	if i < 0 || i >= len(t.Rows) {
		t.Selected = 0
		return
	}
	t.Selected = i
	t.Last = t.Rows[i].Entry.ID
}

// SelectedRow returns the selected row, and false when no row is listed.
func (t *SessionTree) SelectedRow() (TreeRow, bool) {
	if t.Selected < 0 || t.Selected >= len(t.Rows) {
		return TreeRow{}, false
	}
	return t.Rows[t.Selected], true
}

// Move selects the row delta rows below the selected one, or above it for a
// negative delta, wrapping at both ends.
func (t *SessionTree) Move(delta int) {
	if n := len(t.Rows); n > 0 {
		t.Select(((t.Selected+delta)%n + n) % n)
	}
}

// Page selects the row delta pages of rows rows below the selected one, or
// above it for a negative delta, stopping at the ends.
func (t *SessionTree) Page(delta, rows int) {
	if len(t.Rows) > 0 {
		t.Select(min(max(t.Selected+delta*rows, 0), len(t.Rows)-1))
	}
}

// JumpFork selects the start of the next branch below the selection, or for
// a negative dir, the start of the branch the selection is on. Going down
// follows the first branch of each fork; a row with nothing below it stays
// selected. Going up from the start of a branch reaches the start of the
// branch it forked from, and at last the root. Asides are no branches: the
// jumps pass them by.
func (t *SessionTree) JumpFork(dir int) {
	row, ok := t.SelectedRow()
	if !ok {
		return
	}
	id := row.Entry.ID
	if dir > 0 {
		for {
			kids := t.Branches(id)
			if len(kids) == 0 {
				break
			}
			id = kids[0]
			if len(kids) > 1 {
				break
			}
		}
		t.Select(t.RowIndex[id])
		return
	}
	for {
		parent, ok := t.ListedParent[id]
		if !ok || parent == "" {
			break
		}
		if len(t.Branches(parent)) > 1 && t.RowIndex[id] < t.Selected {
			break
		}
		id = parent
	}
	t.Select(t.RowIndex[id])
}

// Window returns the bounds of the at most n rows drawn: the rows around the
// selected one, which is centered unless the rows run out.
func (t *SessionTree) Window(n int) (start, end int) {
	start = max(0, min(t.Selected-n/2, len(t.Rows)-n))
	return start, min(start+n, len(t.Rows))
}

// View returns what the tree panel draws: at most n rows around the
// selection, the count after its title, which, like every menu's, counts the
// rows only when they do not all fit, and the status line under the rows,
// which names a filter and says when nothing matches.
//
// The rows leave a line of the n for the insert line, which marks where the
// next prompt goes, as TreeView.InsertLine places it.
func (t *SessionTree) View(n int) TreeView {
	rows := n
	if len(t.Rows) > 0 {
		rows = max(n-1, 1)
	}
	start, end := t.Window(rows)
	// The view keeps its own rows, since Refresh reuses the tree's.
	v := TreeView{Rows: slices.Clone(t.Rows[start:end]), Selected: t.Selected - start, Query: t.Query}
	var status []string
	if len(t.Rows) == 0 {
		v.Selected = -1
		status = append(status, NoMatchesText)
	} else {
		v.Count = TitleCount(t.Selected, len(t.Rows), rows)
	}
	if label := t.Filter.Label(); label != "" {
		status = append(status, "["+label+"]")
	}
	if t.ShowHidden {
		status = append(status, Tag(HiddenLabel))
	}
	v.Status = strings.Join(status, " ")
	return v
}

// TreeView is what the session tree panel draws.
type TreeView struct {
	// Rows are the rows on screen.
	Rows []TreeRow
	// Selected is the index in Rows of the selected row, or -1 when Rows is
	// empty.
	Selected int
	// Count is the selected row's place, such as "[3/40]", when the rows do
	// not all fit, which the panel's title shows, or "".
	Count string
	// Status names the filter, such as "[user]", and "[hidden]" while the
	// forks are shown, or says that nothing matches. It is empty when there
	// is nothing to say.
	Status string
	// Query is the search the rows match, which the input shows.
	Query string
}

// InsertLine returns the line of the tree's lines that marks where the next
// prompt goes, an empty line that TreeLines draws among the rows, or -1
// when no row is selected. It opens under the selected row: choosing a
// reply or a tool call continues the session from it, and choosing a
// prompt puts it in the input to edit, keeping its row in the tree.
func (v TreeView) InsertLine() int {
	if v.Selected < 0 || v.Selected >= len(v.Rows) {
		return -1
	}
	return v.Selected + 1
}

// TreeLineCount returns the number of lines n rows of the session tree take:
// the rows, and the insert line among them when there are any.
func TreeLineCount(n int) int {
	if n == 0 {
		return 0
	}
	return n + 1
}
