package ssh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	hostKeyMu       sync.Mutex
	hostKeyCallback ssh.HostKeyCallback
	hostKeyPath     string
)

func knownHostsPath() string {
	return filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
}

func loadKnownHosts() (ssh.HostKeyCallback, string, error) {
	path := knownHostsPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, path, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, path, err
		}
		f.Close()
	}
	cb, err := knownhosts.New(path)
	return cb, path, err
}

func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	addrs := []string{knownhosts.Normalize(hostname)}
	if remote != nil {
		if h := knownhosts.Normalize(remote.String()); h != addrs[0] {
			addrs = append(addrs, h)
		}
	}
	line := knownhosts.Line(addrs, key) + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func promptYesNo(tty io.ReadWriter, prompt string) (bool, error) {
	fmt.Fprint(tty, prompt)
	answer, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "yes", "y":
		return true, nil
	}
	return false, nil
}

func openTTY() (*os.File, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open /dev/tty for host key prompt: %w", err)
	}
	return tty, nil
}

func promptAcceptHostKey(hostname string, key ssh.PublicKey) (bool, error) {
	tty, err := openTTY()
	if err != nil {
		return false, err
	}
	defer tty.Close()

	fmt.Fprintf(tty, "The authenticity of host '%s' can't be established.\n", hostname)
	fmt.Fprintf(tty, "%s key fingerprint is %s.\n", key.Type(), ssh.FingerprintSHA256(key))
	return promptYesNo(tty, "Are you sure you want to continue connecting (yes/no)? ")
}

func promptReplaceHostKey(hostname string, key ssh.PublicKey, want []knownhosts.KnownKey) (bool, error) {
	tty, err := openTTY()
	if err != nil {
		return false, err
	}
	defer tty.Close()

	fmt.Fprintln(tty, "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@")
	fmt.Fprintln(tty, "@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @")
	fmt.Fprintln(tty, "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@")
	fmt.Fprintf(tty, "Host '%s' presented a %s key with fingerprint:\n  %s\n",
		hostname, key.Type(), ssh.FingerprintSHA256(key))
	fmt.Fprintln(tty, "Existing known_hosts entries for this host/address:")
	for _, k := range want {
		fmt.Fprintf(tty, "  %s %s (%s:%d)\n",
			k.Key.Type(), ssh.FingerprintSHA256(k.Key), k.Filename, k.Line)
	}
	fmt.Fprintln(tty, "If this is expected (e.g. server reinstall, key rotation, IP reuse on a")
	fmt.Fprintln(tty, "different port), you may replace the conflicting entries.")
	return promptYesNo(tty, "Replace the conflicting entries and continue (yes/no)? ")
}

func removeKnownHostsLines(want []knownhosts.KnownKey) error {
	byFile := map[string]map[int]bool{}
	for _, k := range want {
		if byFile[k.Filename] == nil {
			byFile[k.Filename] = map[int]bool{}
		}
		byFile[k.Filename][k.Line] = true
	}
	for path, lines := range byFile {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var out strings.Builder
		for i, line := range strings.SplitAfter(string(data), "\n") {
			if lines[i+1] {
				continue
			}
			out.WriteString(line)
		}
		if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func verifyingHostKeyCallback() (ssh.HostKeyCallback, error) {
	hostKeyMu.Lock()
	defer hostKeyMu.Unlock()

	if hostKeyCallback != nil {
		return wrapHostKeyCallback(hostKeyCallback, hostKeyPath), nil
	}

	cb, path, err := loadKnownHosts()
	if err != nil {
		return nil, err
	}
	hostKeyCallback = cb
	hostKeyPath = path
	return wrapHostKeyCallback(cb, path), nil
}

func wrapHostKeyCallback(base ssh.HostKeyCallback, path string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		hostKeyMu.Lock()
		defer hostKeyMu.Unlock()

		if cb, _, lerr := loadKnownHosts(); lerr == nil {
			if rerr := cb(hostname, remote, key); rerr == nil {
				hostKeyCallback = cb
				return nil
			} else {
				var ke2 *knownhosts.KeyError
				if errors.As(rerr, &ke2) {
					ke = ke2
				}
			}
		}

		if len(ke.Want) > 0 {
			ok, perr := promptReplaceHostKey(hostname, key, ke.Want)
			if perr != nil {
				return perr
			}
			if !ok {
				return fmt.Errorf("host key verification failed for %s: remote key (%s %s) does not match known_hosts entry",
					hostname, key.Type(), ssh.FingerprintSHA256(key))
			}
			if err := removeKnownHostsLines(ke.Want); err != nil {
				return fmt.Errorf("failed to remove stale known_hosts entries: %w", err)
			}
		} else {
			ok, perr := promptAcceptHostKey(hostname, key)
			if perr != nil {
				return perr
			}
			if !ok {
				return errors.New("host key rejected by user")
			}
		}

		if err := appendKnownHost(path, hostname, remote, key); err != nil {
			return fmt.Errorf("failed to record host key: %w", err)
		}
		if cb, _, lerr := loadKnownHosts(); lerr == nil {
			hostKeyCallback = cb
		}
		return nil
	}
}
