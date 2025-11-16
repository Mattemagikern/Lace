package main

import (
	"fmt"
	"os"
	"strings"
	"log/slog"
	"runtime"
	"sync"

	"github.com/mattemagikern/lace/cfg"
	"github.com/mattemagikern/lace/server"
	"github.com/mattemagikern/lace/client"
	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/exec/ssh"
	"github.com/mattemagikern/lace/exec/osexec"
)

var (
	logger 		= slog.New(slog.NewTextHandler(os.Stderr,
				&slog.HandlerOptions{
					Level: slog.LevelError,
				}))
	server_command 	= "lace --server"

	numCPU = runtime.NumCPU()
	cacheSize = numCPU * 10
	ws = client.WorkSet {
		Delete: make(chan client.DeleteOp, cacheSize),
		Write: make(chan client.Fileop, cacheSize),
		Read: make(chan client.Fileop, cacheSize),
		Done: make(chan bool, numCPU),
	}
)

func spawnWorkers(n int, remote, server_command string, pipe func(string, string) exec.Pipe) *sync.WaitGroup {
    	var wg sync.WaitGroup
	for range(n) {
		wg.Go(func() {
			p := pipe(remote, server_command)
			if err := p.Open(); err != nil {
				logger.Error(fmt.Sprintf("failed to open pipe: %v", err))
				return
			}
			defer p.Close()
			client.Worker(&ws, p, cfg.Dryrun);
		})
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
		if (len(args) < 1) {
			fmt.Println("remote destination required for install")
			return
		}
		remote, dest := ssh.ParseDestination(args[0])
		if dest == "~" || dest == "" {
			dest = "~/.local/bin/lace"
		}
		logger.Info(fmt.Sprintf("installing lace on remote: %s:%s", remote, dest))
		wg := spawnWorkers(1, remote, server_command, ssh.NewPipe)
		exePath, _ := os.Executable()
		ws.WriteFile(exePath, dest)
		ws.Close()
		wg.Wait()
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
		fmt.Println("source path does not exist: %s", from)
		return
	}

	p := pipe(remote, server_command)
	if err := p.Open(); err != nil {
		logger.Error(fmt.Sprintf("failed to open pipe: %v", err))
		return
	}
	defer p.Close()
	c, err := client.NewClient(src, dest, p, &ws)
	if err != nil {
		fmt.Println("failed to create client: %v", err)
		return
	}
	defer c.Close()
	wg := spawnWorkers(numCPU, remote, server_command, pipe)

	if push {
		c.Push()
	} else {
		c.Pull()
	}
	ws.Close()

	logger.Info("waiting for workers to finish...")
	/* cleanup */
	wg.Wait()
}
