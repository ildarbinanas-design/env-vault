package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
)

const maxCheckInputBytes = 16 << 20

var sourceSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// readRegularInput reads a bounded, stable, regular file. The saved GitHub
// observations this checker consumes are never symlinks, never grow while
// being read, and never exceed the bound.
func readRegularInput(filename string) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxCheckInputBytes {
		return nil, fmt.Errorf("%s is not a bounded regular non-symlink file", filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, fmt.Errorf("%s changed identity while opening", filename)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCheckInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read stable input %s: %w", filename, err)
	}
	if int64(len(data)) != before.Size() {
		return nil, fmt.Errorf("read stable input %s: size changed from %d to %d", filename, before.Size(), len(data))
	}
	return data, nil
}

// writeCheckOutput writes to stdout for "-" and otherwise creates a new file
// exclusively, never clobbering an existing path.
func writeCheckOutput(filename string, data []byte, stdout, stderr io.Writer) int {
	if filename == "-" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	if err := writeExclusiveFile(filename, data); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	return exitOK
}
