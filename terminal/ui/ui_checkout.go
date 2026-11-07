package ui

import "github.com/unaquatic/flow/app"

// RequestUndo moves the session back one turn.
//
// The move runs on its own goroutine because it first waits for the run in
// progress to release the session, and a run releases it only after
// publishing events that only the event loop drains. Waiting here would close
// that cycle and hang the UI. The result reenters through Enqueue, so the
// transcript is still updated on the loop.
func (ui *UI) RequestUndo() {
	ui.move(app.Session.Undo, ui.service.CompleteUndo)
}

// RequestRedo moves the session forward one turn, off the event loop for the
// reason given on RequestUndo.
func (ui *UI) RequestRedo() {
	ui.move(app.Session.Redo, ui.service.CompleteRedo)
}

// RequestTreeMove moves the session to a message chosen in the session tree,
// off the event loop for the reason given on RequestUndo: before a chosen
// prompt, and to any other message.
func (ui *UI) RequestTreeMove(e EventTreeMoveRequested) {
	run := func(s app.Session) (app.MoveResult, error) { return s.Navigate(e.MessageID) }
	if e.Edit {
		run = func(s app.Session) (app.MoveResult, error) { return s.Checkout(e.MessageID) }
	}
	ui.move(run, func(moved app.MoveResult, err error) []UIEvent {
		return ui.service.CompleteTreeMove(e, moved, err)
	})
}

// move runs a history move of the active session off the event loop, once
// the operation in progress on the session has released it, and applies
// report back on the loop. The queue holds until the move finishes, and then
// sends its next prompt in the moved-to state.
//
// A move publishes the transcript it leads to, and loading that transcript
// clears every row, notices included. report is applied behind it, so that
// the notice it shows, such as the files the move changed, stays.
func (ui *UI) move(
	run func(app.Session) (app.MoveResult, error),
	report func(app.MoveResult, error) []UIEvent,
) {
	sess, ok := ui.service.ActiveSession()
	if !ok {
		return
	}
	ui.service.BeginMove()
	go func() {
		if sess.Wait() != nil {
			ui.renderer.Enqueue(func() { ui.Apply(ui.service.EndMove()) })
			return
		}
		moved, err := run(sess)
		ui.appEvents.After(func() {
			ui.Apply(report(moved, err))
			ui.Apply(ui.service.EndMove())
		})
	}()
}
