package client

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type Status struct {
	w    io.Writer
	tty  bool
	prev int
}

func NewStatus(w io.Writer) *Status {
	tty := false
	if f, ok := w.(*os.File); ok {
		tty = term.IsTerminal(int(f.Fd()))
	}
	return &Status{w: w, tty: tty}
}

func (s *Status) Set(msg string) {
	if s == nil || s.w == nil {
		return
	}
	if !s.tty {
		fmt.Fprintln(s.w, msg)
		return
	}
	pad := s.prev - len(msg)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(s.w, "\r%s%*s", msg, pad, "")
	s.prev = len(msg)
}

func (s *Status) Clear() {
	if s == nil || s.w == nil || !s.tty || s.prev == 0 {
		return
	}
	fmt.Fprintf(s.w, "\r%*s\r", s.prev, "")
	s.prev = 0
}
