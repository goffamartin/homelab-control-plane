package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	original, err := NewFrame(MessageHello, 42, []byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}

	encoded, err := original.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	decoded, err := Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if decoded.Header != original.Header {
		t.Fatalf("decoded header = %#v, want %#v", decoded.Header, original.Header)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Fatalf("decoded payload = %x, want %x", decoded.Payload, original.Payload)
	}
}

func TestDecodeRejectsInvalidMagic(t *testing.T) {
	encoded := rawHeader(t, 0x0000, ProtocolVersion, MessageHello, FlagNone, 1, 0)
	_, err := Decode(bytes.NewReader(encoded))
	if !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("Decode() error = %v, want ErrInvalidMagic", err)
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	encoded := rawHeader(t, frameMagic, ProtocolVersion+1, MessageHello, FlagNone, 1, 0)
	_, err := Decode(bytes.NewReader(encoded))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Decode() error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestDecodeRejectsExcessivePayloadBeforeAllocation(t *testing.T) {
	encoded := rawHeader(t, frameMagic, ProtocolVersion, MessageHello, FlagNone, 1, MaxPayloadLength+1)
	_, err := Decode(bytes.NewReader(encoded))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("Decode() error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestEncodeRejectsHeaderPayloadMismatch(t *testing.T) {
	frame, err := NewFrame(MessageHello, 1, []byte("payload"))
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}
	frame.Header.PayloadLength++

	_, err = frame.Encode()
	if !errors.Is(err, ErrPayloadLength) {
		t.Fatalf("Encode() error = %v, want ErrPayloadLength", err)
	}
}

func rawHeader(t *testing.T, magic uint16, version uint8, messageType MessageType, flags Flags, messageID uint64, payloadLength uint32) []byte {
	t.Helper()

	encoded := make([]byte, HeaderSize)
	binary.BigEndian.PutUint16(encoded[0:2], magic)
	encoded[2] = version
	encoded[3] = byte(messageType)
	binary.BigEndian.PutUint16(encoded[4:6], uint16(flags))
	binary.BigEndian.PutUint64(encoded[6:14], messageID)
	binary.BigEndian.PutUint32(encoded[14:18], payloadLength)
	return encoded
}
