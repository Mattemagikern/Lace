package client

import (
	"fmt"
	"os"
	"log/slog"

	"github.com/mattemagikern/lace/protocol"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/exec"
)

type DeleteOp struct {
	remote bool
	path string
}

type Fileop struct {
	path string
	remote_path string
}

type WorkSet struct {
	Delete	chan DeleteOp
	Write 	chan Fileop
	Read  	chan Fileop
	Done 	chan bool
}

func (ws *WorkSet) WriteFile(local_path, remote_path string) {
	ws.Write <- Fileop{local_path, remote_path}
}

func (ws *WorkSet) ReadFile(remote_path, local_path string) {
	ws.Read <- Fileop{local_path, remote_path}
}

func (ws *WorkSet) DeleteFile(path string, remote bool) {
	ws.Delete <- DeleteOp{remote, path}
}

func (ws *WorkSet) Close() {
	close(ws.Delete)
	close(ws.Write)
	close(ws.Read)
}


func Worker(ws *WorkSet, pipe exec.Pipe, dryrun bool) {
	del := ws.Delete
	read := ws.Read
	write := ws.Write

	c := protocol.NewConnection(pipe)
	logger := slog.Default()
	for del != nil || read != nil || write != nil {
		select {
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
			logger.Info("<-Write", "path", fp.path, "size", len(wp.Content), "mode", wp.MetaData.Mode)
			if dryrun {
				continue
			}

			if err := files.WriteFile(path, &wp.MetaData, wp.Content); err != nil {
				logger.Error(fmt.Sprintf("Failed to write file:%v", err))
			}
		case fop, ok:= <-write:
			if !ok {
				write = nil
				continue
			}

			metadata, content, err := files.ReadFile(fop.path)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to read file:%v", err))
				continue
			}
			metadata.Path = fop.remote_path
			logger.Info(fmt.Sprintf("Write->path %s(%s), size: %d, mode:%d", fop.path, fop.remote_path, len(content), metadata.Mode))
			err = c.SendMessage(protocol.TypeWrite, &protocol.WritePayload{
				MetaData: metadata,
				Content: content,
			})
			if err != nil {
				logger.Error(fmt.Sprintf("write message error:%v", err))
				continue
			}
		}
	}
	if err := c.Sync(); err != nil {
		logger.Error(fmt.Sprintf("sync error:%v", err))
	}
}
