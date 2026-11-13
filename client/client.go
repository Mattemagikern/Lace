package client

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/mattemagikern/lace/files"
)

type Client struct {
	root        string
	remote_root string

	ws           *WorkSet
	st           *Status
	index        files.FileIndex
	remote_index files.FileIndex
}

func NewClient(root, remote_root string, ws *WorkSet, ignore []string, st *Status) (*Client, error) {
	logger := slog.Default()
	ig, err := files.NewMatcher(ignore)
	if err != nil {
		return nil, err
	}
	c := &Client{
		root:        root,
		remote_root: remote_root,
		ws:          ws,
		st:          st,
	}

	st.Set("Connecting...")
	stop, watch := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watch)
		select {
		case <-ws.Connected():
			if ws.ConnError() != nil {
				st.Set("Connection failed")
			} else {
				st.Set("Indexing...")
			}
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-watch
	}()

	res := make(chan *files.FileIndex, 1)
	go func() {
		if err := ws.RequestIndex(c.remote_root, ignore, res); err != nil {
			res <- nil
		}
	}()

	logger.Info(fmt.Sprintf("local index start:%v\n", time.Now()))
	c.index, err = files.MakeIndex(c.root, ig)
	if err != nil {
		c.index = make(files.FileIndex)
	}
	logger.Info(fmt.Sprintf("local index end:%v\n", time.Now()))

	var remote_index *files.FileIndex
	select {
	case remote_index = <-res:
	case <-ws.Done():
		select {
		case remote_index = <-res:
		default:
		}
	}
	if remote_index == nil {
		if cerr := ws.ConnError(); cerr != nil {
			return nil, fmt.Errorf("failed to connect: %w", cerr)
		}
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

func sortedKeysSymlinksLast(idx files.FileIndex) []string {
	regular := make([]string, 0, len(idx))
	links := make([]string, 0)
	for k, m := range idx {
		if m.Mode&os.ModeSymlink != 0 {
			links = append(links, k)
			continue
		}
		regular = append(regular, k)
	}
	sort.Strings(regular)
	sort.Strings(links)
	return append(regular, links...)
}

func (c *Client) Push() error {
	c.st.Set("Comparing...")
	changed, deleted := files.FilterFiles(c.index, c.remote_index)
	c.st.Clear()
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))
	if (len(changed) + len(deleted)) == 0 {
		return nil
	}

	c.st.Set("Deleting...")
	for _, p := range sortedKeys(deleted) {
		if err := c.ws.DeleteFile(path.Join(c.remote_root, p), true); err != nil {
			return err
		}
	}
	c.st.Clear()

	byteCount := 0
	for _, k := range changed {
		byteCount += int(k.Size)
	}

	if len(changed) == 0 {
		return nil
	}

	var progress *progressbar.ProgressBar
	if byteCount > 0 {
		progress = newProgressBar(byteCount, "Upload progress:")
	}
	for _, p := range sortedKeysSymlinksLast(changed) {
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
	c.st.Set("Comparing...")
	changed, deleted := files.FilterFiles(c.remote_index, c.index)
	c.st.Clear()
	fmt.Printf("Changed files: %d, Deleted files: %d \n", len(changed), len(deleted))
	if (len(changed) + len(deleted)) == 0 {
		return nil
	}

	c.st.Set("Deleting...")
	for _, p := range sortedKeys(deleted) {
		if err := c.ws.DeleteFile(filepath.Join(c.root, p), false); err != nil {
			return err
		}
	}
	c.st.Clear()

	byteCount := 0
	for _, k := range changed {
		byteCount += int(k.Size)
	}
	if len(changed) == 0 {
		return nil
	}

	var progress *progressbar.ProgressBar
	if byteCount > 0 {
		progress = newProgressBar(byteCount, "Download progress:")
	}
	for _, p := range sortedKeysSymlinksLast(changed) {
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
