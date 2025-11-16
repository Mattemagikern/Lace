package files

import (
	"os"
	"path/filepath"
)

type MetaData struct {
	Path string
	Size int64
	Mode os.FileMode
	IsDir bool
	Time int64
	Link string
}

type FileIndex map[string]MetaData

func MakeIndex(root string) (FileIndex, error) {
	files := make(FileIndex)
	root = ExpandPath(root)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rellative, _ := filepath.Rel(root, path)
		link := ""
		if info.Mode() & os.ModeSymlink != 0 {
			link, _ = os.Readlink(path)
		}
		files[rellative] = MetaData{
			Path: rellative,
			Size: info.Size(),
			Mode: info.Mode(),
			IsDir: info.IsDir(),
			Time: info.ModTime().Unix(),
			Link: link,
		}
		return nil
	})
	return files, err
}

func PushFilterFiles(my, remote FileIndex) (changed, deleted FileIndex) {
	deleted = make(FileIndex)
	changed = make(FileIndex)

	for path, myInfo := range my {
		if remoteInfo, exists := remote[path]; !exists {
			// Exists locally but not remotely -> new file
			changed[path] = myInfo
		} else if myInfo.Mode &  os.ModeSymlink != 0 && myInfo.Link == remoteInfo.Link {
			// Don't change symlink if same
			continue
		} else if !myInfo.IsDir && (myInfo.Size != remoteInfo.Size || myInfo.Time != remoteInfo.Time) {
			// Exists in both but differs -> changed file
			changed[path] = myInfo
		}
	}
	// Remove directories from changed list
	traversed := make(map[string]bool)
	for path := range changed {
	    dir := filepath.Dir(path)
	    for !traversed[dir] && dir != "." {
		traversed[dir] = true
		delete(changed, dir)
		dir = filepath.Dir(dir)
	    }
	}
	// Files that exist remotely but not locally -> deleted
	for path, remoteInfo := range remote {
		if _, exists := my[path]; !exists {
			deleted[path] = remoteInfo
		}
	}

	return changed, deleted
}

func PullFilterFiles(my, remote FileIndex) (changed, deleted FileIndex) {
	deleted = make(FileIndex)
	changed = make(FileIndex)

	for path, remoteInfo := range remote {
		if myInfo, exists := my[path]; !exists {
			// Exists remotely but not locally -> new file
			changed[path] = remoteInfo
		} else if myInfo.Mode &  os.ModeSymlink != 0 && myInfo.Link == remoteInfo.Link {
			// Don't change symlink if same
			continue
		} else if !myInfo.IsDir && (myInfo.Size != remoteInfo.Size || myInfo.Time != remoteInfo.Time) {
			// Exists in both but differs -> changed file
			changed[path] = remoteInfo
		}
	}

	// Remove directories from changed list
	traversed := make(map[string]bool)
	for path := range changed {
	    dir := filepath.Dir(path)
	    for !traversed[dir] && dir != "." {
		traversed[dir] = true
		delete(changed, dir)
		dir = filepath.Dir(dir)
	    }
	}

	// Files that exist locally but not remotely -> deleted
	for path, myInfo := range my {
		if _, exists := remote[path]; !exists {
			deleted[path] = myInfo
		}
	}

	return changed, deleted
}
