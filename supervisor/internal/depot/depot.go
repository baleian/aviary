// Package depot keeps the files that outlive a sandbox, such as the archive
// of a session's home, as objects under keys. The depots of an installation
// are processes of their own behind one address. Each serves the objects of
// a directory, and all of them serve the same one: a volume that they mount
// together.
package depot

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
)

var (
	// ErrNotFound is returned for a key that has no object.
	ErrNotFound = errors.New("no such object")
	// ErrExists is returned by Put for a key that has an object.
	ErrExists = errors.New("the key has an object")
	// ErrFull is returned by Put if there is no room for the object.
	ErrFull = errors.New("no room for the object")
	// ErrKey is returned for a key that is not valid.
	ErrKey = errors.New("invalid key")
)

// The maximum lengths of a key and of a name of it: those of the volumes
// that hold the objects, of which a bucket has the shortest keys and a file
// system the shortest names.
const (
	maxKey  = 1024
	maxName = 255
)

// Directory keeps each object as the file at the path of its key. An object
// is written once, under its own name, and is never changed. So depots that
// share the directory need not coordinate, and the directory may be a mounted
// bucket, whose files cannot be renamed or rewritten.
type Directory struct {
	path string
}

// Open returns the objects in the directory at path, which the process must
// be able to write.
func Open(path string) (*Directory, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	root.Close()
	const write, search = 2, 1
	if err := syscall.Access(path, write|search); err != nil {
		return nil, fmt.Errorf("%s cannot be written by user %d: %w", path, os.Getuid(), err)
	}
	return &Directory{path: path}, nil
}

// root opens the directory, which no key leads out of. Every request opens
// it anew: a handle that is kept goes stale when a network volume is served
// again after a failure.
func (d *Directory) root() (*os.Root, error) {
	return os.OpenRoot(d.path)
}

// valid reports whether key is a path of names of letters, digits, '.', '_'
// and '-'.
func valid(key string) bool {
	if key == "" || len(key) > maxKey {
		return false
	}
	for name := range strings.SplitSeq(key, "/") {
		if name == "" || name == "." || name == ".." || len(name) > maxName {
			return false
		}
		for _, c := range []byte(name) {
			letter := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
			if !letter && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
				return false
			}
		}
	}
	return true
}

// Put writes the object of a new key from body and returns its size. It
// returns ErrExists if the key has an object, even one that was not written
// whole, and ErrFull if there is no room for the object.
func (d *Directory) Put(key string, body io.Reader) (int64, error) {
	if !valid(key) {
		return 0, ErrKey
	}
	root, err := d.root()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if dir := path.Dir(key); dir != "." {
		if err := root.MkdirAll(dir, 0o700); err != nil {
			return 0, full(err)
		}
	}
	file, err := root.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return 0, ErrExists
	}
	if err != nil {
		return 0, full(err)
	}
	size, err := io.Copy(file, body)
	// A volume may accept the bytes and refuse them only when they are
	// stored: a network volume when the file is synced, a mounted bucket when
	// it is synced or closed.
	if err == nil {
		err = file.Sync()
	}
	if closed := file.Close(); err == nil {
		err = closed
	}
	if err != nil {
		// What remains of the object is not read: an object is used only
		// after it was written whole.
		root.Remove(key)
		return 0, full(err)
	}
	return size, nil
}

// full returns the error of a write, as ErrFull if the volume or a quota of
// it has no room.
func full(err error) error {
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return fmt.Errorf("%w: %w", ErrFull, err)
	}
	return err
}

// missing reports whether the error of a path means that it has no file.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// Get returns the object of the key and its size. The caller closes it. It
// returns ErrNotFound if the key has no object.
func (d *Directory) Get(key string) (io.ReadCloser, int64, error) {
	if !valid(key) {
		return nil, 0, ErrKey
	}
	root, err := d.root()
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	file, err := root.Open(key)
	if missing(err) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err == nil && info.IsDir() {
		err = ErrNotFound
	}
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// Delete deletes the object of the key and every object whose key is under
// it, such as attachments/<session>/<file> under attachments/<session>. A key
// without objects is not an error.
func (d *Directory) Delete(key string) error {
	if !valid(key) {
		return ErrKey
	}
	root, err := d.root()
	if err != nil {
		return err
	}
	defer root.Close()
	var dirs []string
	err = fs.WalkDir(root.FS(), key, func(name string, entry fs.DirEntry, err error) error {
		switch {
		case missing(err):
			// Another depot deleted it meanwhile.
			return nil
		case err != nil:
			return err
		case entry.IsDir():
			dirs = append(dirs, name)
			return nil
		}
		if err := root.Remove(name); err != nil && !missing(err) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The directories are removed as far as the volume lets them be: a
	// mounted bucket has a directory for as long as it has objects under it,
	// and no longer.
	for i := len(dirs) - 1; i >= 0; i-- {
		root.Remove(dirs[i])
	}
	return nil
}

// Volume returns the size of the volume that holds the directory and how
// much of it is free, in bytes.
func (d *Directory) Volume() (capacity, available uint64, err error) {
	var volume syscall.Statfs_t
	if err := syscall.Statfs(d.path, &volume); err != nil {
		return 0, 0, err
	}
	block := uint64(volume.Bsize)
	return uint64(volume.Blocks) * block, uint64(volume.Bavail) * block, nil
}
