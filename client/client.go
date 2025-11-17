package client

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/mattemagikern/lace/files"
)

type Client struct {
	root        string
	remote_root string

	ws           *WorkSet
	index        files.FileIndex
	remote_index files.FileIndex
}

func NewClient(root, remote_root string, ws *WorkSet) (*Client, error) {
	var err error
	logger := slog.Default()
	c := &Client{
		root:        root,
		remote_root: remote_root,
		ws:          ws,
	}

	res := make(chan *files.FileIndex)
	go ws.RequestIndex(c.remote_root, res)

	logger.Info(fmt.Sprintf("local index start:%v\n", time.Now()))
	c.index, err = files.MakeIndex(c.root)
	if err != nil {
		c.index = make(files.FileIndex)
	}
	logger.Info(fmt.Sprintf("local index end:%v\n", time.Now()))
	remote_index := <-res
	if remote_index == nil {
		return nil, fmt.Errorf("Failed to get remote index")
	}
	c.remote_index = *remote_index
	return c, nil
}

func (c *Client) Push() error {
	changed, deleted := files.PushFilterFiles(c.index, c.remote_index)
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))
	if (len(changed) + len(deleted)) == 0 {
		return nil
	}
	byteCount := 0
	for _, k := range changed {
		byteCount += int(k.Size)
	}

	c.ws.Progress = newProgressBar(byteCount, "Upload progress:")
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
	if (len(changed) + len(deleted)) == 0 {
		return nil
	}
	byteCount := 0
	for _, k := range changed {
		byteCount += int(k.Size)
	}
	c.ws.Progress = newProgressBar(byteCount, "Download progress:")
	for _, k := range deleted {
		c.ws.DeleteFile(fmt.Sprintf("%s/%s", c.root, k.Path), false)
	}

	for _, k := range changed {
		c.ws.ReadFile(fmt.Sprintf("%s/%s", c.root, k.Path), fmt.Sprintf("%s/%s", c.remote_root, k.Path))
	}

	return nil
}
