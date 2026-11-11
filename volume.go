package fsys

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"
)

// Volume is a read-only filesystem, optionally registered under a name.
// Its zero value is not usable.
type Volume struct {
	fs.FS
	name string
}

var registry = struct {
	sync.RWMutex
	volumes map[string]fs.FS
	next    uint64
}{volumes: make(map[string]fs.FS)}

func init() {
	// These names are fixed, valid and unique at initialization.
	registry.volumes["OS"] = os.DirFS(string(os.PathSeparator))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		registry.volumes["HOME"] = os.DirFS(home)
	}
}

// AddVolume registers filesystem under the given names and returns the first
// name as an fs:// path. With no names, it generates a unique name. Names must
// contain only ASCII letters, digits, underscores or hyphens. It panics if a
// name is invalid or already in use, or if filesystem is nil; no names are
// registered in that case. The filesystem is otherwise not checked during
// registration and should support concurrent reads if used concurrently.
func AddVolume(filesystem fs.FS, names ...string) string {
	if filesystem == nil {
		panic(fmt.Errorf("fsys: nil filesystem: %w", fs.ErrInvalid))
	}
	v, err := register(filesystem, names...)
	if err != nil {
		panic(err)
	}
	return v.Path()
}

func register(filesystem fs.FS, names ...string) (Volume, error) {
	registry.Lock()
	defer registry.Unlock()
	if len(names) == 0 {
		for {
			registry.next++
			name := fmt.Sprintf("VOL%d", registry.next)
			if _, exists := registry.volumes[name]; !exists {
				names = []string{name}
				break
			}
		}
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !validVolumeName(name) {
			return Volume{}, fmt.Errorf("fsys: invalid volume name %q: %w", name, fs.ErrInvalid)
		}
		if _, exists := registry.volumes[name]; exists || seen[name] {
			return Volume{}, fmt.Errorf("fsys: volume %q: %w", name, fs.ErrExist)
		}
		seen[name] = true
	}
	for _, name := range names {
		registry.volumes[name] = filesystem
	}
	return Volume{FS: filesystem, name: names[0]}, nil
}

func validVolumeName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// VolumeByName looks up a registered volume by name, without the fs:// prefix.
// It returns an error wrapping fs.ErrNotExist if the name is not registered.
func VolumeByName(name string) (Volume, error) {
	registry.RLock()
	f, ok := registry.volumes[name]
	registry.RUnlock()
	if !ok {
		return Volume{}, fmt.Errorf("fsys: volume %q: %w", name, fs.ErrNotExist)
	}
	return Volume{FS: f, name: name}, nil
}

// Name returns the registered name, or an empty string for an unnamed volume.
func (v Volume) Name() string { return v.name }

// Path returns the fs:// path of a registered volume, or an empty string if unnamed.
func (v Volume) Path() string {
	if v.name == "" {
		return ""
	}
	return prefix + v.name
}

// String returns the registered volume path, or an empty string if unnamed.
func (v Volume) String() string { return v.Path() }

// GoString returns a representation of the volume for Go syntax formatting.
func (v Volume) GoString() string { return fmt.Sprintf("fsys.Volume(%q)", v.Path()) }

// SubVolume returns a filesystem rooted at name. If volumeNames are provided,
// it also registers the result under those names. Without names the result is
// not registered. As with fs.Sub, the directory may not be checked immediately.
func (v Volume) SubVolume(name string, volumeNames ...string) (Volume, error) {
	sub, err := v.Sub(name)
	if err != nil {
		return Volume{}, err
	}
	if len(volumeNames) == 0 {
		return Volume{FS: sub}, nil
	}
	return register(sub, volumeNames...)
}

// Open opens name in the volume. The caller must close the returned file.
// Names must follow the io/fs path rules.
func (v Volume) Open(name string) (fs.File, error) { return v.FS.Open(name) }

// Sub returns a filesystem rooted at name.
func (v Volume) Sub(name string) (fs.FS, error) { return fs.Sub(v.FS, name) }

// HTTPHandler serves the volume with net/http's standard file server.
func (v Volume) HTTPHandler() http.Handler { return http.FileServer(http.FS(v)) }

// Exists reports whether name exists. Errors other than not-exist are returned.
func (v Volume) Exists(name string) (bool, error) {
	_, err := v.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// ReadDir returns directory entries sorted by filename.
func (v Volume) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(v.FS, name) }

// Stat returns information about name.
func (v Volume) Stat(name string) (fs.FileInfo, error) { return fs.Stat(v.FS, name) }

// ReadFile reads a file's complete contents.
func (v Volume) ReadFile(name string) ([]byte, error) { return fs.ReadFile(v.FS, name) }

// Glob returns names matching pattern relative to the volume root.
func (v Volume) Glob(pattern string) ([]string, error) { return fs.Glob(v.FS, pattern) }

// ContentHash64 returns the first 64 bits of a SHA-256 hash of file paths and
// contents. Empty directories and metadata are not included. It is intended for
// change detection, not cryptographic verification or persistent identifiers.
func (v Volume) ContentHash64() (uint64, error) {
	hash := sha256.New()
	err := fs.WalkDir(v.FS, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		file, err := v.FS.Open(name)
		if err != nil {
			return err
		}
		fileHash := sha256.New()
		_, copyErr := io.Copy(fileHash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(name)))
		_, _ = hash.Write(size[:])
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write(fileHash.Sum(nil))
		return nil
	})
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(hash.Sum(nil)), nil
}

// OnUpdate starts an endless polling goroutine that calls fn once immediately
// and again when file paths or contents change. The first successful scan
// establishes the initial state;
// changes before that scan completes may not be detected. It scans the whole
// volume every 100 ms. Scan errors are passed to onError, or ignored when it is
// nil, and polling continues. It panics if fn is nil. Callbacks should return
// promptly; their panics are not recovered. The caller must ensure the filesystem
// supports concurrent reads.
func (v Volume) OnUpdate(fn func(), onError func(error)) {
	if fn == nil {
		panic("fsys: nil update callback")
	}
	go func() {
		for prev := uint64(0); ; time.Sleep(100 * time.Millisecond) {
			h64, err := v.ContentHash64()
			if err != nil {
				if onError != nil {
					onError(err)
				}
				continue
			}
			if h64 != prev {
				prev = h64
				fn()
			}
		}
	}()
}
