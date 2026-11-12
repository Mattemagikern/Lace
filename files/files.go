package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const copyBufSize = 1 << 20

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, copyBufSize)
		return &b
	},
}

func getBuf() *[]byte  { return bufPool.Get().(*[]byte) }
func putBuf(p *[]byte) { bufPool.Put(p) }

type MkdirCache struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func (m *MkdirCache) Ensure(dir string) error {
	m.mu.Lock()
	if _, ok := m.seen[dir]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	m.mu.Lock()
	if m.seen == nil {
		m.seen = make(map[string]struct{})
	}
	m.seen[dir] = struct{}{}
	m.mu.Unlock()
	return nil
}

func MakeMetaData(path string) (MetaData, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return MetaData{}, err
	}

	var link string
	if info.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(path)
		if err != nil {
			return MetaData{}, err
		}
	}

	return MetaData{
		Path:  path,
		Size:  info.Size(),
		Mode:  info.Mode(),
		IsDir: info.IsDir(),
		Time:  info.ModTime().Unix(),
		Link:  link,
	}, nil
}

func StreamExact(dst io.Writer, src io.Reader, size int64) error {
	bufp := getBuf()
	defer putBuf(bufp)
	written, err := io.CopyBuffer(dst, io.LimitReader(src, size), *bufp)
	if err != nil {
		return err
	}
	if written < size {
		return fmt.Errorf("short read: %d/%d bytes: %w", written, size, io.ErrUnexpectedEOF)
	}
	return nil
}

func WriteFile(path string, metadata *MetaData, content io.Reader, mkdir *MkdirCache, dryrun bool) error {
	if metadata.IsDir {
		if dryrun {
			return nil
		}
		mode := metadata.Mode.Perm()
		if mode == 0 {
			mode = 0755
		}
		if err := os.MkdirAll(path, mode); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	}
	if metadata.Mode&os.ModeSymlink != 0 {
		if dryrun {
			return nil
		}
		_ = os.Remove(path)
		return os.Symlink(metadata.Link, path)
	}

	body := io.LimitReader(content, metadata.Size)
	bufp := getBuf()
	defer putBuf(bufp)
	buf := *bufp

	if dryrun {
		_, err := io.CopyBuffer(io.Discard, body, buf)
		return err
	}

	dir := filepath.Dir(path)
	var setupErr error
	if mkdir != nil {
		setupErr = mkdir.Ensure(dir)
	} else {
		setupErr = os.MkdirAll(dir, 0755)
	}

	var f *os.File
	if setupErr == nil {
		var err error
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, metadata.Mode.Perm())
		if err != nil && os.IsPermission(err) {
			os.Chmod(path, 0644)
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, metadata.Mode.Perm())
		}
		if err != nil {
			setupErr = fmt.Errorf("failed to open: %w", err)
		}
	}

	if setupErr != nil {
		if _, err := io.CopyBuffer(io.Discard, body, buf); err != nil {
			return fmt.Errorf("drain after setup error %v: %w", setupErr, err)
		}
		return setupErr
	}

	if _, err := io.CopyBuffer(f, body, buf); err != nil {
		f.Close()
		return fmt.Errorf("write error: %w", err)
	}

	mtime := time.Unix(metadata.Time, 0)
	f.Close()
	os.Chtimes(path, mtime, mtime)
	os.Chmod(path, metadata.Mode)
	return nil
}
