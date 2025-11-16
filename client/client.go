package client

import (
	"io"
	"fmt"
	"log/slog"

	"github.com/mattemagikern/lace/protocol"
	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/exec"
)


type Client struct {
	root string
	remote_root string
	c *protocol.Connection

	ws *WorkSet
	index files.FileIndex
	remote_index files.FileIndex
}


func NewClient(root, remote_root string, pipe exec.Pipe, ws *WorkSet) (*Client, error) {
	c := &Client{
		root: root,
		remote_root: remote_root,
		ws: ws,
		c: protocol.NewConnection(pipe),
	}

	var err error
	indexchan := make(chan files.FileIndex)

	go c.RemoteIndex(c.remote_root, indexchan)

	c.index, err = files.MakeIndex(c.root)
	if err != nil {
		c.index = make(files.FileIndex)
	}
	remote_index := <-indexchan
	if remote_index == nil {
		return nil, fmt.Errorf("Failed to get remote index")
	}
	c.remote_index = remote_index
	return c, nil
}

func (c *Client) Close() error {
	if err := c.c.Sync(); err != nil {
		return err
	}
	return nil
}


func (c *Client)RemoteIndex(remote_root string, result chan files.FileIndex) {
	logger := slog.Default()
	err := c.c.SendMessage(protocol.TypeIndex,
		&protocol.IndexPayload{
			Root:  remote_root,
		})
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to request index response: %v", err))
		close(result)
		return
	}
	ty, payload, err := c.c.ReadMessage()
	if err != nil {
		if err == io.EOF  || err == io.ErrClosedPipe {
			logger.Info("Connection closed")
			close(result)
			return
		}
		logger.Error(fmt.Sprintf("remote index: Failed to read message: %v", err))
		close(result)
		return
	}
	if ty != protocol.TypeIndex {
		logger.Error("Out of sequence message type")
		close(result)
		return
	}
	idx := payload.(*protocol.IndexPayload)
	result <- idx.Index
}

func (c *Client) Push() error {
	changed, deleted := files.PushFilterFiles(c.index, c.remote_index)
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))

	for _, k := range deleted {
		c.ws.DeleteFile(fmt.Sprintf("%s/%s", c.remote_root, k.Path), true)
	}

	for _, k := range changed {
		c.ws.WriteFile(fmt.Sprintf("%s/%s", c.root, k.Path), fmt.Sprintf("%s/%s", c.remote_root, k.Path))
	}

	return nil
}

func (c *Client) Pull() error {
	changed, deleted := files.PullFilterFiles(c.index, c.remote_index)
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))

	for _, k := range deleted {
		c.ws.DeleteFile(fmt.Sprintf("%s/%s", c.remote_root, k.Path), false)
	}

	for _, k := range changed {
		c.ws.ReadFile(fmt.Sprintf("%s/%s", c.root, k.Path), fmt.Sprintf("%s/%s", c.remote_root, k.Path))
	}

	return nil
}
