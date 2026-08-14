package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxReleaseEntries = 256

func main() {
	source := flag.String("source", "", "release bundle directory")
	output := flag.String("output", "", "output tar.gz path")
	root := flag.String("root", "", "archive root directory")
	flag.Parse()
	if flag.NArg() != 0 || *source == "" || *output == "" || !validArchivePath(*root) || strings.Contains(*root, "/") {
		fatal(errors.New("usage: package-release -source DIR -output FILE -root NAME"))
	}
	if err := packageRelease(filepath.Clean(*source), filepath.Clean(*output), *root); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "package release: %v\n", err)
	os.Exit(1)
}

func packageRelease(sourcePath, outputPath, archiveRoot string) error {
	rootInfo, err := os.Lstat(sourcePath)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("source must be a regular directory, not a symbolic link")
	}
	outputDirectory := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary, err := os.CreateTemp(outputDirectory, ".polyfleet-release-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temporary archive: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set archive permissions: %w", err)
	}
	gzipWriter, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.Header.Name = archiveRoot + ".tar"
	gzipWriter.Header.ModTime = time.Now().UTC()
	tarWriter := tar.NewWriter(gzipWriter)
	closeWriters := func() error {
		if err := tarWriter.Close(); err != nil {
			_ = gzipWriter.Close()
			return err
		}
		return gzipWriter.Close()
	}

	if err := tarWriter.WriteHeader(&tar.Header{
		Name: archiveRoot + "/", Mode: 0o755, Typeflag: tar.TypeDir,
		ModTime: rootInfo.ModTime().UTC(), Format: tar.FormatPAX,
	}); err != nil {
		_ = closeWriters()
		return fmt.Errorf("write archive root: %w", err)
	}
	entries := 1
	walkErr := filepath.WalkDir(sourcePath, func(currentPath string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if currentPath == sourcePath {
			return nil
		}
		entries++
		if entries > maxReleaseEntries {
			return errors.New("release bundle has too many entries")
		}
		relativePath, err := filepath.Rel(sourcePath, currentPath)
		if err != nil {
			return err
		}
		archiveName := archiveRoot + "/" + filepath.ToSlash(relativePath)
		if !validArchivePath(archiveName) {
			return fmt.Errorf("unsafe release path: %s", relativePath)
		}
		info, err := os.Lstat(currentPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release bundle contains a symbolic link: %s", relativePath)
		}
		switch {
		case info.IsDir():
			return tarWriter.WriteHeader(&tar.Header{
				Name: archiveName + "/", Mode: 0o755, Typeflag: tar.TypeDir,
				ModTime: info.ModTime().UTC(), Format: tar.FormatPAX,
			})
		case info.Mode().IsRegular():
			mode := int64(0o644)
			if strings.HasPrefix(filepath.ToSlash(relativePath), "bin/") || strings.HasSuffix(relativePath, ".sh") {
				mode = 0o755
			}
			header := &tar.Header{
				Name: archiveName, Mode: mode, Size: info.Size(), Typeflag: tar.TypeReg,
				ModTime: info.ModTime().UTC(), Format: tar.FormatPAX,
			}
			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}
			file, err := os.Open(currentPath)
			if err != nil {
				return err
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
				_ = file.Close()
				return fmt.Errorf("release file changed while packaging: %s", relativePath)
			}
			written, copyErr := io.Copy(tarWriter, io.LimitReader(file, info.Size()+1))
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil || written != info.Size() {
				return fmt.Errorf("copy release file: %s", relativePath)
			}
			return nil
		default:
			return fmt.Errorf("release bundle contains a special file: %s", relativePath)
		}
	})
	if walkErr != nil {
		_ = closeWriters()
		return walkErr
	}
	currentRootInfo, err := os.Lstat(sourcePath)
	if err != nil || !currentRootInfo.IsDir() || !os.SameFile(rootInfo, currentRootInfo) {
		_ = closeWriters()
		return errors.New("release bundle changed while packaging")
	}
	if err := closeWriters(); err != nil {
		return fmt.Errorf("finalize release archive: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync release archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close release archive: %w", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return fmt.Errorf("publish release archive: %w", err)
	}
	committed = true
	return nil
}

func validArchivePath(value string) bool {
	if value == "" || value == "." || value == ".." || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "//") ||
		strings.Contains(value, "/./") || strings.Contains(value, "/../") ||
		strings.HasSuffix(value, "/.") || strings.HasSuffix(value, "/..") {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._@/-", character) {
			continue
		}
		return false
	}
	return true
}
