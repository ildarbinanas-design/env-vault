//go:build darwin || linux

package cli

import (
	"errors"
	"io"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

// readHiddenLine reads only one byte at a time so a pasted confirmation line
// remains available for the next prompt. A nonpositive limit is unbounded.
func readHiddenLine(read func([]byte) (int, error), limit int) (value []byte, err error) {
	var line hiddenLineBuffer
	var scratch [1]byte
	defer bundle.Wipe(scratch[:])
	defer func() {
		if err != nil {
			line.eraseFrom(0)
		}
	}()
	overflow := false
	for {
		n, readErr := read(scratch[:])
		if errors.Is(readErr, unix.EINTR) {
			continue
		}
		if readErr != nil || n == 0 {
			if overflow {
				return nil, secretstore.ErrValueTooLarge
			}
			if readErr != nil {
				return nil, readErr
			}
			if len(line.value) == 0 {
				return nil, io.EOF
			}
			return line.value, nil
		}
		b := scratch[0]
		if b == '\n' || b == '\r' {
			if overflow {
				return nil, secretstore.ErrValueTooLarge
			}
			return line.value, nil
		}
		// Once overflow is latched, editing cannot recover the rejected line.
		if overflow {
			continue
		}
		switch b {
		case 0x7f, 0x08:
			_, size := utf8.DecodeLastRune(line.value)
			line.eraseFrom(len(line.value) - size)
		case 0x15:
			line.eraseFrom(0)
		case 0x17:
			line.eraseWord()
		case 0x04:
			if len(line.value) == 0 {
				return nil, io.EOF
			}
		default:
			if limit > 0 && len(line.value) == limit {
				overflow = true
				continue
			}
			line.appendByte(b, limit)
		}
	}
}

// hiddenLineBuffer owns every allocation it creates. Removed bytes and replaced
// allocations are wiped before ownership is relinquished.
type hiddenLineBuffer struct {
	value []byte
}

func (line *hiddenLineBuffer) appendByte(b byte, limit int) {
	if len(line.value) == cap(line.value) {
		size := max(64, 2*cap(line.value))
		if limit > 0 {
			size = min(size, limit)
		}
		replacement := make([]byte, len(line.value), size)
		copy(replacement, line.value)
		bundle.Wipe(line.value[:cap(line.value)])
		line.value = replacement
	}
	line.value = append(line.value, b)
}

func (line *hiddenLineBuffer) eraseFrom(offset int) {
	bundle.Wipe(line.value[offset:])
	line.value = line.value[:offset]
}

func (line *hiddenLineBuffer) eraseWord() {
	offset := len(line.value)
	for offset > 0 {
		r, size := utf8.DecodeLastRune(line.value[:offset])
		if !unicode.IsSpace(r) {
			break
		}
		offset -= size
	}
	for offset > 0 {
		r, size := utf8.DecodeLastRune(line.value[:offset])
		if unicode.IsSpace(r) {
			break
		}
		offset -= size
	}
	line.eraseFrom(offset)
}
