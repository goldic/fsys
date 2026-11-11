package fsys

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// RuntimeDir returns the source directory of its caller. It is a debugging
// helper: source paths can be trimmed, unavailable or different on another host.
func RuntimeDir() string {
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		return ""
	}
	return filepath.Dir(file)
}

// RuntimeDirFS returns a filesystem for the caller's source directory. It is a
// debugging helper subject to the same source-path limitations as RuntimeDir.
func RuntimeDirFS() (fs.FS, error) {
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		return nil, fs.ErrNotExist
	}
	return Dir(filepath.Dir(file))
}

var mainSource struct {
	sync.Once
	file string
}

// GoSourceMainFile returns the last non-runtime source file in the first caller's
// stack, cached on first use. This debugging heuristic may identify a different
// file when first called from a goroutine; source paths may also be trimmed or
// absent on the machine running the binary. It returns an empty string when no
// frame is available.
func GoSourceMainFile() string {
	mainSource.Do(func() {
		for i := 2; ; i++ {
			_, file, _, ok := runtime.Caller(i)
			if !ok || strings.Contains(filepath.ToSlash(file), "/runtime/") {
				break
			}
			mainSource.file = file
		}
	})
	return mainSource.file
}

// GoSourceMainDir returns the directory of GoSourceMainFile, or an empty string
// if no source file was found. The same debugging limitations apply.
func GoSourceMainDir() string {
	file := GoSourceMainFile()
	if file == "" {
		return ""
	}
	return filepath.Dir(file)
}
