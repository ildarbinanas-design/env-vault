// Package releasearchive safely extracts the fixed set of env-vault release
// archives for release-time inspection.
package releasearchive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxEntriesPerArchive = 64
	maxArchiveFileBytes  = 64 << 20
	maxFileBytes         = 64 << 20
	maxArchiveBytes      = 128 << 20
	maxTotalBytes        = 256 << 20
	maxTarPaddingBytes   = 1 << 20
)

type archiveFormat uint8

const (
	formatTarGz archiveFormat = iota
	formatZip
)

type archiveSpec struct {
	name   string
	root   string
	format archiveFormat
}

var releaseArchives = [...]archiveSpec{
	{name: "env-vault-linux-amd64.tar.gz", root: "env-vault-linux-amd64", format: formatTarGz},
	{name: "env-vault-linux-arm64.tar.gz", root: "env-vault-linux-arm64", format: formatTarGz},
	{name: "env-vault-darwin-amd64.tar.gz", root: "env-vault-darwin-amd64", format: formatTarGz},
	{name: "env-vault-darwin-arm64.tar.gz", root: "env-vault-darwin-arm64", format: formatTarGz},
	{name: "env-vault-windows-amd64.zip", root: "env-vault-windows-amd64", format: formatZip},
}

type extractionLimits struct {
	entriesPerArchive int
	fileBytes         int64
	archiveBytes      int64
	totalBytes        int64
}

var defaultLimits = extractionLimits{
	entriesPerArchive: maxEntriesPerArchive,
	fileBytes:         maxFileBytes,
	archiveBytes:      maxArchiveBytes,
	totalBytes:        maxTotalBytes,
}

type entryKind uint8

const (
	entryDirectory entryKind = iota
	entryFile
)

type trackedEntry struct {
	kind     entryKind
	explicit bool
}

type extractionState struct {
	entries    map[string]trackedEntry
	totalBytes int64
	limits     extractionLimits
}

// ExtractAll extracts exactly the five supported release archives from
// inputDir. The input directory may also contain their .sha256 sidecars, but
// no other entries. outputDir must either not exist or be an empty directory.
func ExtractAll(inputDir, outputDir string) error {
	if err := validateInputDirectory(inputDir); err != nil {
		return err
	}
	if err := prepareOutputDirectory(outputDir); err != nil {
		return err
	}

	state := newExtractionState(defaultLimits)
	for _, spec := range releaseArchives {
		archivePath := filepath.Join(inputDir, spec.name)
		if err := extractArchive(spec, archivePath, outputDir, state); err != nil {
			return err
		}
	}
	return nil
}

// ExtractArchive extracts one supported release archive. Its basename must be
// one of the five fixed release archive names. outputDir must either not exist
// or be an empty directory.
func ExtractArchive(archivePath, outputDir string) error {
	spec, ok := archiveSpecByName(filepath.Base(archivePath))
	if !ok {
		return fmt.Errorf("unsupported release archive name %q", filepath.Base(archivePath))
	}
	if err := requireRegularFile(archivePath); err != nil {
		return fmt.Errorf("archive %s: %w", spec.name, err)
	}
	if err := prepareOutputDirectory(outputDir); err != nil {
		return err
	}
	return extractArchive(spec, archivePath, outputDir, newExtractionState(defaultLimits))
}

func newExtractionState(limits extractionLimits) *extractionState {
	return &extractionState{
		entries: make(map[string]trackedEntry),
		limits:  limits,
	}
}

func archiveSpecByName(name string) (archiveSpec, bool) {
	for _, spec := range releaseArchives {
		if name == spec.name {
			return spec, true
		}
	}
	return archiveSpec{}, false
}

func validateInputDirectory(inputDir string) error {
	info, err := os.Lstat(inputDir)
	if err != nil {
		return fmt.Errorf("input directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("input directory must be a real directory: %s", inputDir)
	}

	allowed := make(map[string]bool, len(releaseArchives)*2)
	for _, spec := range releaseArchives {
		allowed[spec.name] = true
		allowed[spec.name+".sha256"] = true
	}

	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return fmt.Errorf("read input directory: %w", err)
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return fmt.Errorf("unexpected input entry %q", entry.Name())
		}
		if err := requireRegularFile(filepath.Join(inputDir, entry.Name())); err != nil {
			return fmt.Errorf("input entry %s: %w", entry.Name(), err)
		}
	}
	for _, spec := range releaseArchives {
		if err := requireRegularFile(filepath.Join(inputDir, spec.name)); err != nil {
			return fmt.Errorf("required archive %s: %w", spec.name, err)
		}
	}
	return nil
}

func prepareOutputDirectory(outputDir string) error {
	info, err := os.Lstat(outputDir)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("output path must be a real directory: %s", outputDir)
		}
		entries, readErr := os.ReadDir(outputDir)
		if readErr != nil {
			return fmt.Errorf("read output directory: %w", readErr)
		}
		if len(entries) != 0 {
			return fmt.Errorf("output directory must be empty: %s", outputDir)
		}
		return nil
	case errors.Is(err, os.ErrNotExist):
		if mkdirErr := os.Mkdir(outputDir, 0o755); mkdirErr != nil {
			return fmt.Errorf("create output directory: %w", mkdirErr)
		}
		return nil
	default:
		return fmt.Errorf("inspect output path: %w", err)
	}
}

func requireRegularFile(filename string) error {
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("must be a regular file: %s", filename)
	}
	return nil
}

func extractArchive(spec archiveSpec, archivePath, outputDir string, state *extractionState) error {
	info, err := os.Lstat(archivePath)
	if err != nil {
		return fmt.Errorf("archive %s: %w", spec.name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("archive %s: must be a regular file: %s", spec.name, archivePath)
	}
	if info.Size() > maxArchiveFileBytes {
		return fmt.Errorf("archive %s: compressed size %d exceeds limit %d", spec.name, info.Size(), maxArchiveFileBytes)
	}
	if info.Size() < 0 {
		return fmt.Errorf("archive %s: compressed size is negative", spec.name)
	}

	var extractErr error
	switch spec.format {
	case formatTarGz:
		extractErr = extractTarGz(spec, archivePath, outputDir, state)
	case formatZip:
		extractErr = extractZip(spec, archivePath, outputDir, state)
	default:
		extractErr = errors.New("unsupported archive format")
	}
	if extractErr != nil {
		return fmt.Errorf("archive %s: %w", spec.name, extractErr)
	}
	return nil
}

func extractTarGz(spec archiveSpec, archivePath, outputDir string, state *extractionState) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	entryCount := 0
	var archiveBytes int64
	var regularFiles int
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read tar entry: %w", nextErr)
		}
		// Keep this source-local guard in addition to validateEntryName's
		// canonical component checks. Besides conservatively rejecting every
		// traversal-like spelling, it makes the archive-to-filesystem trust
		// boundary explicit to static data-flow analysis.
		if strings.Contains(header.Name, "..") {
			return fmt.Errorf("entry %q: double-dot sequences in paths are not allowed", header.Name)
		}
		entryCount++
		if entryCount > state.limits.entriesPerArchive {
			return fmt.Errorf("entry count exceeds limit %d", state.limits.entriesPerArchive)
		}

		kind, typeErr := tarEntryKind(header)
		if typeErr != nil {
			return fmt.Errorf("entry %q: %w", header.Name, typeErr)
		}
		entryName, nameErr := validateEntryName(header.Name, spec.root, kind)
		if nameErr != nil {
			return fmt.Errorf("entry %q: %w", header.Name, nameErr)
		}
		if registerErr := state.registerEntry(entryName, kind); registerErr != nil {
			return fmt.Errorf("entry %q: %w", header.Name, registerErr)
		}

		if kind == entryDirectory {
			if header.Size != 0 {
				return fmt.Errorf("entry %q: directory has non-zero size", header.Name)
			}
			if err := ensureDirectory(outputDir, entryName); err != nil {
				return fmt.Errorf("entry %q: %w", header.Name, err)
			}
			continue
		}

		if err := state.reserveBytes(header.Size, &archiveBytes); err != nil {
			return fmt.Errorf("entry %q: %w", header.Name, err)
		}
		if err := writeRegularFile(outputDir, entryName, header.FileInfo().Mode(), tarReader, header.Size); err != nil {
			return fmt.Errorf("entry %q: %w", header.Name, err)
		}
		regularFiles++
	}
	if entryCount == 0 || regularFiles == 0 {
		return errors.New("archive must contain at least one regular file")
	}
	if err := verifyTarPadding(gzipReader); err != nil {
		return err
	}
	return nil
}

func verifyTarPadding(reader io.Reader) error {
	buffer := make([]byte, 32<<10)
	total := 0
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			total += count
			if total > maxTarPaddingBytes {
				return fmt.Errorf("tar padding exceeds limit %d", maxTarPaddingBytes)
			}
			for _, value := range buffer[:count] {
				if value != 0 {
					return errors.New("tar stream contains non-zero trailing data")
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read gzip trailer: %w", err)
		}
		if count == 0 {
			return errors.New("gzip stream made no progress")
		}
	}
}

func tarEntryKind(header *tar.Header) (entryKind, error) {
	switch header.Typeflag {
	case tar.TypeDir:
		return entryDirectory, nil
	case tar.TypeReg, tar.TypeRegA:
		return entryFile, nil
	case tar.TypeSymlink:
		return 0, errors.New("symbolic links are not allowed")
	case tar.TypeLink:
		return 0, errors.New("hard links are not allowed")
	default:
		return 0, fmt.Errorf("unsupported tar entry type %d", header.Typeflag)
	}
}

func extractZip(spec archiveSpec, archivePath, outputDir string, state *extractionState) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer reader.Close()

	if len(reader.File) > state.limits.entriesPerArchive {
		return fmt.Errorf("entry count exceeds limit %d", state.limits.entriesPerArchive)
	}
	var archiveBytes int64
	var regularFiles int
	for _, file := range reader.File {
		// See the equivalent tar guard above. This must remain before the entry
		// name is propagated to any filesystem helper.
		if strings.Contains(file.Name, "..") {
			return fmt.Errorf("entry %q: double-dot sequences in paths are not allowed", file.Name)
		}
		kind, typeErr := zipEntryKind(file)
		if typeErr != nil {
			return fmt.Errorf("entry %q: %w", file.Name, typeErr)
		}
		entryName, nameErr := validateEntryName(file.Name, spec.root, kind)
		if nameErr != nil {
			return fmt.Errorf("entry %q: %w", file.Name, nameErr)
		}
		if registerErr := state.registerEntry(entryName, kind); registerErr != nil {
			return fmt.Errorf("entry %q: %w", file.Name, registerErr)
		}

		if kind == entryDirectory {
			if file.UncompressedSize64 != 0 {
				return fmt.Errorf("entry %q: directory has non-zero size", file.Name)
			}
			if err := ensureDirectory(outputDir, entryName); err != nil {
				return fmt.Errorf("entry %q: %w", file.Name, err)
			}
			continue
		}

		if file.UncompressedSize64 > uint64(^uint64(0)>>1) {
			return fmt.Errorf("entry %q: uncompressed size is too large", file.Name)
		}
		size := int64(file.UncompressedSize64)
		if err := state.reserveBytes(size, &archiveBytes); err != nil {
			return fmt.Errorf("entry %q: %w", file.Name, err)
		}
		entryReader, openErr := file.Open()
		if openErr != nil {
			return fmt.Errorf("entry %q: open: %w", file.Name, openErr)
		}
		writeErr := writeRegularFile(outputDir, entryName, file.Mode(), entryReader, size)
		closeErr := entryReader.Close()
		if writeErr != nil {
			return fmt.Errorf("entry %q: %w", file.Name, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("entry %q: close: %w", file.Name, closeErr)
		}
		regularFiles++
	}
	if len(reader.File) == 0 || regularFiles == 0 {
		return errors.New("archive must contain at least one regular file")
	}
	return nil
}

func zipEntryKind(file *zip.File) (entryKind, error) {
	mode := file.Mode()
	if mode&os.ModeSymlink != 0 {
		return 0, errors.New("symbolic links are not allowed")
	}
	if file.FileInfo().IsDir() {
		return entryDirectory, nil
	}
	if !mode.IsRegular() {
		return 0, fmt.Errorf("unsupported zip entry mode %s", mode)
	}
	return entryFile, nil
}

func validateEntryName(rawName, expectedRoot string, kind entryKind) (string, error) {
	if rawName == "" {
		return "", errors.New("empty path is not allowed")
	}
	if !utf8.ValidString(rawName) {
		return "", errors.New("path is not valid UTF-8")
	}
	if strings.ContainsRune(rawName, '\x00') {
		return "", errors.New("NUL in path is not allowed")
	}
	if strings.Contains(rawName, `\`) {
		return "", errors.New("backslashes in paths are not allowed")
	}
	if strings.HasPrefix(rawName, "/") || path.IsAbs(rawName) || hasWindowsVolume(rawName) {
		return "", errors.New("absolute path is not allowed")
	}

	hasTrailingSlash := strings.HasSuffix(rawName, "/")
	name := strings.TrimSuffix(rawName, "/")
	if hasTrailingSlash && strings.HasSuffix(name, "/") {
		return "", errors.New("non-canonical path is not allowed")
	}
	if hasTrailingSlash && kind != entryDirectory {
		return "", errors.New("regular file path must not end with a slash")
	}
	if name == "" || path.Clean(name) != name {
		return "", errors.New("non-canonical path is not allowed")
	}
	for _, component := range strings.Split(name, "/") {
		if component == "." || component == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	if name != expectedRoot && !strings.HasPrefix(name, expectedRoot+"/") {
		return "", fmt.Errorf("path must be rooted at %q", expectedRoot)
	}
	if name == expectedRoot && kind != entryDirectory {
		return "", fmt.Errorf("archive root %q must be a directory", expectedRoot)
	}
	return name, nil
}

func hasWindowsVolume(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':'
}

func (state *extractionState) registerEntry(name string, kind entryKind) error {
	if existing, ok := state.entries[name]; ok {
		if existing.kind != kind {
			return errors.New("file/directory collision")
		}
		if existing.explicit {
			return errors.New("duplicate archive entry")
		}
		existing.explicit = true
		state.entries[name] = existing
		return nil
	}

	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		if existing, ok := state.entries[parent]; ok {
			if existing.kind == entryFile {
				return fmt.Errorf("parent %q is a regular file", parent)
			}
			continue
		}
		state.entries[parent] = trackedEntry{kind: entryDirectory}
	}
	if kind == entryFile {
		prefix := name + "/"
		for existingName := range state.entries {
			if strings.HasPrefix(existingName, prefix) {
				return fmt.Errorf("regular file collides with child %q", existingName)
			}
		}
	}
	state.entries[name] = trackedEntry{kind: kind, explicit: true}
	return nil
}

func (state *extractionState) reserveBytes(size int64, archiveBytes *int64) error {
	if size < 0 {
		return errors.New("negative file size")
	}
	if size > state.limits.fileBytes {
		return fmt.Errorf("file size %d exceeds limit %d", size, state.limits.fileBytes)
	}
	if size > state.limits.archiveBytes-*archiveBytes {
		return fmt.Errorf("archive size exceeds limit %d", state.limits.archiveBytes)
	}
	if size > state.limits.totalBytes-state.totalBytes {
		return fmt.Errorf("total extracted size exceeds limit %d", state.limits.totalBytes)
	}
	*archiveBytes += size
	state.totalBytes += size
	return nil
}

func ensureDirectory(outputDir, slashName string) error {
	current := outputDir
	for _, component := range strings.Split(slashName, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("path component is not a real directory: %s", slashName)
			}
		case errors.Is(err, os.ErrNotExist):
			if mkdirErr := os.Mkdir(current, 0o755); mkdirErr != nil {
				return fmt.Errorf("create directory: %w", mkdirErr)
			}
		default:
			return fmt.Errorf("inspect directory: %w", err)
		}
	}
	return nil
}

func writeRegularFile(outputDir, slashName string, archiveMode os.FileMode, source io.Reader, expectedSize int64) error {
	parent := path.Dir(slashName)
	if parent != "." {
		if err := ensureDirectory(outputDir, parent); err != nil {
			return err
		}
	}

	mode := os.FileMode(0o644)
	if archiveMode&0o111 != 0 {
		mode = 0o755
	}
	destination := filepath.Join(outputDir, filepath.FromSlash(slashName))
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create regular file: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(destination)
		}
	}()

	written, copyErr := io.Copy(file, io.LimitReader(source, expectedSize+1))
	if copyErr != nil {
		_ = file.Close()
		return fmt.Errorf("write regular file: %w", copyErr)
	}
	if written != expectedSize {
		_ = file.Close()
		return fmt.Errorf("uncompressed size mismatch: wrote %d, expected %d", written, expectedSize)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close regular file: %w", err)
	}
	complete = true
	return nil
}
