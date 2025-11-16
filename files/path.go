package files

import (
	"os"
	"path/filepath"
	"strings"

)

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return os.ExpandEnv(path)
}

