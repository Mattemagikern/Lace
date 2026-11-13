package files

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/vmihailenco/msgpack/v5"
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

func (idx FileIndex) EncodeMsgpack(enc *msgpack.Encoder) error {
	if err := enc.EncodeMapLen(len(idx)); err != nil {
		return err
	}
	for path, m := range idx {
		if err := enc.EncodeString(path); err != nil {
			return err
		}
		if err := enc.EncodeArrayLen(4); err != nil {
			return err
		}
		if err := enc.EncodeInt(m.Size); err != nil {
			return err
		}
		if err := enc.EncodeUint(uint64(m.Mode)); err != nil {
			return err
		}
		if err := enc.EncodeInt(m.Time); err != nil {
			return err
		}
		if err := enc.EncodeString(m.Link); err != nil {
			return err
		}
	}
	return nil
}

func (idx *FileIndex) DecodeMsgpack(dec *msgpack.Decoder) error {
	n, err := dec.DecodeMapLen()
	if err != nil {
		return err
	}
	out := make(FileIndex, n)
	for range n {
		path, err := dec.DecodeString()
		if err != nil {
			return err
		}
		if _, err := dec.DecodeArrayLen(); err != nil {
			return err
		}
		size, err := dec.DecodeInt64()
		if err != nil {
			return err
		}
		mode, err := dec.DecodeUint32()
		if err != nil {
			return err
		}
		t, err := dec.DecodeInt64()
		if err != nil {
			return err
		}
		link, err := dec.DecodeString()
		if err != nil {
			return err
		}
		fm := os.FileMode(mode)
		out[path] = MetaData{
			Path:  path,
			Size:  size,
			Mode:  fm,
			IsDir: fm.IsDir(),
			Time:  t,
			Link:  link,
		}
	}
	*idx = out
	return nil
}

func emitEntry(rel, path string, d fs.DirEntry, out chan<- MetaData) error {
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
}

type walkState struct {
	root     string
	ig       *Matcher
	out      chan<- MetaData
	sem      chan struct{}
	wg       sync.WaitGroup
	errMu    sync.Mutex
	firstErr error
}

func (s *walkState) rel(path string) string {
	n := len(s.root)
	if len(path) > n+1 && path[:n] == s.root && os.IsPathSeparator(path[n]) {
		return path[n+1:]
	}
	rel, _ := filepath.Rel(s.root, path)
	return rel
}

func (s *walkState) fail(err error) {
	s.errMu.Lock()
	if s.firstErr == nil {
		s.firstErr = err
	}
	s.errMu.Unlock()
}

func (s *walkState) walk(dir string) {
	defer s.wg.Done()

	entries, err := os.ReadDir(dir)
	if err != nil {
		s.fail(err)
		return
	}

	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		rel := s.rel(path)
		if s.ig.Match(filepath.ToSlash(rel), e.IsDir()) {
			continue
		}
		if err := emitEntry(rel, path, e, s.out); err != nil {
			s.fail(err)
			continue
		}
		if e.IsDir() {
			s.wg.Add(1)
			select {
			case s.sem <- struct{}{}:
				go func(sub string) {
					defer func() { <-s.sem }()
					s.walk(sub)
				}(path)
			default:
				s.walk(path)
			}
		}
	}
}

func MakeIndex(root string, ig *Matcher) (FileIndex, error) {
	root = filepath.Clean(ExpandPath(root))
	idx := make(FileIndex)

	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !ig.Match(".", false) {
			out := make(chan MetaData, 1)
			go func() {
				defer close(out)
				emitEntry(".", root, fs.FileInfoToDirEntry(info), out)
			}()
			for m := range out {
				idx[m.Path] = m
			}
		}
		return idx, nil
	}

	idx["."] = MetaData{Path: ".", IsDir: true, Mode: os.ModeDir}

	out := make(chan MetaData, 1024)
	s := &walkState{
		root: root,
		ig:   ig,
		out:  out,
		sem:  make(chan struct{}, runtime.NumCPU()),
	}

	s.wg.Add(1)
	go s.walk(root)

	go func() {
		s.wg.Wait()
		close(out)
	}()

	for m := range out {
		idx[m.Path] = m
	}

	return idx, s.firstErr
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
