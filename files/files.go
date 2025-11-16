package files
import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func WriteFile(path string, metadata *MetaData, content []byte) error {
	if metadata.IsDir {
		return os.MkdirAll(path, metadata.Mode)
	}

	if _, err := os.Lstat(filepath.Dir(path)); err != nil {
		os.MkdirAll(filepath.Dir(path), 0755);
	}

	os.Remove(path)
	if metadata.Mode & os.ModeSymlink != 0 {
		return os.Symlink(string(content), path)
	}

	//normal file
	if err := os.WriteFile(path, content, metadata.Mode); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	time := time.Unix(metadata.Time, 0)
	return os.Chtimes(path, time, time)
}

func ReadFile(path string) (m MetaData, content []byte, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return m, nil, fmt.Errorf("lstat file err:%v", err)
	}
	if (info.Mode() & os.ModeSymlink) != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return m, nil, fmt.Errorf("readlink file err:%v", err)
		}
		content = ([]byte)(target)
	} else if info.IsDir() {
		content = make([]byte, 0)
	} else {
		content, err = os.ReadFile(path)
		if err != nil {
			return m, nil, fmt.Errorf("read file err:%v", err)
		}
	}
	m = MetaData {
		Path: path,
		Size: info.Size(),
		Mode: info.Mode(),
		IsDir: info.IsDir(),
		Time: info.ModTime().Unix(),
	}

	return m, content, nil
}
