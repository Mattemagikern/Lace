package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

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

func WriteFile(path string, metadata *MetaData, content io.Reader) error {
	if metadata.IsDir {
		return os.MkdirAll(path, metadata.Mode.Perm())
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdirall err: %w", err)
	}

	if metadata.Mode&os.ModeSymlink != 0 {
		_ = os.Remove(path)
		return os.Symlink(metadata.Link, path)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, metadata.Mode.Perm())
	if err != nil {
		if os.IsPermission(err) {
			os.Chmod(path, 0644)
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, metadata.Mode.Perm())
		}
		if err != nil {
			return fmt.Errorf("failed to open: %w", err)
		}
	}
	defer f.Close()

	if metadata.Size > 0 {
		f.Truncate(metadata.Size)
	}

	_, err = io.CopyN(f, content, metadata.Size)
	if err != nil {
		return fmt.Errorf("write error: %w", err)
	}

	mtime := time.Unix(metadata.Time, 0)
	defer os.Chtimes(path, mtime, mtime)
	defer os.Chmod(path, metadata.Mode)

	return nil
}

func ReadFile(path string, remote io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(remote, f)
	if err != nil {
		return fmt.Errorf("failed to read file content: %w", err)
	}
	return nil
}
