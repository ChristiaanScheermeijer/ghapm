package cmd

type exitCodeError struct {
	code    int
	message string
}

func newExitCodeError(code int, message string) error {
	return &exitCodeError{code: code, message: message}
}

func (e *exitCodeError) Error() string {
	return e.message
}

func (e *exitCodeError) ExitCode() int {
	if e.code <= 0 {
		return 1
	}
	return e.code
}
