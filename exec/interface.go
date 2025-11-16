package exec

import (
	"os"
)

type Pipe interface {
	Open() error
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
}

type StdPipe struct {
}

func NewStdPipe() *StdPipe {
	return &StdPipe{}
}
func (p *StdPipe) Open() error {
	return nil
}

func (p *StdPipe) Read(b []byte) (n int, err error) {
	return os.Stdin.Read(b)
}

func (p *StdPipe) Write(b []byte) (n int, err error) {
	return os.Stdout.Write(b)
}

func (p *StdPipe) Close() error {
	return nil
}
