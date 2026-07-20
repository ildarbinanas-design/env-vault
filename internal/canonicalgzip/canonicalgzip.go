// Package canonicalgzip implements the repository's versioned canonical gzip
// representation. It deliberately uses stored DEFLATE blocks instead of a Go
// compression encoder so content-addressed bytes cannot change with encoder
// heuristics or toolchain versions.
package canonicalgzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

var header = []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff}

// Encode emits one gzip member with zero mtime, no name/comment, OS 255, and
// maximal 65,535-byte stored DEFLATE blocks.
func Encode(data []byte) ([]byte, error) {
	capacity, err := Capacity(len(data))
	if err != nil {
		return nil, err
	}
	output := make([]byte, 0, capacity)
	output = append(output, header...)
	remaining := data
	for {
		length := len(remaining)
		if length > 65535 {
			length = 65535
		}
		final := length == len(remaining)
		if final {
			output = append(output, 0x01)
		} else {
			output = append(output, 0x00)
		}
		value := uint16(length)
		output = binary.LittleEndian.AppendUint16(output, value)
		output = binary.LittleEndian.AppendUint16(output, ^value)
		output = append(output, remaining[:length]...)
		remaining = remaining[length:]
		if final {
			break
		}
	}
	output = binary.LittleEndian.AppendUint32(output, crc32.ChecksumIEEE(data))
	output = binary.LittleEndian.AppendUint32(output, uint32(len(data)))
	return output, nil
}

// Capacity returns the exact encoded size without overflowing int.
func Capacity(dataLength int) (int, error) {
	if dataLength < 0 {
		return 0, errors.New("gzip input length is negative")
	}
	blocks := dataLength / 65535
	if dataLength%65535 != 0 || blocks == 0 {
		blocks++
	}
	const fixedOverhead = 10 + 8
	maxInt := int(^uint(0) >> 1)
	if dataLength > maxInt-fixedOverhead {
		return 0, errors.New("gzip output capacity overflows int")
	}
	capacity := dataLength + fixedOverhead
	if blocks > (maxInt-capacity)/5 {
		return 0, errors.New("gzip output capacity overflows int")
	}
	return capacity + blocks*5, nil
}

// Decode accepts only Encode's exact single-member representation and applies
// caller-owned raw and encoded byte limits before allocating the output.
func Decode(compressed []byte, declaredSize, maxUncompressed, maxCompressed int64) ([]byte, error) {
	if declaredSize < 0 || maxUncompressed < 0 || maxCompressed < 0 || declaredSize > maxUncompressed ||
		len(compressed) == 0 || int64(len(compressed)) > maxCompressed {
		return nil, errors.New("compressed or declared object size is outside the supported limit")
	}
	if len(compressed) < len(header)+5+8 || !bytes.Equal(compressed[:len(header)], header) {
		return nil, errors.New("gzip header is not the canonical stored-block header")
	}
	position := len(header)
	output := make([]byte, 0, int(declaredSize))
	for {
		if position+5 > len(compressed)-8 {
			return nil, errors.New("gzip stored block is truncated")
		}
		blockHeader := compressed[position]
		position++
		if blockHeader != 0x00 && blockHeader != 0x01 {
			return nil, errors.New("gzip stream is not canonical stored-block DEFLATE")
		}
		length := binary.LittleEndian.Uint16(compressed[position : position+2])
		inverse := binary.LittleEndian.Uint16(compressed[position+2 : position+4])
		position += 4
		remaining := declaredSize - int64(len(output))
		expectedLength := remaining
		if expectedLength > 65535 {
			expectedLength = 65535
		}
		expectedFinal := remaining <= 65535
		if inverse != ^length || int64(length) != expectedLength || (blockHeader == 0x01) != expectedFinal || position+int(length) > len(compressed)-8 {
			return nil, errors.New("gzip stored block length is invalid")
		}
		output = append(output, compressed[position:position+int(length)]...)
		position += int(length)
		if int64(len(output)) > declaredSize {
			return nil, errors.New("gzip decoded bytes exceed the declared size")
		}
		if blockHeader == 0x01 {
			break
		}
	}
	if position+8 != len(compressed) || int64(len(output)) != declaredSize {
		return nil, errors.New("gzip decoded size or trailing bytes mismatch")
	}
	wantCRC := binary.LittleEndian.Uint32(compressed[position : position+4])
	wantSize := binary.LittleEndian.Uint32(compressed[position+4 : position+8])
	if wantCRC != crc32.ChecksumIEEE(output) || wantSize != uint32(len(output)) {
		return nil, errors.New("gzip trailer checksum or size mismatch")
	}
	return output, nil
}
