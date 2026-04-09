package assets

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var permOverrides = []struct {
	prefix string
	mode   os.FileMode
}{
	{"rootfs/etc/sudoers.d/", 0440},
	{"rootfs/usr/local/bin/", 0755},
}

// WriteFunc writes data to dest with the given permissions.
// For rootfs files this must handle privilege escalation (e.g., sudo).
type WriteFunc func(data []byte, dest string, perm os.FileMode) error

type Extractor struct {
	FS             fs.FS
	RootDir        string
	DataDir        string
	MarkerPath     string
	Remount        func(rw bool) error
	RootfsWriteFile WriteFunc // privileged writer for rootfs files (nil = use os.WriteFile)
}

func (e *Extractor) Extract(currentVersion string) error {
	marker, err := os.ReadFile(e.MarkerPath)
	if err == nil && strings.TrimSpace(string(marker)) == currentVersion {
		log.Printf("assets: version %s matches, skipping extraction", currentVersion)
		return nil
	}

	log.Printf("assets: extracting assets for version %s", currentVersion)

	rootfsFiles, err := e.collectFiles("rootfs")
	if err != nil {
		return fmt.Errorf("collect rootfs files: %w", err)
	}

	if len(rootfsFiles) > 0 {
		if err := e.Remount(true); err != nil {
			return fmt.Errorf("remount rw: %w", err)
		}
		rootfsWriter := e.RootfsWriteFile
		if rootfsWriter == nil {
			rootfsWriter = defaultWriteFile
		}
		for _, f := range rootfsFiles {
			dest := filepath.Join(e.RootDir, f.relPath)
			if err := e.writeFileWith(f, dest, rootfsWriter); err != nil {
				e.Remount(false)
				return fmt.Errorf("write rootfs %s: %w", f.relPath, err)
			}
		}
		if err := e.Remount(false); err != nil {
			log.Printf("assets: WARNING: remount ro failed: %v", err)
		}
	}

	dataFiles, err := e.collectFiles("data")
	if err != nil {
		return fmt.Errorf("collect data files: %w", err)
	}

	for _, f := range dataFiles {
		dest := filepath.Join(e.DataDir, f.relPath)
		if err := e.writeFileWith(f, dest, defaultWriteFile); err != nil {
			return fmt.Errorf("write data %s: %w", f.relPath, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(e.MarkerPath), 0755); err != nil {
		return fmt.Errorf("create marker dir: %w", err)
	}
	if err := os.WriteFile(e.MarkerPath, []byte(currentVersion), 0644); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	log.Printf("assets: extracted %d rootfs + %d data files for version %s",
		len(rootfsFiles), len(dataFiles), currentVersion)
	return nil
}

type embeddedFile struct {
	embedPath string
	relPath   string
	perm      os.FileMode
}

func (e *Extractor) collectFiles(prefix string) ([]embeddedFile, error) {
	var files []embeddedFile
	err := fs.WalkDir(e.FS, prefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(prefix, path)
		if err != nil {
			return fmt.Errorf("unexpected path %q not under %q: %w", path, prefix, err)
		}
		perm := os.FileMode(0644)
		for _, po := range permOverrides {
			if strings.HasPrefix(path, po.prefix) {
				perm = po.mode
				break
			}
		}
		files = append(files, embeddedFile{embedPath: path, relPath: rel, perm: perm})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return files, nil
}

func defaultWriteFile(data []byte, dest string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	os.Remove(dest) // remove existing file in case of ownership mismatch
	return os.WriteFile(dest, data, perm)
}

func (e *Extractor) writeFileWith(f embeddedFile, dest string, writeFn WriteFunc) error {
	data, err := fs.ReadFile(e.FS, f.embedPath)
	if err != nil {
		return err
	}
	if err := writeFn(data, dest, f.perm); err != nil {
		return err
	}
	log.Printf("assets: wrote %s (%d bytes, %o)", dest, len(data), f.perm)
	return nil
}
