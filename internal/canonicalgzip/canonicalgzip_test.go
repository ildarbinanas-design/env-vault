package canonicalgzip

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeCanonicalBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 65535, 65536} {
		raw := bytes.Repeat([]byte{byte(size)}, size)
		encoded, err := Encode(raw)
		if err != nil {
			t.Fatalf("size %d encode: %v", size, err)
		}
		decoded, err := Decode(encoded, int64(size), int64(size), int64(len(encoded)))
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatalf("size %d decode: exact=%t error=%v", size, bytes.Equal(decoded, raw), err)
		}
	}
}

func TestDecodeRejectsFullNonFinalBlockAndEmptyFinalBlock(t *testing.T) {
	raw := bytes.Repeat([]byte{'x'}, 65535)
	canonical, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	alternate := append([]byte(nil), canonical[:len(canonical)-8]...)
	alternate[10] = 0x00
	alternate = append(alternate, 0x01, 0x00, 0x00, 0xff, 0xff)
	alternate = append(alternate, canonical[len(canonical)-8:]...)
	if _, err := Decode(alternate, int64(len(raw)), int64(len(raw)), int64(len(alternate))); err == nil {
		t.Fatal("noncanonical empty final stored block was accepted")
	}
}
