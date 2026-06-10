// Package term contains the platform seam: TTY detection, opening the
// controlling terminal for keyboard input when stdin carries data, and raw
// mode for the single-keystroke promotion listener.
package term

import (
	"os"

	xterm "golang.org/x/term"
)

func IsTTY(f *os.File) bool {
	return xterm.IsTerminal(int(f.Fd()))
}

// OpenTTY opens the controlling terminal for reading keystrokes
// independently of stdin.
func OpenTTY() (*os.File, error) {
	return os.OpenFile(ttyPath, os.O_RDWR, 0)
}

type State = xterm.State

func MakeRaw(f *os.File) (*State, error) {
	return xterm.MakeRaw(int(f.Fd()))
}

func Restore(f *os.File, s *State) error {
	return xterm.Restore(int(f.Fd()), s)
}
