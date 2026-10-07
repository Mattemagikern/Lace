package files

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type MetaData struct {
	Path  string
	Size  int64
	Mode  os.FileMode
	IsDir bool
	Time  int64
	Link  string
}

type FileIndex map[string]MetaData

func walkSubtree(root, sub string, ig *Matcher, out chan<- MetaData) error {
	return filepath.WalkDir(sub, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if ig.Match(filepath.ToSlash(rel), d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(path)
		}
		out <- MetaData{
			Path:  rel,
			Size:  info.Size(),
			Mode:  info.Mode(),
			IsDir: d.IsDir(),
			Time:  info.ModTime().Unix(),
			Link:  link,
		}
		return nil
	})
}

func MakeIndex(root string, ig *Matcher) (FileIndex, error) {
	root = ExpandPath(root)
	idx := make(FileIndex)

	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		out := make(chan MetaData, 1)
		go func() {
			defer close(out)
			walkSubtree(root, root, ig, out)
		}()
		for m := range out {
			idx[m.Path] = m
		}
		return idx, nil
	}

	idx["."] = MetaData{Path: ".", IsDir: true, Mode: os.ModeDir}

	entries, err := os.ReadDir(root)
	if err != nil {
		return idx, err
	}

	out := make(chan MetaData, 1024)
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for _, e := range entries {
		if ig.Match(e.Name(), e.IsDir()) {
			continue
		}
		sub := filepath.Join(root, e.Name())
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := walkSubtree(root, sub, ig, out); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	for m := range out {
		idx[m.Path] = m
	}

	return idx, firstErr
}

func FilterFiles(src, dst FileIndex) (changed, deleted FileIndex) {
	changed = make(FileIndex)
	deleted = make(FileIndex)

	for path, srcInfo := range src {
		dstInfo, exists := dst[path]
		switch {
		case !exists:
			changed[path] = srcInfo
		case srcInfo.Mode&os.ModeSymlink != 0 && srcInfo.Link == dstInfo.Link:
		case srcInfo.IsDir != dstInfo.IsDir:
			changed[path] = srcInfo
		case srcInfo.IsDir && srcInfo.Mode != dstInfo.Mode:
			changed[path] = srcInfo
		case !srcInfo.IsDir && (srcInfo.Size != dstInfo.Size || srcInfo.Time != dstInfo.Time):
			changed[path] = srcInfo
		}
	}
	for path, dstInfo := range dst {
		if _, exists := src[path]; !exists {
			deleted[path] = dstInfo
		}
	}
	return changed, deleted
}
