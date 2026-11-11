# fsys

`fsys` is a small, read-only Go package for accessing local files and named
[`io/fs`](https://pkg.go.dev/io/fs) filesystems through a common path format.
It can also search multiple filesystems in order. It has no external dependencies
and requires Go 1.20 or later.

```sh
go get github.com/goldic/fsys
```

## Paths

- A path without a scheme is a local operating-system path. Relative paths are
  resolved against the current working directory; absolute paths stay absolute.
- `fs://NAME` identifies the root of a registered volume, and
  `fs://NAME/path/to/file` identifies a path inside it. Volume names are
  case-sensitive and may contain ASCII letters, digits, `_` and `-`.
- Volume paths use `/` separators and `io/fs` path rules: use `.` for the
  root, and do not use `.` or `..` path components. The `fs://NAME` and
  `fs://NAME/` forms both identify the root.
- Volume paths are **not URLs**: `%`, `?`, and `#` are literal characters in
  filenames. For `Glob`, `?` and other glob characters have their usual pattern
  meaning. No percent decoding is performed.
- Schemes other than `fs://` are rejected. Native Windows volume paths, such
  as `C:\data\file.txt`, are local paths. A local Unix filename beginning
  with a valid scheme followed by `:` must be prefixed with `./` or expressed
  as an absolute path.

The predefined `OS` volume is rooted at the OS path separator (on Windows, the
current drive's root). `HOME` is registered only when `os.UserHomeDir` succeeds
and returns a non-empty path. Its location is determined when the package is
initialized. Neither `os.DirFS` nor a subdirectory of it is a security boundary
against symbolic links that lead outside the root.

## Register and read

```go
package main

import (
    "embed"
    "fmt"
    "log"

    "github.com/goldic/fsys"
)

//go:embed assets
var assets embed.FS

func main() {
    root := fsys.AddVolume(assets, "ASSETS")
    data, err := fsys.ReadFile(root + "/assets/config.json")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(string(data))
}
```

`AddVolume` also accepts multiple names for a filesystem, or generates a unique
name if none are supplied. A nil filesystem or duplicate or invalid names cause
a panic; no names are registered in that case. Registration does not otherwise
check the filesystem: errors are reported when it is accessed. The registry is
global and safe for concurrent access. Registered filesystems must themselves support
concurrent reads when used concurrently. There is no unregister operation.

Use `VolumeByName` to obtain a `Volume`, which implements `fs.FS` and has
`Open`, `ReadFile`, `ReadDir`, `Stat`, `Glob`, and `Sub` methods. Names passed to
these methods are relative to the volume root and use `io/fs` path rules.
`SubVolume` returns an unnamed volume without registering it, unless names are
explicitly provided. `Dir` and `SubVolume` use `fs.Sub`, which may defer checking
whether a directory exists until it is accessed.

`fsys.Glob("fs://ASSETS/assets/file?.txt")` preserves `?` as a wildcard and
returns paths relative to the volume root, for example `assets/file1.txt`.
Local glob patterns and returned matches follow `filepath.Glob` conventions.

## Search multiple filesystems

```go
// local and embedded are fs.FS values, such as os.DirFS("./assets")
// and an embed.FS. Files in local have priority over files in embedded.
combined := fsys.MergedFS(local, embedded)
root := fsys.AddVolume(combined, "APP")
data, err := fsys.ReadFile(root + "/config.json")
```

`MergedFS` searches in argument order, falling back only when a filesystem
returns an error matching `fs.ErrNotExist`. Other errors are returned immediately.
It performs **file lookup**, not a union of directory entries: opening a
directory exposes only the first matching directory's entries. Consequently,
`ReadDir`, `Glob`, and tree walks may not discover files present only in later
filesystems even though opening those files directly succeeds. `Merge` offers
the same behavior using registered volume names and registers the result under an
automatically generated name.

## Changes and debugging helpers

`Volume.ContentHash64` calculates a 64-bit change-detection hash from file paths
and contents; empty directories and metadata are not included. It is not a
cryptographic verifier or a stable persistent identifier. `Volume.OnUpdate`
starts a polling goroutine that rescans the entire volume every 100 ms and runs
indefinitely. It calls the update callback once when the goroutine starts, even
if the initial scan fails. The first successful scan establishes the initial
state; changes before that scan completes may not be detected. All scan errors are passed to
the error callback, or ignored if it is `nil`, and polling continues. A nil
update callback causes a panic. Scanning can be expensive for large filesystems;
callbacks must return promptly, and their panics are not recovered.

`RuntimeDir`, `RuntimeDirFS`, `GoSourceMainFile`, and `GoSourceMainDir` are
convenience helpers for debugging. They inspect source paths in the runtime
stack; these paths may be trimmed at build time, may not exist on the machine
running the binary, and do not identify the executable directory. The main-file
heuristic is cached on first use and can produce a different result if called
from a goroutine. Do not depend on these helpers to locate production resources.

## License

MIT. See [LICENSE](LICENSE).
