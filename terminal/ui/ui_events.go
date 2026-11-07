package ui

// ProcessEvent dispatches one event to its widget handler.
func (ui *UI) ProcessEvent(evt UIEvent) {
	switch e := evt.(type) {
	case EventQuit:
		ui.OnQuit(e)
	case EventQuitArmed:
		ui.OnQuitArmed(e)
	case EventInputClear:
		ui.OnInputClear(e)
	case EventInputSet:
		ui.OnInputSet(e)
	case EventClearMessages:
		ui.OnClearMessages(e)
	case EventMessageAdded:
		ui.OnMessageAdded(e)
	case EventMessageUpdated:
		ui.OnMessageUpdated(e)
	case EventTurnEnded:
		ui.OnTurnEnded(e)
	case EventStatusChanged:
		ui.OnStatusChanged(e)
	case EventModelLabelChanged:
		ui.OnModelLabelChanged(e)
	case EventUsageChanged:
		ui.OnUsageChanged(e)
	case EventSlashMenuShow:
		ui.OnSlashMenuShow(e)
	case EventSlashMenuHide:
		ui.OnSlashMenuHide(e)
	case EventSessionMenuShow:
		ui.OnSessionMenuShow(e)
	case EventSessionMenuHide:
		ui.OnSessionMenuHide(e)
	case EventSessionLoad:
		ui.OnSessionLoad(e)
	case EventSessionPreview:
		ui.OnSessionPreview(e)
	case EventSessionPrompts:
		ui.OnSessionPrompts(e)
	case EventBannerShow:
		ui.OnBannerShow(e)
	case EventBannerUpdate:
		ui.OnBannerUpdate(e)
	case EventBannerHide:
		ui.OnBannerHide(e)
	case EventIndicatorShow:
		ui.OnIndicatorShow(e)
	case EventIndicatorHide:
		ui.OnIndicatorHide(e)
	case EventInlineModeSet:
		ui.OnInlineModeSet(e)
	case EventQueueChanged:
		ui.OnQueueChanged(e)
	case EventTitleSet:
		ui.OnTitleSet(e)
	case EventProgressSet:
		ui.OnProgressSet(e)
	case EventUndoRequested:
		ui.OnUndoRequested(e)
	case EventRedoRequested:
		ui.OnRedoRequested(e)
	case EventTreeShow:
		ui.OnTreeShow(e)
	case EventTreeMoveRequested:
		ui.OnTreeMoveRequested(e)
	case EventToolsExpandedSet:
		ui.OnToolsExpandedSet(e)
	case EventExpandStyleSet:
		ui.OnExpandStyleSet(e)
	case EventToolMarkersSet:
		ui.OnToolMarkersSet(e)
	case EventToolColorSet:
		ui.OnToolColorSet(e)
	case EventMarkerShapeSet:
		ui.OnMarkerShapeSet(e)
	case EventDebugSet:
		ui.OnDebugSet(e)
	case EventCacheModeSet:
		ui.OnCacheModeSet(e)
	case EventKeyHintsModeSet:
		ui.OnKeyHintsModeSet(e)
	case EventUsagePinnedSet:
		ui.OnUsagePinnedSet(e)
	case EventCountPlaceSet:
		ui.OnCountPlaceSet(e)
	case EventSelectionStyleSet:
		ui.OnSelectionStyleSet(e)
	case EventPromptStyleSet:
		ui.OnPromptStyleSet(e)
	case EventInputHintChanged:
		ui.OnInputHintChanged(e)
	}
}

// OnInputHintChanged shows the hint the empty input now shows.
func (ui *UI) OnInputHintChanged(EventInputHintChanged) { ui.SyncInputHint() }

// OnCacheModeSet shows the cache use of every reply where e says.
func (ui *UI) OnCacheModeSet(e EventCacheModeSet) { ui.SetCacheMode(e.Mode) }

// OnExpandStyleSet shows every expanded tool call as e says.
func (ui *UI) OnExpandStyleSet(e EventExpandStyleSet) { ui.SetExpandStyle(e.Style) }

// OnToolMarkersSet shows or hides the marker of every tool call as e says.
func (ui *UI) OnToolMarkersSet(e EventToolMarkersSet) { ui.SetToolMarkers(e.Show) }

// OnMarkerShapeSet shows the marker of every tool call in e's shape.
func (ui *UI) OnMarkerShapeSet(e EventMarkerShapeSet) { ui.SetMarkerShape(e.Shape) }

// OnToolColorSet colors the marker and the name of every tool call as e says.
func (ui *UI) OnToolColorSet(e EventToolColorSet) { ui.SetToolColor(e.Color) }

// OnDebugSet shows or hides what helps debug flow as e says.
func (ui *UI) OnDebugSet(e EventDebugSet) { ui.SetDebug(e.On) }

// OnKeyHintsModeSet names every panel's keys where the mode now says.
func (ui *UI) OnKeyHintsModeSet(EventKeyHintsModeSet) { ui.SyncKeyHints() }

// OnCountPlaceSet counts the rows of every list where the place now says.
func (ui *UI) OnCountPlaceSet(EventCountPlaceSet) { ui.SyncCounts() }

// OnPromptStyleSet shows every prompt as e says, building the transcript
// again, since a prompt's background is set as it is built.
func (ui *UI) OnPromptStyleSet(EventPromptStyleSet) { ui.RebuildTranscript() }

// OnSelectionStyleSet redraws every menu, whose selected row MarkSelection
// marks as the style now says, and the session tree, whose selected row
// FillTreeSelection and MarkTreeSelection mark.
func (ui *UI) OnSelectionStyleSet(EventSelectionStyleSet) {
	for menu := range ui.Menus() {
		menu.RequestRender()
	}
	ui.treeRowsBox.RequestRender()
	ui.treeRows.RequestRender()
}

// OnUsagePinnedSet shows or hides the Usage box as the pin now says, and
// names what Ctrl+U does next.
func (ui *UI) OnUsagePinnedSet(EventUsagePinnedSet) {
	ui.SyncKeyHints()
	ui.SyncSlashStatus()
}

// OnQuit tears the renderer down, ending Run.
//
// Inline output stays in the terminal, so one last frame is drawn first, as
// PrepareInlineQuit lays it out.
func (ui *UI) OnQuit(EventQuit) {
	if ui.inline {
		ui.PrepareInlineQuit()
		ui.renderer.Render()
	}
	ui.Destroy()
}

// OnQuitArmed shows the key that quits under the input box until a second
// Ctrl+C no longer quits.
func (ui *UI) OnQuitArmed(EventQuitArmed) { ui.ShowQuitHint(ui.renderer.Enqueue) }

// PrepareInlineQuit lays out the frame that an inline quit leaves in the
// terminal: the transcript alone, without the input box or the indicator,
// which no longer apply, and under it what follows: the notice that tells
// how to resume a stored session, which Run then reports shown, or else the
// shell's prompt. The transcript ends in a blank row, which parts the two.
func (ui *UI) PrepareInlineQuit() {
	if id := ui.service.Resumable(); id != "" {
		ui.AppendResumeNotice(id)
		ui.resumeShown.Store(true)
	}
	// The input placed the cursor, and would place it again as the box
	// redraws. Blurring it hides the cursor for good.
	ui.input.Blur()
	ui.inputWrapper.SetGone(true)
	ui.indicatorBox.SetGone(true)
	// A shimmer caught mid-sweep would stay in the terminal, over the time
	// and over the tool call rows alike.
	ui.shimmer.On.Store(false)
	ui.shimmer.SyncRows()
}

// OnInputClear empties the input box.
func (ui *UI) OnInputClear(EventInputClear) { ui.ClearInput() }

// OnInputSet replaces the input box's text and parks the cursor at its end.
func (ui *UI) OnInputSet(e EventInputSet) { ui.SetInputText(e.Text) }

// OnStatusChanged replaces the hint in the status line.
func (ui *UI) OnStatusChanged(e EventStatusChanged) { ui.SetStatus(e.Content) }

// OnModelLabelChanged replaces the model the slash menu's status row names.
func (ui *UI) OnModelLabelChanged(e EventModelLabelChanged) { ui.SetModelLabel(e.Label) }

// OnUsageChanged replaces the token use the slash menu's status row shows.
func (ui *UI) OnUsageChanged(e EventUsageChanged) { ui.SetUsage(e.Usage) }

// OnClearMessages empties the transcript.
func (ui *UI) OnClearMessages(EventClearMessages) { ui.ResetTranscript() }

// OnMessageAdded appends a message, merging it into the preceding
// bubble when they share a parent, or a notice.
func (ui *UI) OnMessageAdded(e EventMessageAdded) { ui.AddMessage(e.Msg) }

// OnMessageUpdated applies the latest parts and usage to a message.
func (ui *UI) OnMessageUpdated(e EventMessageUpdated) { ui.UpdateMessage(e.Msg) }

// OnTurnEnded shows how long a turn took under its last reply, and in the
// worked time of the slash menu's status row.
func (ui *UI) OnTurnEnded(e EventTurnEnded) {
	ui.SetTurnEnd(e.MessageID, e.Worked)
	ui.SyncSlashStatus()
}

// OnSessionLoad rebuilds the transcript from a session's persisted log.
func (ui *UI) OnSessionLoad(e EventSessionLoad) { ui.LoadTranscript(e.Messages) }

// OnSessionPreview refills the preview pane shown beside the session menu.
func (ui *UI) OnSessionPreview(e EventSessionPreview) { ui.LoadPreview(e.Messages) }

// OnSessionPrompts replaces the prompts listed under the session menu in
// inline mode.
func (ui *UI) OnSessionPrompts(e EventSessionPrompts) { ui.sessionPrompts.Set(e.Prompts) }

// OnInlineModeSet switches between inline and alternate-screen rendering.
func (ui *UI) OnInlineModeSet(e EventInlineModeSet) { ui.SetInlineMode(e) }

// OnQueueChanged shows the queued prompts in the input box.
func (ui *UI) OnQueueChanged(e EventQueueChanged) { ui.ShowQueue(e.Prompts) }

// OnTitleSet replaces the terminal window and tab title.
func (ui *UI) OnTitleSet(e EventTitleSet) { ui.renderer.SetTitle(e.Title) }

// OnProgressSet shows or clears the progress indicator on the terminal tab.
func (ui *UI) OnProgressSet(e EventProgressSet) { ui.renderer.SetProgress(e.Active) }

// OnUndoRequested starts a move back one turn off the event loop.
func (ui *UI) OnUndoRequested(EventUndoRequested) { ui.RequestUndo() }

// OnRedoRequested starts a move forward one turn off the event loop.
func (ui *UI) OnRedoRequested(EventRedoRequested) { ui.RequestRedo() }

// OnTreeShow draws the session tree at the top of the input box.
func (ui *UI) OnTreeShow(e EventTreeShow) { ui.ShowTree(e.Tree) }

// OnTreeMoveRequested starts a move to a message chosen in the session tree
// off the event loop.
func (ui *UI) OnTreeMoveRequested(e EventTreeMoveRequested) { ui.RequestTreeMove(e) }

// OnToolsExpandedSet shows or hides the details of every tool call and of the
// changes of every step. Laying out a long transcript again and writing it to
// the terminal holds the event loop for a while, so when that changes any
// block, the indicator says so until the new frame is drawn. Rows committed to
// the terminal are written again whatever they show, as ReprintCommitted
// writes them.
func (ui *UI) OnToolsExpandedSet(e EventToolsExpandedSet) {
	if !ui.committed && !ui.TogglesTools(e.Expanded) {
		ui.SetToolsExpanded(e.Expanded)
		return
	}
	text := ToolsToggleText(e.Expanded, ui.service.TurnElapsed())
	ui.ShowIndicatorWhile(text, func() { ui.SetToolsExpanded(e.Expanded) })
}

// OnIndicatorShow reveals the indicator below the message list.
func (ui *UI) OnIndicatorShow(e EventIndicatorShow) { ui.ShowIndicator(e) }

// OnIndicatorHide dismisses the indicator.
func (ui *UI) OnIndicatorHide(EventIndicatorHide) { ui.HideIndicator() }

// OnSlashMenuShow inserts the command menu into the input box.
func (ui *UI) OnSlashMenuShow(e EventSlashMenuShow) { ui.ShowSlashMenu(e.Items) }

// OnSlashMenuHide removes the command menu.
func (ui *UI) OnSlashMenuHide(EventSlashMenuHide) { ui.HideSlashMenu() }

// OnSessionMenuShow opens the session list over the transcript.
func (ui *UI) OnSessionMenuShow(e EventSessionMenuShow) { ui.ShowSessionMenu(e) }

// OnSessionMenuHide closes the session list and restores the transcript.
func (ui *UI) OnSessionMenuHide(EventSessionMenuHide) { ui.HideSessionMenu() }

// OnBannerShow displays the modal banner at the top of the input box.
func (ui *UI) OnBannerShow(e EventBannerShow) { ui.ShowBanner(e) }

// OnBannerUpdate replaces the banner's body text.
func (ui *UI) OnBannerUpdate(e EventBannerUpdate) { ui.SetBannerText(e.Text) }

// OnBannerHide takes the banner down.
func (ui *UI) OnBannerHide(EventBannerHide) { ui.HideBanner() }
