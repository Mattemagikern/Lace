package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	charmlog "github.com/charmbracelet/log"

	"github.com/mattemagikern/lace/cfg"
	"github.com/mattemagikern/lace/client"
	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/exec/osexec"
	"github.com/mattemagikern/lace/exec/ssh"
	"github.com/mattemagikern/lace/server"
)

func newLogger(level charmlog.Level) *slog.Logger {
	return slog.New(charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		Level:           level,
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
	}))
}

var (
	logger         = newLogger(charmlog.ErrorLevel)
	server_command = "lace --server"

	numCPU    = runtime.NumCPU() * 2
	cacheSize = numCPU * 5
	ws        = client.MakeWorkSet(uint(cacheSize))
)

func main() {
	os.Exit(run())
}

func run() int {
	args, err := cfg.Make()
	if err != nil {
		fmt.Println(err)
		return 1
	}

	if cfg.Version {
		fmt.Println(cfg.VERSION)
		return 0
	}

	if cfg.Help {
		cfg.PrintHelpAndExit()
	}

	if cfg.Verbose {
		logger = newLogger(charmlog.DebugLevel)
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
			return 1
		}
		logger.Info("server instance exiting...")
		return 0
	}

	if len(args) < 2 {
		fmt.Println("Not enough arguments")
		return 1
	}

	from := args[0]
	to := args[1]

	if strings.Contains(from, ":") && strings.Contains(to, ":") {
		logger.Error("both source and target cannot be remote")
		return 1
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

	if _, err := os.Lstat(src); err != nil {
		fmt.Printf("source path does not exist: %s\n", from)
		return 1
	}

	ws.SpawnWorkers(cfg.Jobs, remote, server_command, pipe, cfg.Dryrun)
	defer ws.Wait()
	defer ws.Close()
	c, err := client.NewClient(src, dest, ws)
	if err != nil {
		fmt.Printf("failed to create client: %v\n", err)
		return 1
	}

	if push {
		err = c.Push()
	} else {
		err = c.Pull()
	}

	if err != nil {
		fmt.Printf("file transfer failed: %v\n", err)
		return 1
	}
	return 0
}
