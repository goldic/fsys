package fsys

import (
	"errors"
	"io/fs"
)

type mergedFS []fs.FS

// MergedFS searches filesystems in order and opens the first matching path.
// It falls back only on fs.ErrNotExist. Directories are not merged: opening a
// directory returns the first matching directory and its own entries.
func MergedFS(filesystems ...fs.FS) fs.FS {
	return mergedFS(append([]fs.FS(nil), filesystems...))
}

func (filesystems mergedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	for _, filesystem := range filesystems {
		if filesystem == nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
		}
		file, err := filesystem.Open(name)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return file, err
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// Merge registers a new volume that searches the named volumes in order.
// Unknown names return an error; the returned path identifies the new volume.
func Merge(names ...string) (string, error) {
	filesystems := make([]fs.FS, 0, len(names))
	for _, name := range names {
		volume, err := VolumeByName(name)
		if err != nil {
			return "", err
		}
		filesystems = append(filesystems, volume)
	}
	return AddVolume(MergedFS(filesystems...)), nil
}
