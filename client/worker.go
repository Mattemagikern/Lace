package client

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/schollz/progressbar/v3"

	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/protocol"
)

const readWindow = 32

type workerCtx struct {
	c       *protocol.Connection
	mkdir   *files.MkdirCache
	pending chan<- pendingResp
	dryrun  bool
}

type Op interface {
	Execute(w *workerCtx) error
}

type writeOp struct {
	meta       files.MetaData
	localPath  string
	remotePath string
	progress   *progressbar.ProgressBar
}

func (o *writeOp) Execute(w *workerCtx) error {
	logger := slog.Default()
	meta := o.meta
	meta.Path = o.remotePath

	err := w.c.SendFile(files.ExpandPath(o.localPath), meta, o.progress)
	if err != nil {
		if errors.Is(err, protocol.ErrLocalFile) {
			logger.Error(fmt.Sprintf("send file %s: %v", o.localPath, err))
			if o.progress != nil {
				o.progress.Add(int(meta.Size))
			}
			return nil
		}
		return fmt.Errorf("send file %s: %w", o.localPath, err)
	}
	if (meta.IsDir || meta.Mode&os.ModeSymlink != 0) && o.progress != nil {
		o.progress.Add(int(meta.Size))
	}
	logger.Info(fmt.Sprintf("<-Write: path %s, size: %d, mode:%d", o.localPath, meta.Size, meta.Mode))
	return nil
}

type readOp struct {
	localPath  string
	remotePath string
	progress   *progressbar.ProgressBar
}

func (o *readOp) Execute(w *workerCtx) error {
	if err := w.c.SendMessage(protocol.TypeRead, &protocol.ReadPayload{Path: o.remotePath}); err != nil {
		return fmt.Errorf("send read req: %w", err)
	}
	w.pending <- pendingResp{kind: pendRead, localPath: o.localPath, progress: o.progress}
	return nil
}

type deleteOp struct {
	path   string
	remote bool
}

func (o *deleteOp) Execute(w *workerCtx) error {
	logger := slog.Default()
	logger.Info(fmt.Sprintf("Delete-> path: %s", o.path))
	if w.dryrun {
		return nil
	}
	if o.remote {
		if err := w.c.SendMessage(protocol.TypeDelete, &protocol.DeletePayload{Path: o.path}); err != nil {
			return fmt.Errorf("send delete: %w", err)
		}
		return nil
	}
	if err := os.RemoveAll(o.path); err != nil {
		logger.Error(fmt.Sprintf("delete file: %v", err))
	}
	return nil
}

type indexOp struct {
	path   string
	ignore []string
	res    chan *files.FileIndex
}

func (o *indexOp) Execute(w *workerCtx) error {
	if err := w.c.SendMessage(protocol.TypeIndex, &protocol.IndexPayload{Root: o.path, Ignore: o.ignore}); err != nil {
		o.res <- nil
		return fmt.Errorf("send index req: %w", err)
	}
	w.pending <- pendingResp{kind: pendIndex, indexRes: o.res}
	return nil
}

type WorkSet struct {
	done chan struct{}
	Ops  chan Op

	mkdir files.MkdirCache
}

func MakeWorkSet(cachesize uint) *WorkSet {
	return &WorkSet{
		done: make(chan struct{}),
		Ops:  make(chan Op, cachesize),
	}
}

func (ws *WorkSet) SpawnWorkers(n int, remote, server_command string, pipe func(string, string) exec.Pipe, dryrun bool) {
	var wg sync.WaitGroup
	logger := slog.Default()
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
			ws.Worker(p, dryrun)
		}()
	}
	go func() {
		wg.Wait()
		close(ws.done)
	}()
}

func (ws *WorkSet) Wait() {
	<-ws.done
}

func (ws *WorkSet) Submit(op Op) error {
	select {
	case <-ws.done:
		return fmt.Errorf("workset closed")
	case ws.Ops <- op:
		return nil
	}
}

func (ws *WorkSet) RequestIndex(path string, ignore []string, res chan *files.FileIndex) error {
	return ws.Submit(&indexOp{path: path, ignore: ignore, res: res})
}

func (ws *WorkSet) WriteFile(meta files.MetaData, localPath, remotePath string, progress *progressbar.ProgressBar) error {
	return ws.Submit(&writeOp{meta: meta, localPath: localPath, remotePath: remotePath, progress: progress})
}

func (ws *WorkSet) ReadFile(meta files.MetaData, localPath, remotePath string, progress *progressbar.ProgressBar) error {
	return ws.Submit(&readOp{localPath: localPath, remotePath: remotePath, progress: progress})
}

func (ws *WorkSet) DeleteFile(path string, remote bool) error {
	return ws.Submit(&deleteOp{path: path, remote: remote})
}

func (ws *WorkSet) Close() {
	close(ws.Ops)
}

type pendingKind int

const (
	pendRead pendingKind = iota
	pendIndex
)

type pendingResp struct {
	kind      pendingKind
	localPath string
	indexRes  chan *files.FileIndex
	progress  *progressbar.ProgressBar
}

func (ws *WorkSet) Worker(pipe exec.Pipe, dryrun bool) {
	c := protocol.NewConnection(pipe)
	logger := slog.Default()

	pending := make(chan pendingResp, readWindow)
	receiverDone := make(chan struct{})
	connBroken := false

	go func() {
		defer close(receiverDone)
		for p := range pending {
			ty, payload, err := c.ReadMessage()
			if err != nil {
				logger.Error(fmt.Sprintf("receiver read err: %v", err))
				if p.kind == pendIndex {
					p.indexRes <- nil
				}
				continue
			}
			switch p.kind {
			case pendRead:
				if ty != protocol.TypeWrite {
					logger.Error(fmt.Sprintf("expected TypeWrite, got %v", ty))
					continue
				}
				wp := payload.(*protocol.WritePayload)
				path := files.ExpandPath(p.localPath)
				logger.Info("<-Write", "path", p.localPath, "size", wp.MetaData.Size, "mode", wp.MetaData.Mode)
				var src io.Reader = c
				if p.progress != nil {
					src = io.TeeReader(c, p.progress)
				}
				if err := files.WriteFile(path, &wp.MetaData, src, &ws.mkdir, dryrun); err != nil {
					logger.Error(fmt.Sprintf("write %s: %v", path, err))
				}
			case pendIndex:
				if ty != protocol.TypeIndex {
					logger.Error(fmt.Sprintf("expected TypeIndex, got %v", ty))
					p.indexRes <- nil
					continue
				}
				ip := payload.(*protocol.IndexPayload)
				p.indexRes <- &ip.Index
			}
		}
	}()

	defer func() {
		close(pending)
		<-receiverDone
		if !connBroken {
			if err := c.Sync(); err != nil &&
				!errors.Is(err, io.EOF) &&
				!errors.Is(err, io.ErrUnexpectedEOF) &&
				!errors.Is(err, io.ErrClosedPipe) {
				logger.Error(fmt.Sprintf("sync error:%v", err))
			}
		}
		c.Close()
	}()

	ctx := &workerCtx{
		c:       c,
		mkdir:   &ws.mkdir,
		pending: pending,
		dryrun:  dryrun,
	}

	for op := range ws.Ops {
		if err := op.Execute(ctx); err != nil {
			logger.Error(fmt.Sprintf("op failed: %v", err))
			connBroken = true
			return
		}
	}
}
