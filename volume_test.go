package fsys

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func TestVolumeRegistration(t *testing.T) {
	name, root := registerVolume(t, sampleFS())
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if volume.Name() != name || volume.Path() != root || volume.String() != root {
		t.Fatalf("volume identity: %q, %q, %q", volume.Name(), volume.Path(), volume.String())
	}
	assertAddVolumePanics(t, fs.ErrExist, func() { AddVolume(sampleFS(), name) })
	for _, invalid := range []string{"", "has space", "has/slash", "ümlaut"} {
		t.Run("invalid name "+invalid, func(t *testing.T) {
			assertAddVolumePanics(t, fs.ErrInvalid, func() { AddVolume(sampleFS(), invalid) })
		})
	}
	alias1, alias2 := uniqueName(), uniqueName()
	aliasRoot := AddVolume(sampleFS(), alias1, alias2)
	if aliasRoot != "fs://"+alias1 {
		t.Fatalf("aliases = %q", aliasRoot)
	}
	if _, err := ReadFile("fs://" + alias2 + "/dir/a1.txt"); err != nil {
		t.Errorf("alias read: %v", err)
	}
	unregistered := uniqueName()
	assertAddVolumePanics(t, fs.ErrExist, func() { AddVolume(sampleFS(), unregistered, name) })
	if _, err := VolumeByName(unregistered); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("partial registration was not atomic: %v", err)
	}
	duplicate := uniqueName()
	assertAddVolumePanics(t, fs.ErrExist, func() { AddVolume(sampleFS(), duplicate, duplicate) })
}

func assertAddVolumePanics(t *testing.T, want error, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		err, ok := value.(error)
		if !ok || !errors.Is(err, want) {
			t.Errorf("panic = %v; want error matching %v", value, want)
		}
	}()
	fn()
}

func TestAddVolumeDefersFilesystemErrors(t *testing.T) {
	name := uniqueName()
	assertAddVolumePanics(t, fs.ErrInvalid, func() { AddVolume(nil, name) })
	if _, err := VolumeByName(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("nil filesystem was registered: %v", err)
	}
	root := AddVolume(failingFS{fs.ErrPermission})
	if _, err := ReadFile(root + "/file"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("opening failing filesystem: %v", err)
	}
}

func TestConcurrentRegistrationAndLookup(t *testing.T) {
	const count = 40
	roots := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			root := AddVolume(sampleFS())
			if _, err := ReadFile(root + "/dir/a1.txt"); err != nil {
				errs <- err
			}
			roots <- root
		}()
	}
	wg.Wait()
	close(errs)
	close(roots)
	for err := range errs {
		t.Error(err)
	}
	seen := make(map[string]bool)
	for root := range roots {
		if seen[root] {
			t.Errorf("duplicate generated path %q", root)
		}
		seen[root] = true
	}
	if len(seen) != count {
		t.Errorf("got %d paths, want %d", len(seen), count)
	}
}

func TestVolumeMethodsAndSubVolume(t *testing.T) {
	name, _ := registerVolume(t, sampleFS())
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(volume, "dir/a1.txt", "dir/a2.txt", "dir/b.txt", "dir/sub/x.c", "special?#%"); err != nil {
		t.Errorf("filesystem contract: %v", err)
	}
	file, err := volume.Open("dir/a1.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(data) != "one" {
		t.Errorf("Open = %q, %v, %v", data, readErr, closeErr)
	}
	if info, err := volume.Stat("dir"); err != nil || !info.IsDir() {
		t.Errorf("Stat = %v, %v", info, err)
	}
	if entries, err := volume.ReadDir("dir"); err != nil || len(entries) != 4 {
		t.Errorf("ReadDir = %v, %v", entries, err)
	}
	if matches, err := volume.Glob("dir/a?.txt"); err != nil || !reflect.DeepEqual(matches, []string{"dir/a1.txt", "dir/a2.txt"}) {
		t.Errorf("Glob = %v, %v", matches, err)
	}
	for _, tc := range []struct {
		name string
		want bool
	}{{"dir/a1.txt", true}, {"missing", false}} {
		got, err := volume.Exists(tc.name)
		if err != nil || got != tc.want {
			t.Errorf("Exists(%q) = %v, %v", tc.name, got, err)
		}
	}
	unnamed, err := volume.SubVolume("dir")
	if err != nil || unnamed.Name() != "" || unnamed.Path() != "" {
		t.Fatalf("unnamed subvolume = %v, %v", unnamed, err)
	}
	data, err = unnamed.ReadFile("a1.txt")
	if err != nil || string(data) != "one" {
		t.Errorf("unnamed subvolume read = %q, %v", data, err)
	}
	subName := uniqueName()
	named, err := volume.SubVolume("dir", subName)
	if err != nil || named.Path() != "fs://"+subName {
		t.Fatalf("named subvolume = %v, %v", named, err)
	}
	data, err = ReadFile(named.Path() + "/a2.txt")
	if err != nil || string(data) != "two" {
		t.Errorf("named subvolume read = %q, %v", data, err)
	}
	if _, err := volume.SubVolume("../invalid"); err == nil {
		t.Error("SubVolume accepted an invalid path")
	}
	if _, err := volume.SubVolume("dir", name); !errors.Is(err, fs.ErrExist) {
		t.Errorf("duplicate subvolume error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/dir/a1.txt", nil)
	response := httptest.NewRecorder()
	volume.HTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "one" {
		t.Errorf("HTTP response = %d, %q", response.Code, response.Body.String())
	}
}

func TestVolumeErrors(t *testing.T) {
	_, root := registerVolume(t, failingFS{fs.ErrPermission})
	name := strings.TrimPrefix(root, "fs://")
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := volume.Exists("file"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Exists permission error = %v", err)
	}
	if _, err := FileExists(root + "/file"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("FileExists permission error = %v", err)
	}
	if _, err := volume.ContentHash64(); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("ContentHash64 walk error = %v", err)
	}

}

func TestContentHash64(t *testing.T) {
	name, _ := registerVolume(t, sampleFS())
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	first, err := volume.ContentHash64()
	if err != nil {
		t.Fatal(err)
	}
	again, err := volume.ContentHash64()
	if err != nil || first != again {
		t.Fatalf("unchanged hash = %d, %v; want %d", again, err, first)
	}
	changed := sampleFS()
	changed["dir/a1.txt"] = &fstest.MapFile{Data: []byte("changed")}
	changedName, _ := registerVolume(t, changed)
	changedVolume, err := VolumeByName(changedName)
	if err != nil {
		t.Fatal(err)
	}
	other, err := changedVolume.ContentHash64()
	if err != nil || first == other {
		t.Errorf("changed hash = %d, %v; previous %d", other, err, first)
	}
}

// pollingFS keeps its immutable snapshots safe for the endless polling goroutine.
type pollingFS struct {
	state           atomic.Int32
	old             fstest.MapFS
	new             fstest.MapFS
	failureObserved chan struct{}
	scanObserved    chan struct{}
}

func (f *pollingFS) Open(name string) (fs.File, error) {
	switch state := f.state.Load(); state {
	case 0, 1:
		snapshot := f.old
		if state == 1 {
			snapshot = f.new
		}
		file, err := snapshot.Open(name)
		if err == nil && name == "file" && f.scanObserved != nil {
			file = scanFile{File: file, observed: f.scanObserved}
		}
		return file, err
	default:
		if f.failureObserved != nil {
			select {
			case f.failureObserved <- struct{}{}:
			default:
			}
		}
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
}

type scanFile struct {
	fs.File
	observed chan struct{}
}

func (f scanFile) Close() error {
	err := f.File.Close()
	select {
	case f.observed <- struct{}{}:
	default:
	}
	return err
}

func waitForScan(t *testing.T, filesystem *pollingFS) {
	t.Helper()
	select {
	case <-filesystem.scanObserved:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for scan")
	}
}

func waitForInitialUpdate(t *testing.T, updates <-chan struct{}) {
	t.Helper()
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial update")
	}
}

func TestOnUpdate(t *testing.T) {
	filesystem := &pollingFS{
		old:          fstest.MapFS{"file": &fstest.MapFile{Data: []byte("old")}},
		new:          fstest.MapFS{"file": &fstest.MapFile{Data: []byte("new")}},
		scanObserved: make(chan struct{}, 1),
	}
	name, _ := registerVolume(t, filesystem)
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan struct{}, 1)
	failures := make(chan error, 1)
	volume.OnUpdate(func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}, func(err error) {
		select {
		case failures <- err:
		default:
		}
	})
	waitForInitialUpdate(t, updates)
	waitForScan(t, filesystem)
	filesystem.state.Store(1)
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for update")
	}
	filesystem.state.Store(2)
	select {
	case err := <-failures:
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("polling error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for polling error")
	}
	filesystem.state.Store(0)
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for recovery update")
	}
}

func TestOnUpdateWithoutErrorCallback(t *testing.T) {
	filesystem := &pollingFS{
		old:             fstest.MapFS{"file": &fstest.MapFile{Data: []byte("old")}},
		new:             fstest.MapFS{"file": &fstest.MapFile{Data: []byte("new")}},
		failureObserved: make(chan struct{}, 1),
		scanObserved:    make(chan struct{}, 1),
	}
	name, _ := registerVolume(t, filesystem)
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan struct{}, 1)
	volume.OnUpdate(func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}, nil)
	waitForInitialUpdate(t, updates)
	waitForScan(t, filesystem)
	filesystem.state.Store(2)
	select {
	case <-filesystem.failureObserved:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for polling error")
	}
	filesystem.state.Store(1)
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for update after polling error")
	}
}

func TestOnUpdateInitialError(t *testing.T) {
	filesystem := &pollingFS{
		old:          fstest.MapFS{"file": &fstest.MapFile{Data: []byte("old")}},
		new:          fstest.MapFS{"file": &fstest.MapFile{Data: []byte("new")}},
		scanObserved: make(chan struct{}, 1),
	}
	filesystem.state.Store(2)
	name, _ := registerVolume(t, filesystem)
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan struct{}, 1)
	failures := make(chan error, 1)
	volume.OnUpdate(func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}, func(err error) {
		select {
		case failures <- err:
		default:
		}
	})
	select {
	case err := <-failures:
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("initial scan error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial scan error")
	}
	select {
	case <-updates:
		t.Fatal("update was called after a failed initial scan")
	default:
	}
	filesystem.state.Store(0)
	waitForInitialUpdate(t, updates)
	waitForScan(t, filesystem)
	filesystem.state.Store(1)
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for update after initial error")
	}
}

type blockingFS struct {
	started chan struct{}
	release chan struct{}
}

func (f blockingFS) Open(name string) (fs.File, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func TestOnUpdateInitialScanIsAsync(t *testing.T) {
	filesystem := blockingFS{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(filesystem.release)
	name, _ := registerVolume(t, filesystem)
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	returned := make(chan struct{})
	go func() {
		volume.OnUpdate(func() {}, nil)
		close(returned)
	}()
	select {
	case <-filesystem.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the initial scan")
	}
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("OnUpdate blocked on the initial scan")
	}
}

func TestOnUpdateNilCallback(t *testing.T) {
	name, _ := registerVolume(t, sampleFS())
	volume, err := VolumeByName(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("OnUpdate with nil callback did not panic")
		}
	}()
	volume.OnUpdate(nil, nil)
}

type brokenFileFS struct {
	readErr  error
	closeErr error
	closed   *atomic.Bool
}

func (f brokenFileFS) Open(name string) (fs.File, error) {
	file, err := (fstest.MapFS{"file": &fstest.MapFile{Data: []byte("content")}}).Open(name)
	if err != nil || name != "file" {
		return file, err
	}
	return brokenFile{File: file, readErr: f.readErr, closeErr: f.closeErr, closed: f.closed}, nil
}

type brokenFile struct {
	fs.File
	readErr  error
	closeErr error
	closed   *atomic.Bool
}

func (f brokenFile) Read(p []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.File.Read(p)
}

func (f brokenFile) Close() error {
	f.closed.Store(true)
	err := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}

func TestContentHash64FileErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readErr  error
		closeErr error
		want     error
	}{
		{"read", fs.ErrPermission, nil, fs.ErrPermission},
		{"close", nil, fs.ErrClosed, fs.ErrClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var closed atomic.Bool
			name, _ := registerVolume(t, brokenFileFS{readErr: tc.readErr, closeErr: tc.closeErr, closed: &closed})
			volume, err := VolumeByName(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := volume.ContentHash64(); !errors.Is(err, tc.want) {
				t.Errorf("ContentHash64 error = %v; want %v", err, tc.want)
			}
			if !closed.Load() {
				t.Error("file was not closed")
			}
		})
	}
}
