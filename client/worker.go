package client

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/protocol"
)

type DeleteOp struct {
	remote bool
	path   string
}

type Fileop struct {
	path        string
	remote_path string
}

type IndexOp struct {
	path string
	res  chan *files.FileIndex
}

type WorkSet struct {
	Progress *progressbar.ProgressBar

	Index  chan IndexOp
	Delete chan DeleteOp
	Write  chan Fileop
	Read   chan Fileop
}

func (ws *WorkSet) RequestIndex(path string, res chan *files.FileIndex) {
	ws.Index <- IndexOp{path, res}
}

func (ws *WorkSet) WriteFile(local_path, remote_path string) {
	ws.Write <- Fileop{local_path, remote_path}
}

func (ws *WorkSet) ReadFile(local_path, remote_path string) {
	ws.Read <- Fileop{local_path, remote_path}
}

func (ws *WorkSet) DeleteFile(path string, remote bool) {
	ws.Delete <- DeleteOp{remote, path}
}

func (ws *WorkSet) Close() {
	close(ws.Delete)
	close(ws.Write)
	close(ws.Read)
	close(ws.Index)
}

func Worker(ws *WorkSet, pipe exec.Pipe, dryrun bool) {
	del := ws.Delete
	read := ws.Read
	write := ws.Write
	index := ws.Index

	c := protocol.NewConnection(pipe)
	logger := slog.Default()
	for del != nil || read != nil || write != nil || index != nil {
		select {
		case idx, ok := <-index:
			if !ok {
				index = nil
				continue
			}
			logger.Info(fmt.Sprintf("remote index start:%v\n", time.Now()))
			err := c.SendMessage(protocol.TypeIndex,
				&protocol.IndexPayload{
					Root: idx.path,
				})
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to request index response: %v", err))
				idx.res <- nil
				continue
			}
			ty, payload, err := c.ReadMessage()
			if err != nil {
				logger.Error(fmt.Sprintf("remote index: Failed to read message: %v", err))
				idx.res <- nil
				continue
			}
			if ty != protocol.TypeIndex {
				logger.Error("Out of sequence message type")
				idx.res <- nil
				continue
			}
			respkg, ok := payload.(*protocol.IndexPayload)
			idx.res <- &respkg.Index
			logger.Info(fmt.Sprintf("remote index end:%v\n", time.Now()))
		case fp, ok := <-del:
			if !ok {
				del = nil
				continue
			}
			logger.Info(fmt.Sprintf("Delete-> path: %s", fp.path))
			if dryrun {
				continue
			}
			if fp.remote {
				err := c.SendMessage(protocol.TypeDelete, &protocol.DeletePayload{
					Path: fp.path,
				})
				if err != nil {
					logger.Error(fmt.Sprintf("write delete message err:%v", err))
					continue
				}
			} else {
				err := os.RemoveAll(fp.path)
				if err != nil {
					logger.Error(fmt.Sprintf("delete file err:%v", err))
					continue
				}
			}
		case fp, ok := <-read:
			if !ok {
				read = nil
				continue
			}
			err := c.SendMessage(protocol.TypeRead, &protocol.ReadPayload{
				Path: fp.remote_path,
			})
			if err != nil {
				logger.Error(fmt.Sprintf("make read message err: %v", err))
				continue
			}
			ty, payload, err := c.ReadMessage()
			if err != nil {
				logger.Error(fmt.Sprintf("read message err:%v", err))
				continue
			}
			if ty != protocol.TypeWrite {
				logger.Error(fmt.Sprintf("unexpected response type got: %v", ty))
				continue
			}

			wp := payload.(*protocol.WritePayload)
			path := files.ExpandPath(fp.path)
			logger.Info("<-Write", "path", fp.path, "size", wp.MetaData.Size, "mode", wp.MetaData.Mode)
			if dryrun {
				ws.Progress.Add(int(wp.MetaData.Size))
				continue
			}

			if err := files.WriteFile(path, &wp.MetaData, io.MultiReader(c, ws.Progress)); err != nil {
				logger.Error(fmt.Sprintf("Failed to write file:%v", err))
			}
		case fop, ok := <-write:
			if !ok {
				write = nil
				continue
			}
			var wp protocol.WritePayload
			var err error
			path := files.ExpandPath(fop.path)

			wp.MetaData, err = files.MakeMetaData(path)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to make metadata:%v", err))
				return
			}
			wp.MetaData.Path = fop.remote_path

			err = c.SendMessage(protocol.TypeWrite, &wp)
			if err != nil {
				logger.Error(fmt.Sprintf("write message error:%v", err))
				return
			}
			logger.Info(fmt.Sprintf("<-Write: path %s, size: %d, mode:%d", fop.path, wp.MetaData.Size, wp.MetaData.Mode))
			if (wp.MetaData.Mode&os.ModeDir) != 0 || wp.MetaData.Link != "" {
				logger.Info("skipping data transfer for directory or symlink")
				ws.Progress.Add(int(wp.MetaData.Size))
				continue
			}
			if err := files.ReadFile(path, io.MultiWriter(c, ws.Progress)); err != nil {
				logger.Error(fmt.Sprintf("Failed to write file:%v", err))
				return
			}
		}
	}
	if err := c.Sync(); err != nil {
		logger.Error(fmt.Sprintf("sync error:%v", err))
	}
}
