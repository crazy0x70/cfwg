package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

type unexpectedExitError struct {
	err error
}

func (e *unexpectedExitError) Error() string {
	return fmt.Sprintf("process exited unexpectedly: %v", e.err)
}

func (e *unexpectedExitError) Unwrap() error {
	return e.err
}

func normalizeWaitError(err error, stopped bool) error {
	if err == nil {
		return nil
	}
	if stopped {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
	}

	return &unexpectedExitError{err: err}
}
