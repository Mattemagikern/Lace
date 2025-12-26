package client

import (
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
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

func sortedKeys(idx files.FileIndex) []string {
	keys := make([]string, 0, len(idx))
	for k := range idx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *Client) Push() error {
	changed, deleted := files.FilterFiles(c.index, c.remote_index)
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))
	if (len(changed) + len(deleted)) == 0 {
		return nil
	}

	for _, path := range sortedKeys(deleted) {
		if err := c.ws.DeleteFile(filepath.Join(c.remote_root, path), true); err != nil {
			return err
		}
	}

	byteCount := 0
	for _, k := range changed {
		byteCount += int(k.Size)
	}

	if byteCount == 0 {
		return nil
	}

	progress := newProgressBar(byteCount, "Upload progress:")
	for _, p := range sortedKeys(changed) {
		meta := changed[p]
		err := c.ws.WriteFile(meta,
			filepath.Join(c.root, p),
			path.Join(c.remote_root, p),
			progress)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) Pull() error {
	changed, deleted := files.FilterFiles(c.remote_index, c.index)
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))
	if (len(changed) + len(deleted)) == 0 {
		return nil
	}

	for _, p := range sortedKeys(deleted) {
		if err := c.ws.DeleteFile(filepath.Join(c.root, p), false); err != nil {
			return err
		}
	}

	byteCount := 0
	for _, k := range changed {
		byteCount += int(k.Size)
	}
	if byteCount == 0 {
		return nil
	}

	progress := newProgressBar(byteCount, "Download progress:")
	for _, p := range sortedKeys(changed) {
		meta := changed[p]
		err := c.ws.ReadFile(meta,
			filepath.Join(c.root, p),
			filepath.Join(c.remote_root, p),
			progress)
		if err != nil {
			return err
		}
	}

	return nil
}
