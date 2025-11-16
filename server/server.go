package server

import (
	"io"
	"os"
	"log/slog"
	"time"
	"fmt"

	"github.com/mattemagikern/lace/protocol"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/exec"
)

type Server struct {
	dry_run bool
	c *protocol.Connection
}

func NewServer(p exec.Pipe, dry_run bool) *Server {
	return &Server{
		c: protocol.NewConnection(p),
		dry_run: dry_run,
	}
}


func (w *Server) Server() error {
	logger := slog.Default()
	for {
		ty, payload, err := w.c.ReadMessage()
		if err != nil {
			if err == io.EOF  || err == io.ErrClosedPipe {
				logger.Info("Connection closed")
				return nil
			}
			return err
		}
		switch (ty) {
		case protocol.TypeIndex:
			idx := payload.(*protocol.IndexPayload)
			idx.Index, _ = files.MakeIndex(idx.Root)
			err := w.c.SendMessage(protocol.TypeIndex, idx)
			if err != nil {
				logger.Error("Failed to create index response:", err)
				return err
			}
		case protocol.TypeWrite:
			wp := payload.(*protocol.WritePayload)
			path := files.ExpandPath(wp.MetaData.Path)
			logger.Info(fmt.Sprintf("[%s]: %d %d", path, len(wp.Content), wp.MetaData.Mode))
			if w.dry_run {
				continue
			}
			if err := files.WriteFile(path, &wp.MetaData, wp.Content); err != nil {
				logger.Error(fmt.Sprintf("Failed to write file:%v", err))
			}
		case protocol.TypeRead:
			var err error
			var wp protocol.WritePayload
			rp := payload.(*protocol.ReadPayload)

			wp.MetaData, wp.Content, err = files.ReadFile(files.ExpandPath(rp.Path))
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to read file:%v", err))
				return err
			}

			wp.MetaData.Path = rp.Path
			logger.Info(fmt.Sprintf("<-Write: path %s, size: %d, mode:%d", rp.Path, wp.MetaData.Size, wp.MetaData.Mode))
			err = w.c.SendMessage(protocol.TypeWrite, &wp)
			if err != nil {
				logger.Error(fmt.Sprintf("write message error:%v", err))
				return err
			}
		case protocol.TypeDelete:
			dp := payload.(*protocol.DeletePayload)
			logger.Info(fmt.Sprintf("Delete: %v", dp.Path))
			if w.dry_run {
				continue
			}
			if err = os.RemoveAll(files.ExpandPath(dp.Path)); err != nil {
				logger.Error(fmt.Sprintf("Failed to delete file:%v", err))
			}
		case protocol.TypeSync:
			logger.Info(fmt.Sprintf("Sync %v", time.Now()))
			err := w.c.SendMessage(protocol.TypeSyncAck, &protocol.SyncPayload{
				Time: time.Now(),
				})
			if err != nil {
				return err
			}
		default:
			logger.Warn("Unknown message type:", ty)
		}
	}

	return fmt.Errorf("unreachable")
}
