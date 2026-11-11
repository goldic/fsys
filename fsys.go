// Package fsys provides read-only access to local files and named io/fs file
// systems through a common path format. Named paths have the form fs://NAME/path.
package fsys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "fs://"

// ParsePath resolves a local path or an fs:// volume path into a filesystem and
// a slash-separated name suitable for io/fs. Relative local paths are resolved
// against the current working directory. Named paths are not URL-decoded.
func ParsePath(name string) (fs.FS, string, error) {
	if strings.HasPrefix(name, prefix) {
		rest := strings.TrimPrefix(name, prefix)
		volumeName, path, _ := strings.Cut(rest, "/")
		v, err := VolumeByName(volumeName)
		if err != nil {
			return nil, "", err
		}
		if path == "" {
			path = "."
		}
		return v, path, nil
	}
	if hasScheme(name) {
		return nil, "", fmt.Errorf("fsys: unsupported path scheme in %q", name)
	}
	if name == "" {
		return nil, "", &fs.PathError{Op: "resolve", Path: name, Err: fs.ErrInvalid}
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, "", err
	}
	volume := filepath.VolumeName(abs)
	root := volume + string(filepath.Separator)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, "", err
	}
	return os.DirFS(root), filepath.ToSlash(rel), nil
}

// hasScheme recognizes URI schemes while preserving native Windows volume paths.
func hasScheme(name string) bool {
	if filepath.VolumeName(name) != "" {
		return false
	}
	i := strings.IndexByte(name, ':')
	if i <= 0 {
		return false
	}
	for j, c := range name[:i] {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (j > 0 && ((c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'))) {
			return false
		}
	}
	return true
}

// Dir returns a filesystem rooted at path. Depending on the underlying filesystem,
// the directory may not be checked until it is accessed.
func Dir(path string) (fs.FS, error) {
	vol, name, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	return fs.Sub(vol, name)
}

// Open opens a file. The caller must close the returned file.
func Open(path string) (fs.File, error) {
	vol, name, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	return vol.Open(name)
}

// ReadFile reads a file's complete contents.
func ReadFile(path string) ([]byte, error) {
	vol, name, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(vol, name)
}

// ReadDir reads the entries in a directory, sorted by filename.
func ReadDir(path string) ([]fs.DirEntry, error) {
	vol, name, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(vol, name)
}

// Glob returns matches for pattern. Named-volume matches are relative to the
// volume root; local matches use the same format as filepath.Glob.
func Glob(pattern string) ([]string, error) {
	if !strings.HasPrefix(pattern, prefix) && !hasScheme(pattern) {
		return filepath.Glob(pattern)
	}
	vol, name, err := ParsePath(pattern)
	if err != nil {
		return nil, err
	}
	return fs.Glob(vol, name)
}

// Stat returns information about a file or directory.
func Stat(path string) (fs.FileInfo, error) {
	vol, name, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	return fs.Stat(vol, name)
}

// FileExists reports whether a file or directory exists. Other errors are returned.
func FileExists(path string) (bool, error) {
	vol, name, err := ParsePath(path)
	if err != nil {
		return false, err
	}
	_, err = fs.Stat(vol, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// IsDir reports whether path is a directory. It returns an error if Stat fails.
func IsDir(path string) (bool, error) {
	info, err := Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
