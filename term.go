package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func enableRawMode() *unix.Termios {
	fd := int(os.Stdin.Fd())
	oldState, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil
	}
	raw := *oldState
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw)
	return oldState
}

func restoreTermMode(state *unix.Termios) {
	if state == nil {
		return
	}
	fd := int(os.Stdin.Fd())
	unix.IoctlSetTermios(fd, unix.TIOCSETA, state)
}
