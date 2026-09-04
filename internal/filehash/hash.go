// Package filehash defines the canonical content fingerprints used for ROM
// files and directory-shaped game resources. Directory hashes include each
// portable relative path and the exact bytes in lexical walk order.
package filehash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func File(path string) (string, int64, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, handle)
	closeErr := handle.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func Directory(root string) (string, int64, error) {
	hash := sha256.New()
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("directory artifact contains a symbolic link")
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(hash, filepath.ToSlash(rel)+"\x00"); err != nil {
			return err
		}
		handle, err := os.Open(path)
		if err != nil {
			return err
		}
		size, copyErr := io.Copy(hash, handle)
		closeErr := handle.Close()
		total += size
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), total, nil
}
