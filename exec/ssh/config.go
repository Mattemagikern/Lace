package ssh

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"fmt"
	"errors"
	"golang.org/x/term"

	"io/fs"

	"golang.org/x/crypto/ssh"
//	"golang.org/x/crypto/ssh/knownhosts"
	"github.com/mattemagikern/lace/files"
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

func ParseSSHTarget(target string) (user, host, port string) {
	port = ""

	if atSplit := strings.SplitN(target, "@", 2); len(atSplit) == 2 {
		user = atSplit[0]
		target = atSplit[1]
	}

	if colonSplit := strings.SplitN(target, ":", 2); len(colonSplit) == 2 {
		host = colonSplit[0]
		port = colonSplit[1]
	} else {
		host = target
	}

	return user, host, port
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
			// copy to string (required by ssh.PasswordCallback)
			pw = string(b)

			// zero the byte slice asap
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

func MakeSSHConfig(hostAddress string) (*ssh.ClientConfig, error) {
	configs, err := getConfigs()
	if err != nil {
		return nil, err
	}
	if conf, ok := configs[hostAddress]; ok {
		sshConfig := &ssh.ClientConfig{}
		if conf.User != "" {
			sshConfig.User = conf.User
		} else {
			sshConfig.User = os.Getenv("USER") // fallback
		}

		if len(conf.IdentityFile) == 0 {
			sshConfig.Auth = defaultKeys()
		} else {
			sshConfig.Auth = parseIdentityFiles(conf.IdentityFile)
		}

		/*
		hostKeyCallback, err := knownhosts.New(filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts"))
		if err != nil {
			return nil, err
		}
		sshConfig.HostKeyCallback = hostKeyCallback
		*/

		sshConfig.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		return sshConfig, nil
	}
	return &ssh.ClientConfig {
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		User: os.Getenv("USER"),
		Auth: append(defaultKeys(), passwordCallbackPrompt("Password: ")),
	}, nil
}
