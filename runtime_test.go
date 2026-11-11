package fsys

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestRuntimeDir(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller source is unavailable")
	}
	dir := RuntimeDir()
	if dir != filepath.Dir(file) {
		t.Fatalf("RuntimeDir = %q; want %q", dir, filepath.Dir(file))
	}
	data, err := os.ReadFile(filepath.Join(dir, "runtime_test.go"))
	if err != nil {
		t.Skipf("caller source is not available on disk: %v", err)
	}
	if !strings.Contains(string(data), "func TestRuntimeDir") {
		t.Fatal("unexpected source file contents")
	}
	filesystem, err := RuntimeDirFS()
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(filesystem, "runtime_test.go")
	if err != nil || string(got) != string(data) {
		t.Errorf("RuntimeDirFS read error = %v", err)
	}
}

func TestGoSourceMainFileConcurrent(t *testing.T) {
	const count = 20
	files := make(chan string, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			files <- GoSourceMainFile()
		}()
	}
	wg.Wait()
	close(files)
	var first string
	for file := range files {
		if file == "" {
			t.Error("GoSourceMainFile returned an empty path")
		}
		if first == "" {
			first = file
		} else if file != first {
			t.Errorf("cached source file = %q; want %q", file, first)
		}
	}
	if got := GoSourceMainDir(); got != filepath.Dir(first) {
		t.Errorf("GoSourceMainDir = %q; want %q", got, filepath.Dir(first))
	}
}
