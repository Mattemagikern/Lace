package server

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/protocol"
)

type Server struct {
	dry_run bool
	c       *protocol.Connection
}

func NewServer(p exec.Pipe, dry_run bool) *Server {
	return &Server{
		c:       protocol.NewConnection(p),
		dry_run: dry_run,
	}
}

func (w *Server) Server() error {
	logger := slog.Default()
	defer w.c.Close()
	for {
		ty, payload, err := w.c.ReadMessage()
		if err != nil {
			if err == io.EOF || err == io.ErrClosedPipe {
				logger.Info("Connection closed")
				return nil
			}
			return err
		}
		switch ty {
		case protocol.TypeIndex:
			logger.Info(fmt.Sprintf("Index request received:%v", time.Now()))
			idx := payload.(*protocol.IndexPayload)
			idx.Index, _ = files.MakeIndex(idx.Root)
			logger.Info(fmt.Sprintf("Index request sent:%v", time.Now()))
			err := w.c.SendMessage(protocol.TypeIndex, idx)
			if err != nil {
				logger.Error("Failed to create index response:", err)
				return err
			}
		case protocol.TypeWrite:
			wp := payload.(*protocol.WritePayload)
			path := files.ExpandPath(wp.MetaData.Path)
			logger.Info(fmt.Sprintf("[%s]: %d %d", path, wp.MetaData.Size, wp.MetaData.Mode))
			if w.dry_run {
				continue
			}
			if err := files.WriteFile(path, &wp.MetaData, w.c); err != nil {
				logger.Error(fmt.Sprintf("Failed to write file:%v", err))
				return err
			}
		case protocol.TypeRead:
			var err error
			var wp protocol.WritePayload
			rp := payload.(*protocol.ReadPayload)
			path := files.ExpandPath(rp.Path)
			wp.MetaData, err = files.MakeMetaData(path)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to make metadata:%v", err))
				return err
			}
			wp.MetaData.Path = rp.Path

			err = w.c.SendMessage(protocol.TypeWrite, &wp)
			if err != nil {
				logger.Error(fmt.Sprintf("write message error:%v", err))
				return err
			}
			logger.Info(fmt.Sprintf("<-Write: path %s, size: %d, mode:%d", rp.Path, wp.MetaData.Size, wp.MetaData.Mode))
			if (wp.MetaData.Mode&os.ModeDir) != 0 || wp.MetaData.Link != "" {
				continue
			}
			if err := files.ReadFile(path, w.c); err != nil {
				logger.Error(fmt.Sprintf("Failed to write file:%v", err))
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
				return err
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
