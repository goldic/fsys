package fsys_test

import (
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/goldic/fsys"
)

type failingFS struct{ err error }

func (f failingFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: f.err}
}

func TestMergedFS(t *testing.T) {
	first := fstest.MapFS{
		"shared.txt": &fstest.MapFile{Data: []byte("first")},
		"dir/a.txt":  &fstest.MapFile{Data: []byte("a")},
	}
	second := fstest.MapFS{
		"shared.txt": &fstest.MapFile{Data: []byte("second")},
		"dir/b.txt":  &fstest.MapFile{Data: []byte("b")},
	}
	merged := fsys.MergedFS(first, second)
	for _, tc := range []struct{ name, want string }{
		{"shared.txt", "first"}, {"dir/a.txt", "a"}, {"dir/b.txt", "b"},
	} {
		data, err := fs.ReadFile(merged, tc.name)
		if err != nil || string(data) != tc.want {
			t.Errorf("ReadFile(%q) = %q, %v", tc.name, data, err)
		}
	}
	entries, err := fs.ReadDir(merged, "dir")
	if err != nil || len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("ReadDir = %v, %v", entries, err)
	}
	matches, err := fs.Glob(merged, "dir/*.txt")
	if err != nil || !reflect.DeepEqual(matches, []string{"dir/a.txt"}) {
		t.Errorf("Glob = %v, %v", matches, err)
	}
	if _, err := merged.Open("absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing error = %v", err)
	}
	if _, err := merged.Open("../invalid"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("invalid path error = %v", err)
	}
	if _, err := fsys.MergedFS().Open("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty merge error = %v", err)
	}
	if _, err := fsys.MergedFS(nil).Open("file"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("nil filesystem error = %v", err)
	}
	if _, err := fsys.MergedFS(failingFS{fs.ErrPermission}, second).Open("shared.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("permission error = %v", err)
	}
	data, err := fs.ReadFile(fsys.MergedFS(failingFS{fs.ErrNotExist}, second), "shared.txt")
	if err != nil || string(data) != "second" {
		t.Errorf("wrapped not-exist fallback = %q, %v", data, err)
	}
	if err := fstest.TestFS(fsys.MergedFS(first), "shared.txt", "dir/a.txt"); err != nil {
		t.Errorf("filesystem contract: %v", err)
	}
}

func TestMergeRegisteredVolumes(t *testing.T) {
	firstName, _ := registerVolume(t, fstest.MapFS{"one": &fstest.MapFile{Data: []byte("one")}})
	secondName, _ := registerVolume(t, fstest.MapFS{"two": &fstest.MapFile{Data: []byte("two")}})
	root, err := fsys.Merge(firstName, secondName)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		data, err := fsys.ReadFile(root + "/" + name)
		if err != nil || string(data) != name {
			t.Errorf("ReadFile(%q) = %q, %v", name, data, err)
		}
	}
	if _, err := fsys.Merge(firstName, uniqueName()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unknown volume error = %v", err)
	}
}
