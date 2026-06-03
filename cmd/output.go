package cmd

import "os"

const (
	ansiReset   = "\033[0m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiRed     = "\033[31m"
	ansiCyan    = "\033[36m"
	ansiMagenta = "\033[35m"
	ansiGray    = "\033[90m"
)

var colorEnabled = os.Getenv("NO_COLOR") == ""

func colorize(text, color string) string {
	if !colorEnabled || color == "" {
		return text
	}
	return color + text + ansiReset
}
