package config

import (
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// atomicWriteFile writes data to a file atomically by writing to a unique
// temporary file in the same directory and renaming it into place. This
// prevents concurrent readers from observing a partially-written file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// On Windows, os.Rename can fail with "Access is denied" when multiple
	// writers race in the same directory. Retry a few times with a short
	// back-off to work around the issue.
	retries := 3
	if runtime.GOOS == "windows" {
		retries = 5
	}
	for i := 0; i < retries; i++ {
		if err := os.Rename(tmp, path); err != nil {
			if i < retries-1 {
				time.Sleep(time.Millisecond * 50)
				continue
			}
			os.Remove(tmp)
			return err
		}
		return nil
	}
	return nil
}
