package ssh

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"golang.org/x/term"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"io/fs"

	"github.com/mattemagikern/lace/files"
	"golang.org/x/crypto/ssh"
)

type SSHConfig struct {
	Host         []string
	HostName     string
	User         string
	Port         string
	IdentityFile []string
}

func parseIdentityFiles(identities []string) []ssh.AuthMethod {
	var authMethods []ssh.AuthMethod
	for _, file := range identities {
		keyPath := files.ExpandPath(file)
		key, err := os.ReadFile(keyPath)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	return authMethods
}

func defaultKeys() []ssh.AuthMethod {
	var keys []string
	_ = filepath.WalkDir(filepath.Join(os.Getenv("HOME"), ".ssh"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), "id_") && !strings.HasSuffix(d.Name(), ".pub") {
			keys = append(keys, path)
		}
		return nil
	})
	return parseIdentityFiles(keys)
}

func parseSSHConfig(sshconfig []byte) (map[string]*SSHConfig, error) {
	configs := make(map[string]*SSHConfig)
	scanner := bufio.NewScanner(bytes.NewReader(sshconfig))
	var current *SSHConfig

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		value := strings.Join(fields[1:], " ")

		switch key {
		case "host":
			current = &SSHConfig{
				Host: strings.Fields(value),
			}
			for _, h := range current.Host {
				configs[h] = current
			}
		default:
			if current == nil {
				continue
			}
			switch key {
			case "hostname":
				current.HostName = value
			case "user":
				current.User = value
			case "port":
				current.Port = value
			case "identityfile":
				current.IdentityFile = append(current.IdentityFile, files.ExpandPath(value))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return configs, nil
}

func readSSHConfigFile() ([]byte, error) {
	file, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ssh", "config"))
	if err != nil {
		return nil, err
	}
	return file, nil
}

func passwordCallbackPrompt(prompt string) ssh.AuthMethod {
	var once sync.Once
	var pw string
	var pwErr error

	cb := func() (string, error) {
		once.Do(func() {
			// Print prompt and read without echo
			fmt.Fprint(os.Stderr, prompt)
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr) // newline after the prompt
			if err != nil {
				pwErr = err
				return
			}
			pw = string(b)
			for i := range b {
				b[i] = 0
			}
		})
		if pwErr != nil {
			return "", pwErr
		}
		if pw == "" {
			return "", errors.New("empty password")
		}
		return pw, nil
	}

	return ssh.PasswordCallback(cb)
}

func getConfigs() (map[string]*SSHConfig, error) {
	data, err := readSSHConfigFile()
	if err != nil {
		return nil, err
	}
	configs, err := parseSSHConfig(data)
	if err != nil {
		return nil, err
	}
	return configs, nil
}

func expandHostAddress(hostAddress, port string) string {
	cfgs, err := getConfigs()
	if err != nil {
		return hostAddress
	}
	if conf, ok := cfgs[hostAddress]; ok {
		if conf.HostName != "" {
			hostAddress = conf.HostName
		}
		if port == "" {
			port = conf.Port
		}
	}
	if port == "" {
		port = "22"
	}
	return fmt.Sprintf("%s:%s", hostAddress, port)
}

var (
	fastCiphers = []string{ssh.CipherAES128GCM, ssh.CipherAES256GCM, ssh.CipherChaCha20Poly1305}
	fastMacs    = []string{ssh.HMACSHA256ETM, ssh.HMACSHA512ETM}
)

func MakeSSHConfig(hostAddress string) (*ssh.ClientConfig, error) {
	configs, err := getConfigs()
	if err != nil {
		return nil, err
	}
	algorithms := ssh.SupportedAlgorithms()
	hostKeyCB, err := verifyingHostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("host key verification setup failed: %w", err)
	}
	if conf, ok := configs[hostAddress]; ok {
		sshConfig := &ssh.ClientConfig{
			HostKeyAlgorithms: algorithms.HostKeys,
			Config: ssh.Config{
				KeyExchanges: algorithms.KeyExchanges,
				Ciphers:      fastCiphers,
				MACs:         fastMacs,
			},
		}
		if conf.User != "" {
			sshConfig.User = conf.User
		} else {
			sshConfig.User = os.Getenv("USER")
		}

		if len(conf.IdentityFile) == 0 {
			sshConfig.Auth = defaultKeys()
		} else {
			sshConfig.Auth = parseIdentityFiles(conf.IdentityFile)
		}

		sshConfig.HostKeyCallback = hostKeyCB
		return sshConfig, nil
	}
	return &ssh.ClientConfig{
		HostKeyCallback:   hostKeyCB,
		User:              os.Getenv("USER"),
		Auth:              append(defaultKeys(), passwordCallbackPrompt("Password: ")),
		HostKeyAlgorithms: algorithms.HostKeys,
		Config: ssh.Config{
			KeyExchanges: algorithms.KeyExchanges,
			Ciphers:      fastCiphers,
			MACs:         fastMacs,
		},
	}, nil
}
