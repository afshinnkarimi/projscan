package scanner

import (
	"archive/tar"

	"bufio"

	"compress/gzip"

	"errors"

	"fmt"

	"io"

	"os"

	"path"

	"strings"
)

func readTarArchive(filePath string) (archiveContents, error) {
	contents := archiveContents{
		chartFiles: make(map[string][]byte),
		javaFiles:  make(map[string][]byte),
	}
	file, err := os.Open(filePath)
	if err != nil {
		return contents, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var archiveReader io.Reader = reader
	header, peekErr := reader.Peek(2)
	if peekErr == nil && header[0] == 0x1f && header[1] == 0x8b {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return contents, fmt.Errorf("open gzip archive: %w", err)
		}
		defer gzipReader.Close()
		archiveReader = gzipReader
	}

	tarReader := tar.NewReader(archiveReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return contents, fmt.Errorf("read tar archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		fileName := cleanArchivePath(header.Name)
		if fileName == "" {
			continue
		}
		contents.files = append(contents.files, fileName)

		baseName := path.Base(fileName)
		if baseName == "manifest.json" {
			contents.manifestFound = true
			contents.manifest, contents.manifestReadErr = readArchiveMetadata(tarReader, header.Size)
			continue
		}
		if baseName == "Chart.yaml" {
			data, readErr := readArchiveMetadata(tarReader, header.Size)
			if readErr != nil {
				contents.errs = append(contents.errs, fmt.Sprintf("read %s: %v", fileName, readErr))
			} else {
				contents.chartFiles[fileName] = data
			}
			continue
		}
		if isJavaBuildFile(fileName) {
			contents.javaIndicatorsSeen = true
			data, readErr := readArchiveMetadata(tarReader, header.Size)
			if readErr != nil {
				contents.errs = append(contents.errs, fmt.Sprintf("read %s: %v", fileName, readErr))
			} else {
				contents.javaFiles[fileName] = data
			}
			continue
		}
		if isJavaBinary(fileName) {
			contents.javaIndicatorsSeen = true
			if strings.EqualFold(path.Ext(fileName), ".jar") {
				if header.Size > maxJarSize {
					contents.errs = append(contents.errs, fmt.Sprintf("read %s: JAR exceeds %d bytes", fileName, maxJarSize))
					continue
				}
				data, readErr := io.ReadAll(io.LimitReader(tarReader, maxJarSize+1))
				if readErr != nil {
					contents.errs = append(contents.errs, fmt.Sprintf("read %s: %v", fileName, readErr))
					continue
				}
				if int64(len(data)) > maxJarSize {
					contents.errs = append(contents.errs, fmt.Sprintf("read %s: JAR exceeds %d bytes", fileName, maxJarSize))
					continue
				}
				app, manifestErr := javaApplicationFromJar(fileName, data)
				if manifestErr != nil {
					contents.errs = append(contents.errs, fmt.Sprintf("read JAR manifest %s: %v", fileName, manifestErr))
				} else if app != nil {
					contents.javaApplications = append(contents.javaApplications, *app)
				}
			}
		}
	}
	contents.files = sortedUnique(contents.files)
	return contents, nil
}

func readArchiveMetadata(reader io.Reader, size int64) ([]byte, error) {
	if size > maxMetadataSize {
		return nil, fmt.Errorf("metadata file exceeds %d bytes", maxMetadataSize)
	}
	return io.ReadAll(io.LimitReader(reader, maxMetadataSize+1))
}

func cleanArchivePath(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}
