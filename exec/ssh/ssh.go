package ssh

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/cfg"
)

type SSH struct {
	remote string
	command string
	ssh_cfg *ssh.ClientConfig

	client *ssh.Client
	session *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

func ParseDestination(input string) (string, string) {
	idx := strings.LastIndex(input, ":")
	if idx == -1 {
		return input, ""
	}
	remote := input[:idx]
	path := input[idx+1:]
	return remote, path
}

func parseSSHAddr(input string) (user, host, port string) {
	if i := strings.Index(input, "@"); i != -1 {
		user = input[:i]
		input = input[i+1:]
	}

	parts := strings.Split(input, ":")
	switch len(parts) {
	case 1:
		host = parts[0]
	case 2:
		host = parts[0]
		port = parts[1]
	default:
		host = parts[0]
		port = parts[1]
	}

	return
}

func NewPipe(target string, command string) exec.Pipe {
	user, hostAddress, port := parseSSHAddr(target)
	remote := expandHostAddress(hostAddress, port)
	cfg, err := MakeSSHConfig(hostAddress)

	if err == nil {
		if user != "" {
			cfg.User = user
		}
	}

	return &SSH {
		ssh_cfg: cfg,
		remote:  remote,
		command: command,
	}
}

func (peer *SSH)Open() error {
	client, err := ssh.Dial("tcp", peer.remote, peer.ssh_cfg)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}

	// Get pipes to remote stdin/stdout/stderr
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}

	peer.client = client
	peer.session = session
	peer.stdin = stdin
	peer.stdout = stdout


	if cfg.Verbose {
		stderr, err := session.StderrPipe()
		if err != nil {
			fmt.Println("failed to get stderr pipe:", err)
			return err
		}
		go func() {
			io.Copy(os.Stderr, stderr)
		}()
	}

	return session.Start(peer.command)
}

func (peer *SSH) Close() error {
	peer.stdin.Close()
	peer.session.Wait()
	return nil
}

func (peer *SSH) Read(p []byte) (n int, err error) {
	return peer.stdout.Read(p)
}

func (peer *SSH) Write(p []byte) (n int, err error) {
	return peer.stdin.Write(p)
}
