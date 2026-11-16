// Package script holds what the service shares about running its Python scripts.
package script

import "strings"

// LastLine is the last line a script wrote: the exception, after a Python traceback.
func LastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
