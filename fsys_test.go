package fsys

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

//go:embed fsys_test.go
var embeddedFiles embed.FS

var nextName atomic.Uint64

func uniqueName() string {
	return fmt.Sprintf("TEST_%d", nextName.Add(1))
}

func registerVolume(t *testing.T, filesystem fs.FS) (string, string) {
	t.Helper()
	name := uniqueName()
	root := AddVolume(filesystem, name)
	return name, root
}

func sampleFS() fstest.MapFS {
	return fstest.MapFS{
		"dir/a1.txt":  &fstest.MapFile{Data: []byte("one")},
		"dir/a2.txt":  &fstest.MapFile{Data: []byte("two")},
		"dir/b.txt":   &fstest.MapFile{Data: []byte("three")},
		"special?#%":  &fstest.MapFile{Data: []byte("special")},
		"dir/sub/x.c": &fstest.MapFile{Data: []byte("nested")},
	}
}

func TestParsePathEmbedded(t *testing.T) {
	_, root := registerVolume(t, embeddedFiles)
	filesystem, name, err := ParsePath(root + "/fsys_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if name != "fsys_test.go" {
		t.Fatalf("name = %q", name)
	}
	data, err := fs.ReadFile(filesystem, name)
	if err != nil || !strings.Contains(string(data), "func TestParsePathEmbedded") {
		t.Fatalf("embedded file: %v", err)
	}
	for _, path := range []string{root, root + "/"} {
		_, name, err := ParsePath(path)
		if err != nil || name != "." {
			t.Errorf("ParsePath(%q) = %q, %v", path, name, err)
		}
	}
}

func TestLocalPaths(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "local.txt")
	if err := os.WriteFile(filename, []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filename}
	if relative, err := filepath.Rel(cwd, filename); err == nil {
		paths = append(paths, relative)
	}
	for _, name := range paths {
		t.Run(name, func(t *testing.T) {
			filesystem, rel, err := ParsePath(name)
			if err != nil {
				t.Fatal(err)
			}
			if !fs.ValidPath(rel) {
				t.Fatalf("invalid fs path %q", rel)
			}
			data, err := fs.ReadFile(filesystem, rel)
			if err != nil || string(data) != "local" {
				t.Fatalf("ReadFile = %q, %v", data, err)
			}
			data, err = ReadFile(name)
			if err != nil || string(data) != "local" {
				t.Fatalf("ReadFile = %q, %v", data, err)
			}
		})
	}
	data, err := ReadFile("fsys_test.go")
	if err != nil || !strings.Contains(string(data), "func TestLocalPaths") {
		t.Fatalf("relative path read: %v", err)
	}
	matches, err := Glob(filepath.Join(dir, "loc?l.*"))
	if err != nil || !reflect.DeepEqual(matches, []string{filename}) {
		t.Fatalf("local Glob = %v, %v", matches, err)
	}
}

func TestVolumePathsAndOperations(t *testing.T) {
	_, root := registerVolume(t, sampleFS())
	data, err := ReadFile(root + "/special?#%")
	if err != nil || string(data) != "special" {
		t.Fatalf("special filename = %q, %v", data, err)
	}
	file, err := Open(root + "/dir/a1.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil || string(data) != "one" {
		t.Fatalf("Open = %q, %v, close: %v", data, err, closeErr)
	}
	entries, err := ReadDir(root + "/dir")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"a1.txt", "a2.txt", "b.txt", "sub"}) {
		t.Fatalf("ReadDir = %v", names)
	}
	matches, err := Glob(root + "/dir/a?.txt")
	if err != nil || !reflect.DeepEqual(matches, []string{"dir/a1.txt", "dir/a2.txt"}) {
		t.Fatalf("Glob = %v, %v", matches, err)
	}
	if _, err := Glob(root + "/["); !errors.Is(err, path.ErrBadPattern) {
		t.Errorf("invalid Glob error = %v", err)
	}
	dirFS, err := Dir(root + "/dir")
	if err != nil {
		t.Fatal(err)
	}
	data, err = fs.ReadFile(dirFS, "sub/x.c")
	if err != nil || string(data) != "nested" {
		t.Fatalf("Dir = %q, %v", data, err)
	}
	info, err := Stat(root + "/dir/a1.txt")
	if err != nil || info.Name() != "a1.txt" {
		t.Fatalf("Stat = %v, %v", info, err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{{"dir", true}, {"dir/a1.txt", false}} {
		got, err := IsDir(root + "/" + tc.path)
		if err != nil || got != tc.want {
			t.Errorf("IsDir(%s) = %v, %v", tc.path, got, err)
		}
	}
	if _, err := IsDir(root + "/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("IsDir missing error = %v", err)
	}
}

func TestPathErrors(t *testing.T) {
	_, root := registerVolume(t, sampleFS())
	for _, name := range []string{"", "https://example.com/file", "fs://UNKNOWN_VOLUME/file"} {
		if _, _, err := ParsePath(name); err == nil {
			t.Errorf("ParsePath(%q) succeeded", name)
		}
	}
	for _, name := range []string{"../secret", "/absolute", "dir/../secret"} {
		if _, err := ReadFile(root + "/" + name); err == nil {
			t.Errorf("ReadFile(%q) succeeded", name)
		}
	}
	for _, path := range []string{root + "/dir/a1.txt", root + "/missing"} {
		got, err := FileExists(path)
		if err != nil || got != strings.HasSuffix(path, "a1.txt") {
			t.Errorf("FileExists(%q) = %v, %v", path, got, err)
		}
	}
	if _, err := FileExists("fs://UNKNOWN_VOLUME/file"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing volume error = %v", err)
	}
}

func TestPredefinedHomeVolume(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory is unavailable")
	}
	local, err := os.Stat(home)
	if err != nil {
		t.Skipf("cannot stat home: %v", err)
	}
	for _, path := range []string{"fs://HOME", "fs://HOME/"} {
		info, err := Stat(path)
		if err != nil || !os.SameFile(local, info) {
			t.Errorf("Stat(%q) = %v, %v", path, info, err)
		}
	}
}
