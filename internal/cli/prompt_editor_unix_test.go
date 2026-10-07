//go:build darwin || linux

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

func TestHiddenPromptEditing(t *testing.T) {
	for _, erase := range []byte{0x7f, 0x08} {
		for _, suffix := range []struct {
			name  string
			bytes []byte
		}{
			{"utf8", []byte("界")},
			{"invalid-tail", []byte{0xff}},
		} {
			t.Run(fmt.Sprintf("backspace-%x/%s", erase, suffix.name), func(t *testing.T) {
				value := ephemeralPromptInput(t, 43)
				input := append(append(append([]byte(nil), value...), suffix.bytes...), erase, '\n')
				t.Cleanup(func() { bundle.Wipe(value); bundle.Wipe(input) })
				runHiddenPromptInput(t, "secret", input, value, len(input))
			})
		}
	}
	for _, name := range []string{"clear-line", "erase-word", "erase-invalid-word", "erase-empty", "nonempty-eof", "carriage-return", "edit-at-limit"} {
		t.Run(name, func(t *testing.T) {
			value := ephemeralPromptInput(t, 43)
			if name == "edit-at-limit" {
				bundle.Wipe(value)
				value = ephemeralPromptInput(t, secretstore.MaxValueBytes)
			}
			input := append([]byte(nil), value...)
			switch name {
			case "clear-line":
				input = append(input, 0x15)
				input = append(input, value...)
			case "erase-word", "erase-invalid-word":
				// Fixed bytes here are editing syntax, never a secret fixture.
				separator := []byte("\u2003")
				input = append(input, separator...)
				value = append(value, separator...)
				word := ephemeralPromptInput(t, 43)
				input = append(input, word...)
				bundle.Wipe(word)
				if name == "erase-invalid-word" {
					input = append(input, 0xff)
				}
				input = append(input, []byte("\t\u2002 ")...)
				input = append(input, 0x17)
			case "erase-empty":
				input = append([]byte{0x7f, 0x08, 0x15, 0x17}, input...)
			case "nonempty-eof":
				input = append(input, 0x04)
			case "edit-at-limit":
				input = append(input, 0x7f, value[len(value)-1])
			}
			terminator := byte('\n')
			if name == "carriage-return" {
				terminator = '\r'
			}
			input = append(input, terminator)
			t.Cleanup(func() { bundle.Wipe(value); bundle.Wipe(input) })
			runHiddenPromptInput(t, "secret", input, value, len(input))
		})
	}
	t.Run("empty-eof", func(t *testing.T) {
		runHiddenPromptInput(t, "secret-empty-eof", []byte{0x04}, nil, 1)
	})
}

func TestHiddenPromptOverflowDrainsLineBeforeNextReader(t *testing.T) {
	for _, mode := range []string{"enter", "editing-after-overflow"} {
		t.Run(mode, func(t *testing.T) {
			input := ephemeralPromptInput(t, secretstore.MaxValueBytes+1)
			if mode == "editing-after-overflow" {
				input = append(input, 0x7f, 0x08, 0x15, 0x17, 0x04)
				replacement := ephemeralPromptInput(t, 43)
				input = append(input, replacement...)
				bundle.Wipe(replacement)
			}
			input = append(input, '\n')
			next := ephemeralPromptInput(t, 43)
			input = append(input, next...)
			input = append(input, '\n')
			t.Cleanup(func() { bundle.Wipe(input); bundle.Wipe(next) })
			runHiddenPromptInput(t, "secret-overflow-next", input, next, len(input))
		})
	}
}

func TestHiddenPassphraseLongInputAndConfirmation(t *testing.T) {
	for _, length := range []int{6050, secretstore.MaxValueBytes + 1} {
		for _, kind := range []string{"passphrase", "confirmation"} {
			t.Run(fmt.Sprintf("length-%d/%s", length, kind), func(t *testing.T) {
				value := ephemeralPromptInput(t, length)
				input := append(append([]byte(nil), value...), '\n')
				if kind == "confirmation" {
					input = append(input, input...)
					if !bytes.Equal(input[:length], input[length+1:2*length+1]) {
						t.Fatal("length/digest mismatch")
					}
				}
				t.Cleanup(func() { bundle.Wipe(input); bundle.Wipe(value) })
				runHiddenPromptInput(t, kind, input, value, len(input))
			})
		}
	}
}

func TestHiddenPassphraseInvalidInputRestoresTerminal(t *testing.T) {
	for _, kind := range []string{"empty", "short", "mismatch"} {
		t.Run(kind, func(t *testing.T) {
			var input []byte
			switch kind {
			case "short":
				input = ephemeralPromptInput(t, 11)
			case "mismatch":
				input = ephemeralPromptInput(t, 6050)
				input = append(input, '\n')
				second := ephemeralPromptInput(t, 6050)
				input = append(input, second...)
				bundle.Wipe(second)
			}
			input = append(input, '\n')
			t.Cleanup(func() { bundle.Wipe(input) })
			runHiddenPromptInput(t, "confirmation-invalid", input, nil, len(input))
		})
	}
}

func TestHiddenLinePreservesOtherBytes(t *testing.T) {
	value := ephemeralPromptInput(t, 43)
	// These suffix bytes exercise lossless input, not a fixed secret payload.
	value = append(value, 0, 1, 2, 0xff)
	input := append(append([]byte(nil), value...), '\n')
	defer bundle.Wipe(input)
	defer bundle.Wipe(value)
	reader := bytes.NewReader(input)
	got, err := readHiddenLine(reader.Read, secretstore.MaxValueBytes)
	defer bundle.Wipe(got)
	if err != nil || !bytes.Equal(got, value) {
		t.Fatal("length/digest mismatch")
	}
}

func TestHiddenLineOverflowCannotReturnPartialInput(t *testing.T) {
	for _, ending := range []string{"actual-eof", "read-error"} {
		t.Run(ending, func(t *testing.T) {
			input := ephemeralPromptInput(t, secretstore.MaxValueBytes+1)
			defer bundle.Wipe(input)
			reader := bytes.NewReader(input)
			var scratch []byte
			value, err := readHiddenLine(func(p []byte) (int, error) {
				if len(p) != 1 {
					t.Fatal("hidden reader consumed more than one byte")
				}
				scratch = p
				if reader.Len() > 0 {
					return reader.Read(p)
				}
				if ending == "read-error" {
					return 0, unix.EIO
				}
				return 0, nil
			}, secretstore.MaxValueBytes)
			defer bundle.Wipe(value)
			if !errors.Is(err, secretstore.ErrValueTooLarge) || value != nil {
				t.Fatal("overflow returned partial input or the wrong error")
			}
			assertPromptBytesWiped(t, scratch)
		})
	}
}

func TestHiddenLineEOFAndReadErrors(t *testing.T) {
	for _, name := range []string{"empty-eof", "nonempty-eof", "read-error", "interrupted-read"} {
		t.Run(name, func(t *testing.T) {
			input := ephemeralPromptInput(t, 43)
			if name == "empty-eof" {
				bundle.Wipe(input)
				input = nil
			}
			defer bundle.Wipe(input)
			reader := bytes.NewReader(input)
			first := true
			value, err := readHiddenLine(func(p []byte) (int, error) {
				if first && name == "interrupted-read" {
					first = false
					return 0, unix.EINTR
				}
				if reader.Len() > 0 {
					return reader.Read(p)
				}
				if name == "read-error" {
					return 0, unix.EIO
				}
				return 0, nil
			}, secretstore.MaxValueBytes)
			defer bundle.Wipe(value)
			switch name {
			case "empty-eof":
				if !errors.Is(err, io.EOF) || value != nil {
					t.Fatal("empty EOF result differs")
				}
			case "read-error":
				if !errors.Is(err, unix.EIO) || value != nil {
					t.Fatal("read failure returned partial input or the wrong error")
				}
			default:
				if err != nil || !bytes.Equal(value, input) {
					t.Fatal("length/digest mismatch")
				}
			}
		})
	}
}

func TestHiddenLineBufferWipesOwnedMemory(t *testing.T) {
	var line hiddenLineBuffer
	input := ephemeralPromptInput(t, 65)
	defer bundle.Wipe(input)
	defer func() { bundle.Wipe(line.value[:cap(line.value)]) }()
	for _, b := range input[:64] {
		line.appendByte(b, secretstore.MaxValueBytes)
	}
	replaced := line.value[:cap(line.value)]
	line.appendByte(input[64], secretstore.MaxValueBytes)
	assertPromptBytesWiped(t, replaced)
	if !bytes.Equal(line.value, input) {
		t.Fatal("length/digest mismatch")
	}
	erased := line.value[60:]
	line.eraseFrom(60)
	assertPromptBytesWiped(t, erased)
	if !bytes.Equal(line.value, input[:60]) {
		t.Fatal("length/digest mismatch")
	}
	cleared := line.value
	line.eraseFrom(0)
	assertPromptBytesWiped(t, cleared)
	for _, b := range input {
		line.appendByte(b, secretstore.MaxValueBytes)
	}
	word := line.value
	line.eraseWord()
	assertPromptBytesWiped(t, word)
	if len(line.value) != 0 {
		t.Fatal("word erase left input")
	}
}

func assertPromptBytesWiped(t *testing.T, value []byte) {
	t.Helper()
	for _, b := range value {
		if b != 0 {
			t.Fatal("owned input memory was not wiped")
		}
	}
}
