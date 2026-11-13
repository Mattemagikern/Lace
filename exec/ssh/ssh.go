package ssh

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mattemagikern/lace/cfg"
	"github.com/mattemagikern/lace/exec"
	"golang.org/x/crypto/ssh"
)

var (
	clientsMu sync.Mutex
	clients   = map[string]*ssh.Client{}
	connSeq   atomic.Uint64
)

const maxConnShards = 4

func connShards() int {
	n := cfg.Jobs
	if n > maxConnShards {
		n = maxConnShards
	}
	if n < 1 {
		n = 1
	}
	return n
}

func sharedClient(remote string, cfg *ssh.ClientConfig, chain []hop, shard int) (*ssh.Client, error) {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	key := clientKey(remote, chain, shard)
	if c, ok := clients[key]; ok {
		return c, nil
	}
	c, err := dialChain(remote, cfg, chain)
	if err != nil {
		return nil, err
	}
	clients[key] = c
	return c, nil
}

func clientKey(remote string, chain []hop, shard int) string {
	key := strconv.Itoa(shard) + "|" + remote
	for _, h := range chain {
		key = h.remote + "|" + key
	}
	return key
}

func dialChain(remote string, cfg *ssh.ClientConfig, chain []hop) (*ssh.Client, error) {
	if len(chain) == 0 {
		return ssh.Dial("tcp", remote, cfg)
	}

	client, err := ssh.Dial("tcp", chain[0].remote, chain[0].cfg)
	if err != nil {
		return nil, err
	}

	hops := make([]hop, 0, len(chain))
	hops = append(hops, chain[1:]...)
	hops = append(hops, hop{remote: remote, cfg: cfg})
	for _, h := range hops {
		conn, err := client.Dial("tcp", h.remote)
		if err != nil {
			return nil, err
		}
		ncc, chans, reqs, err := ssh.NewClientConn(conn, h.remote, h.cfg)
		if err != nil {
			conn.Close()
			return nil, err
		}
		client = ssh.NewClient(ncc, chans, reqs)
	}
	return client, nil
}

type SSH struct {
	remote  string
	command string
	ssh_cfg *ssh.ClientConfig
	chain   []hop

	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
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

	chain, _ := proxyChain(hostAddress)
	return &SSH{
		ssh_cfg: cfg,
		remote:  remote,
		command: command,
		chain:   chain,
	}
}

func (peer *SSH) Open() error {
	shard := int(connSeq.Add(1)-1) % connShards()
	client, err := sharedClient(peer.remote, peer.ssh_cfg, peer.chain, shard)
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

func (peer *SSH) ReadFrom(r io.Reader) (n int64, err error) {
	return io.Copy(peer.stdin, r)
}

func (peer *SSH) WriteTo(w io.Writer) (n int64, err error) {
	return io.Copy(w, peer.stdout)
}

