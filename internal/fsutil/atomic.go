// Package fsutil holds the file helpers several packages of the panel and the node share.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data so that a reader, or a crash, sees the old file
// or the whole new one: the data goes to a temporary file in the same directory, is
// flushed to disk and renamed over the target.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
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
	return os.Rename(tmp.Name(), path)
}
