package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/mattemagikern/lace/cfg"
	"github.com/mattemagikern/lace/client"
	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/exec/osexec"
	"github.com/mattemagikern/lace/exec/ssh"
	"github.com/mattemagikern/lace/server"
)

var (
	logger = slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{
			Level: slog.LevelError,
		}))
	server_command = "lace --server"

	numCPU    = runtime.NumCPU()
	cacheSize = numCPU * 10
	ws        = client.WorkSet{
		Delete: make(chan client.DeleteOp, cacheSize),
		Write:  make(chan client.Fileop, cacheSize),
		Read:   make(chan client.Fileop, cacheSize),
		Index:  make(chan client.IndexOp),
	}
)

func spawnWorkers(n int, remote, server_command string, pipe func(string, string) exec.Pipe) *sync.WaitGroup {
	var wg sync.WaitGroup
	logger.Info(fmt.Sprintf("spawning %d workers...", n))
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := pipe(remote, server_command)
			if err := p.Open(); err != nil {
				logger.Error(fmt.Sprintf("failed to open pipe: %v", err))
				return
			}
			defer p.Close()
			client.Worker(&ws, p, cfg.Dryrun)
		}()
	}
	return &wg
}

func main() {
	args, err := cfg.Make()
	if err != nil {
		fmt.Println(err)
		return
	}

	if cfg.Version {
		fmt.Println(cfg.VERSION)
		return
	}

	if cfg.Help {
		cfg.PrintHelpAndExit()
	}

	if cfg.Verbose {
		logger = slog.New(slog.NewTextHandler(os.Stderr,
			&slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))
		server_command += " --verbose"
	}
	slog.SetDefault(logger)

	if cfg.Dryrun {
		logger.Info("performing dry run...")
		server_command += " --dryrun"
	}

	if cfg.Server {
		logger.Info("starting server instance...")
		w := server.NewServer(exec.NewStdPipe(), cfg.Dryrun)
		if err := w.Server(); err != nil {
			logger.Error(fmt.Sprintf("%v", err))
		}
		logger.Info("server instance exiting...")
		return
	}

	if cfg.Install {
		if len(args) < 1 {
			fmt.Println("remote destination required for install")
			return
		}
		remote, dest := ssh.ParseDestination(args[0])
		if dest == "" {
			dest = "~/.local/bin/lace"
		}

		exePath, _ := os.Executable()
		logger.Info(fmt.Sprintf("installing lace to %s:%s", remote, dest))
		err := ssh.Upload(remote, exePath, dest)
		if err != nil {
			logger.Error(fmt.Sprintf("%v", err))
		}
		return
	}

	if len(args) < 2 {
		fmt.Println("Not enough arguments")
		return
	}

	from := args[0]
	to := args[1]

	if strings.Contains(from, ":") && strings.Contains(to, ":") {
		logger.Error("both source and target cannot be remote")
		return
	}

	push := true
	var src, dest, remote string
	var pipe func(string, string) exec.Pipe
	if !strings.Contains(from, ":") && !strings.Contains(to, ":") {
		pipe = osexec.NewPipe
		src = from
		remote, dest = to, to
	} else {
		pipe = ssh.NewPipe
		if strings.Contains(from, ":") {
			remote = from
			src = to
			push = false
		} else {
			remote = to
			src = from
		}
		remote, dest = ssh.ParseDestination(remote)
	}

	if os.Lstat(src); err != nil {
		fmt.Printf("source path does not exist: %s\n", from)
		return
	}

	wg := spawnWorkers(cfg.Jobs, remote, server_command, pipe)
	defer wg.Wait()
	defer ws.Close()
	c, err := client.NewClient(src, dest, &ws)
	if err != nil {
		fmt.Printf("failed to create client: %v\n", err)
		return
	}

	if push {
		c.Push()
	} else {
		c.Pull()
	}
}
