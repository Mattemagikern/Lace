package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/protocol"
)

type handler func(payload interface{}) error

type Server struct {
	dry_run  bool
	c        *protocol.Connection
	mkdir    files.MkdirCache
	handlers map[protocol.MessageType]handler
}

func NewServer(p exec.Pipe, dry_run bool) *Server {
	s := &Server{
		c:       protocol.NewConnection(p),
		dry_run: dry_run,
	}
	s.handlers = map[protocol.MessageType]handler{
		protocol.TypeIndex:  s.handleIndex,
		protocol.TypeWrite:  s.handleWrite,
		protocol.TypeRead:   s.handleRead,
		protocol.TypeDelete: s.handleDelete,
		protocol.TypeSync:   s.handleSync,
	}
	return s
}

func (w *Server) Server() error {
	logger := slog.Default()
	defer w.c.Close()
	for {
		ty, payload, err := w.c.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				logger.Info("Connection closed")
				return nil
			}
			return err
		}
		h, ok := w.handlers[ty]
		if !ok {
			logger.Warn("Unknown message type", "type", ty)
			continue
		}
		if err := h(payload); err != nil {
			return err
		}
	}
}

func (w *Server) handleIndex(payload interface{}) error {
	logger := slog.Default()
	logger.Info(fmt.Sprintf("Index request received:%v", time.Now()))
	idx := payload.(*protocol.IndexPayload)
	idx.Index, _ = files.MakeIndex(idx.Root)
	if err := w.c.SendMessage(protocol.TypeIndex, idx); err != nil {
		logger.Error(fmt.Sprintf("send index response: %v", err))
		return err
	}
	return nil
}

func (w *Server) handleWrite(payload interface{}) error {
	wp := payload.(*protocol.WritePayload)
	path := files.ExpandPath(wp.MetaData.Path)
	slog.Default().Info(fmt.Sprintf("[%s]: %d %d", path, wp.MetaData.Size, wp.MetaData.Mode))
	if err := files.WriteFile(path, &wp.MetaData, w.c, &w.mkdir, w.dry_run); err != nil {
		slog.Default().Error(fmt.Sprintf("write %s: %v", path, err))
	}
	return nil
}

func (w *Server) handleRead(payload interface{}) error {
	rp := payload.(*protocol.ReadPayload)
	path := files.ExpandPath(rp.Path)
	meta, err := files.MakeMetaData(path)
	if err != nil {
		return fmt.Errorf("metadata %s: %w", path, err)
	}
	meta.Path = rp.Path
	if err := w.c.SendFile(path, meta, nil); err != nil {
		return fmt.Errorf("send %s: %w", path, err)
	}
	return w.c.Flush()
}

func (w *Server) handleDelete(payload interface{}) error {
	dp := payload.(*protocol.DeletePayload)
	slog.Default().Info(fmt.Sprintf("Delete: %v", dp.Path))
	if w.dry_run {
		return nil
	}
	if err := os.RemoveAll(files.ExpandPath(dp.Path)); err != nil {
		return fmt.Errorf("delete %s: %w", dp.Path, err)
	}
	return nil
}

func (w *Server) handleSync(payload interface{}) error {
	return w.c.SendMessage(protocol.TypeSyncAck, &protocol.SyncPayload{Time: time.Now()})
}
