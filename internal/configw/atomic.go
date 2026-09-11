package configw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrChanged means the file moved under us between read and write: an editor
// or another client got there first, and their version wins.
var ErrChanged = errors.New("config changed on disk")

// snapshot is the file as it was read, and what is checked again before it is
// replaced.
type snapshot struct {
	data []byte
	size int64
	mod  time.Time
	mode os.FileMode
}

func read(path string) (snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{
		data: data,
		size: fi.Size(),
		mod:  fi.ModTime(),
		mode: fi.Mode().Perm(),
	}, nil
}

// writeAtomic replaces path with data, but only while the file still matches
// the snapshot it was read from. The replacement goes through a temp file in
// the same directory and a rename, so a crash leaves the old file whole rather
// than half of a new one.
func writeAtomic(path string, data []byte, from snapshot) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() != from.size || !fi.ModTime().Equal(from.mod) {
		return fmt.Errorf("%w: %s", ErrChanged, filepath.Base(path))
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".lazycomd-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, from.mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
