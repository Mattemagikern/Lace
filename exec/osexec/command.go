package osexec

import (
	"os"
	"io"
	"os/exec"
	"strings"
	myexec "github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/cfg"
)

type LocalExec struct {
	command string
	cmd    *exec.Cmd

	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func NewPipe(path, command string) myexec.Pipe {
	return &LocalExec{
		command: command,
	}
}

func (peer *LocalExec) Open() error {
	var err error
	parts := strings.Fields(peer.command)
	peer.cmd = exec.Command(parts[0], parts[1:]...)
	peer.stdin, err = peer.cmd.StdinPipe()
	if err != nil {
		return err
	}

	peer.stdout, err = peer.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if cfg.Verbose {
		stderr, err := peer.cmd.StderrPipe()
		if err != nil {
			return err
		}
		go func () {
			io.Copy(os.Stderr, stderr)
		}()
	}
	return peer.cmd.Start()
}

func (peer *LocalExec) Read(p []byte) (n int, err error) {
	return peer.stdout.Read(p)
}

func (peer *LocalExec) Write(p []byte) (n int, err error) {
	return peer.stdin.Write(p)
}

func (peer *LocalExec) Close() error {
	peer.stdin.Close()
	return peer.cmd.Wait()
}
