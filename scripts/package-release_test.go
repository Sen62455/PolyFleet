package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageReleaseUsesPortableModes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "bundle")
	for _, directory := range []string{"bin", "deploy", "docs"} {
		if err := os.MkdirAll(filepath.Join(source, directory), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", directory, err)
		}
	}
	files := map[string]string{
		"bin/hyfleet-server": "binary",
		"deploy/install.sh":  "#!/usr/bin/env bash\n",
		"docs/README.md":     "documentation\n",
		"SHA256SUMS":         "checksums\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	archivePath := filepath.Join(root, "release.tar.gz")
	if err := packageRelease(source, archivePath, "polyfleet-v1.0.0-linux-amd64"); err != nil {
		t.Fatalf("packageRelease() error = %v", err)
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gzipReader.Close()
	targetModes := map[string]int64{
		"polyfleet-v1.0.0-linux-amd64/":                   0o755,
		"polyfleet-v1.0.0-linux-amd64/bin/":               0o755,
		"polyfleet-v1.0.0-linux-amd64/bin/hyfleet-server": 0o755,
		"polyfleet-v1.0.0-linux-amd64/deploy/install.sh":  0o755,
		"polyfleet-v1.0.0-linux-amd64/docs/README.md":     0o644,
		"polyfleet-v1.0.0-linux-amd64/SHA256SUMS":         0o644,
	}
	seen := make(map[string]bool, len(targetModes))
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if expectedMode, ok := targetModes[header.Name]; ok {
			if header.Mode != expectedMode {
				t.Fatalf("mode for %s = %o, want %o", header.Name, header.Mode, expectedMode)
			}
			seen[header.Name] = true
		}
		if header.Uid != 0 || header.Gid != 0 || header.Typeflag == tar.TypeSymlink {
			t.Fatalf("unsafe archive header = %#v", header)
		}
	}
	for name := range targetModes {
		if !seen[name] {
			t.Errorf("archive entry not found: %s", name)
		}
	}
}

func TestPackageReleaseRejectsUnsafeNames(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "bundle")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "unsafe name"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := packageRelease(source, filepath.Join(root, "release.tar.gz"), "polyfleet-v1.0.0"); err == nil {
		t.Fatal("packageRelease() accepted an unsafe archive path")
	}
}
