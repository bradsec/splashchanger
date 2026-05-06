package fileutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// osRename is an overridable package-level var so tests can inject EXDEV failures.
var osRename = os.Rename

// renameOrCopy tries osRename first; if it fails with EXDEV (cross-device link),
// it falls back to copying the file contents directly and removing the source.
func renameOrCopy(tmp, dest string) error {
	err := osRename(tmp, dest)
	if err == nil {
		return nil
	}

	if linkErr, ok := errors.AsType[*os.LinkError](err); ok && errors.Is(linkErr.Err, syscall.EXDEV) {
		// Cross-filesystem: copy contents then remove tmp.
		return copyAndRemove(tmp, dest)
	}
	return err
}

// copyAndRemove copies src to dest (overwriting) and removes src.
func copyAndRemove(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	return os.Remove(src)
}

// CopyFile copies src to dest using streaming I/O, creating parent directories
// as needed. It writes to a temp file first, then renames for atomicity.
func CopyFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("could not create parent directory: %w", err)
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Write to temp file in the same directory, then rename atomically.
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return renameOrCopy(tmp, dest)
}

// WriteFileAtomic writes data to path atomically by writing to a temp file
// first, then renaming. This prevents partial writes from corrupting config files.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("could not create parent directory: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	return renameOrCopy(tmp, path)
}

// CopyDir recursively copies the directory tree rooted at src to dest,
// creating directories and copying files as needed.
func CopyDir(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Compute the destination path relative to src.
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		return CopyFile(path, target)
	})
}
