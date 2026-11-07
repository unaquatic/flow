package ui_test

import (
	demoui "github.com/unaquatic/flow/extension/demo/ui"
	toolsui "github.com/unaquatic/flow/extension/tools/ui"
	"github.com/unaquatic/flow/terminal/ui"
)

// The tests of package ui show the transcript and the menus as flow does,
// with the widgets of the extensions it is built with that need no app: the
// formats of the tools extension, and the demo's /demo and labels. They
// cannot import them, since these import ui. This sets them before any test
// runs, as the test binary starts.
func init() {
	ui.BuiltinExtensions = []ui.Extension{toolsui.New(), demoui.New()}
}
