package app

import (
	"errors"
	"io/fs"
	"slices"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/extension"
)

var (
	// ErrNothingToUndo indicates that no visible prompt can be rewound.
	ErrNothingToUndo = errors.New("nothing to undo")

	// ErrNothingToRedo indicates that the session is already at its latest state.
	ErrNothingToRedo = errors.New("nothing to redo")

	// ErrBusy indicates that a session operation is already active.
	ErrBusy = errors.New("session is busy")
)

// branchView is the history a move operates on: one whole branch, plus the
// head that says how much of it the session currently shows.
type branchView struct {
	// Messages is the branch from its root to its leaf, including any suffix
	// the head hides.
	Messages []message.Content
	// Head is the session's position on that branch.
	Head session.Head
}

// Leaf returns the end of the branch the view was taken from.
func (v branchView) Leaf() message.ID {
	if v.Head.Forward != "" {
		return v.Head.Forward
	}
	return v.Head.ID
}

// Visible returns the messages the head leaves on show.
func (v branchView) Visible() []message.Content {
	if v.Head.ID == "" {
		return nil
	}
	idx := slices.IndexFunc(v.Messages, func(m message.Content) bool { return m.ID == v.Head.ID })
	if idx < 0 {
		return nil
	}
	return v.Messages[:idx+1]
}

// undo moves the session to the state before its newest visible prompt.
func (s Session) undo() ([]FileDiff, error) {
	v, err := s.branchView()
	if err != nil {
		return nil, err
	}
	target, ok := undoTarget(v.Visible())
	if !ok {
		return nil, ErrNothingToUndo
	}
	return s.moveTo(v.Messages, session.Head{ID: target, Forward: v.Leaf()})
}

// redo moves the session forward by one prompt and its run, towards the branch
// leaf it was last moved back from. Reaching the leaf leaves nothing further
// ahead.
func (s Session) redo() ([]FileDiff, error) {
	v, err := s.branchView()
	if err != nil {
		return nil, err
	}
	if !v.Head.MovedBack() {
		return nil, ErrNothingToRedo
	}
	target, ok := redoTarget(v.Messages, v.Head.ID)
	if !ok {
		return nil, ErrNothingToRedo
	}
	h := session.Head{ID: target}
	if target != v.Leaf() {
		h.Forward = v.Leaf()
	}
	return s.moveTo(v.Messages, h)
}

// checkoutAt moves the session to the state before messageID. An empty
// messageID returns to the latest state.
func (s Session) checkoutAt(messageID message.ID) ([]FileDiff, error) {
	v, err := s.branchView()
	if err != nil {
		return nil, err
	}
	if messageID == "" {
		if !v.Head.MovedBack() {
			return nil, ErrNothingToRedo
		}
		return s.moveTo(v.Messages, session.Head{ID: v.Head.Forward})
	}
	if idx := slices.IndexFunc(v.Messages, sameID(messageID)); idx >= 0 {
		return s.moveTo(v.Messages, session.Head{ID: precedes(v.Messages, idx), Forward: v.Leaf()})
	}
	return s.checkoutOffBranch(messageID)
}

// checkoutOffBranch moves to a point on a branch the session is not currently
// on. Like a branch switch the move is sideways, so it leaves nothing ahead to
// redo: the branch being left stays reachable by name.
func (s Session) checkoutOffBranch(messageID message.ID) ([]FileDiff, error) {
	branch, _, err := s.branchThrough(messageID)
	if err != nil {
		return nil, err
	}
	idx := slices.IndexFunc(branch, sameID(messageID))
	if idx < 0 {
		return nil, session.ErrMessageNotFound
	}
	return s.moveTo(branch, session.Head{ID: precedes(branch, idx)})
}

// precedes returns the message before branch[idx], or an empty ID at the root.
func precedes(branch []message.Content, idx int) message.ID {
	if idx == 0 {
		return ""
	}
	return branch[idx-1].ID
}

// sameID matches a message by ID.
func sameID(id message.ID) func(message.Content) bool {
	return func(m message.Content) bool { return m.ID == id }
}

// switchBranch moves the session onto the branch opened by messageID, landing
// on that branch's newest leaf.
//
// The move is sideways rather than backwards, so it leaves nothing ahead to
// redo: the branch being left stays reachable by switching back to it.
func (s Session) switchBranch(messageID message.ID) ([]FileDiff, error) {
	branch, leaf, err := s.branchThrough(messageID)
	if err != nil {
		return nil, err
	}
	return s.moveTo(branch, session.Head{ID: leaf})
}

// branchThrough returns the branch that runs through messageID to its newest
// leaf, and that leaf. It reports [session.ErrMessageNotFound] when the session holds
// no message messageID.
func (s Session) branchThrough(messageID message.ID) ([]message.Content, message.ID, error) {
	leaf, err := s.app.sessions.Leaf(s.ctx, s.id, messageID)
	if err != nil {
		return nil, "", err
	}
	branch, err := s.app.sessions.Path(s.ctx, s.id, leaf)
	if err != nil {
		return nil, "", err
	}
	return branch, leaf, nil
}

// branchView returns the whole branch the session sits on, including the
// suffix a moved head hides.
func (s Session) branchView() (branchView, error) {
	h, err := s.head()
	if err != nil {
		return branchView{}, err
	}
	v := branchView{Head: h}
	if v.Messages, err = s.app.sessions.Path(s.ctx, s.id, v.Leaf()); err != nil {
		return branchView{}, err
	}
	return v, nil
}

// head returns the session's current position in its message tree, which is
// the zero Head for a session that is not stored yet.
func (s Session) head() (session.Head, error) {
	sess, err := s.app.sessions.Session(s.ctx, s.id)
	if errors.Is(err, fs.ErrNotExist) {
		return session.Head{}, nil
	}
	if err != nil {
		return session.Head{}, err
	}
	return sess.Head, nil
}

// moveTo lets the extension.MoveHook of each extension bring what it keeps in step
// with the session, such as the workspace, to h, records h, and reports the
// files the move changed. branch must contain h.ID. A hook that fails, or
// refuses the move, leaves the head where it was.
//
// The hooks are given the destination alone, never the point being left:
// undo, redo, and a branch switch are this one operation.
func (s Session) moveTo(branch []message.Content, h session.Head) ([]FileDiff, error) {
	if h.ID != "" && !slices.ContainsFunc(branch, sameID(h.ID)) {
		return nil, session.ErrMessageNotFound
	}
	m := extension.Move{SessionID: s.id, Branch: branch, Head: h.ID}
	var files []FileDiff
	for hook := range extension.Hooks[extension.MoveHook](s.app.extensions) {
		res, err := hook.OnMove(s.ctx, m)
		if err != nil {
			return nil, err
		}
		files = append(files, res.Files...)
	}
	if err := s.app.sessions.SetHead(s.ctx, s.id, h); err != nil {
		return nil, err
	}
	return files, nil
}

// undoTarget returns the message a rewound session should end at, which is the
// one before the newest visible prompt. It reports false when no prompt
// remains, and an empty ID when rewinding leaves nothing visible.
func undoTarget(visible []message.Content) (message.ID, bool) {
	for i, m := range slices.Backward(visible) {
		if m.Role != message.RoleUser {
			continue
		}
		if i == 0 {
			return "", true
		}
		return visible[i-1].ID, true
	}
	return "", false
}

// redoTarget returns the message a re-advanced session should end at, which is
// the one before the second prompt following head: the first of those prompts
// and its run become visible, the next stays hidden. It returns the branch
// leaf when fewer than two prompts follow, and reports false when branch is
// empty or does not hold head.
func redoTarget(branch []message.Content, head message.ID) (message.ID, bool) {
	if len(branch) == 0 {
		return "", false
	}
	start := 0
	if head != "" {
		idx := slices.IndexFunc(branch, sameID(head))
		if idx < 0 {
			return "", false
		}
		start = idx + 1
	}
	prompts := 0
	for i, m := range branch[start:] {
		if m.Role != message.RoleUser {
			continue
		}
		prompts++
		if prompts == 2 {
			return branch[start+i-1].ID, true
		}
	}
	return branch[len(branch)-1].ID, true
}
