//go:build !unix && !windows

package logging

import (
	"errors"
	"os"
)

// redirectStderr fails where standard error cannot be redirected.
func redirectStderr(*os.File) (restore func(), err error) {
	return nil, errors.ErrUnsupported
}
