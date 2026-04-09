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

type Extractor struct {
	FS         fs.FS
	RootDir    string
	DataDir    string
	MarkerPath string
	Remount    func(rw bool) error
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
		for _, f := range rootfsFiles {
			dest := filepath.Join(e.RootDir, f.relPath)
			if err := e.writeFile(f, dest); err != nil {
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
		if err := e.writeFile(f, dest); err != nil {
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
		rel, _ := filepath.Rel(prefix, path)
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

func (e *Extractor) writeFile(f embeddedFile, dest string) error {
	data, err := fs.ReadFile(e.FS, f.embedPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, f.perm); err != nil {
		return err
	}
	log.Printf("assets: wrote %s (%d bytes, %o)", dest, len(data), f.perm)
	return nil
}
