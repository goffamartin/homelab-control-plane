package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	frameMagic       uint16 = 0x484c // ASCII "HL"
	HeaderSize              = 18
	MaxPayloadLength uint32 = 1 << 20 // 1 MiB; protects receivers before allocation.
)

var (
	ErrInvalidMagic       = errors.New("hlcp: invalid frame magic")
	ErrUnsupportedVersion = errors.New("hlcp: unsupported protocol version")
	ErrUnknownMessageType = errors.New("hlcp: unknown message type")
	ErrUnsupportedFlags   = errors.New("hlcp: unsupported frame flags")
	ErrPayloadTooLarge    = errors.New("hlcp: payload exceeds maximum length")
	ErrPayloadLength      = errors.New("hlcp: payload length does not match header")
)

// Header is the fixed 18-byte prefix of every HLCP v1 frame.
// All multibyte values use network byte order (big endian).
type Header struct {
	Magic         uint16
	Version       uint8
	Type          MessageType
	Flags         Flags
	MessageID     uint64
	PayloadLength uint32
}

// Frame is a decoded or outbound HLCP message.
type Frame struct {
	Header  Header
	Payload []byte
}

// NewFrame constructs a v1 frame. It copies payload so callers can safely
// reuse their source buffer after construction.
func NewFrame(messageType MessageType, messageID uint64, payload []byte) (Frame, error) {
	if uint64(len(payload)) > uint64(MaxPayloadLength) {
		return Frame{}, ErrPayloadTooLarge
	}

	copiedPayload := append([]byte(nil), payload...)
	frame := Frame{
		Header: Header{
			Magic:         frameMagic,
			Version:       ProtocolVersion,
			Type:          messageType,
			Flags:         FlagNone,
			MessageID:     messageID,
			PayloadLength: uint32(len(copiedPayload)),
		},
		Payload: copiedPayload,
	}

	return frame, frame.Header.Validate()
}

// Validate checks whether a header is acceptable to an HLCP v1 receiver.
func (header Header) Validate() error {
	if header.Magic != frameMagic {
		return ErrInvalidMagic
	}
	if header.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, header.Version)
	}
	if !header.Type.IsKnown() {
		return fmt.Errorf("%w: %d", ErrUnknownMessageType, header.Type)
	}
	if header.Flags != FlagNone {
		return fmt.Errorf("%w: %d", ErrUnsupportedFlags, header.Flags)
	}
	if header.PayloadLength > MaxPayloadLength {
		return fmt.Errorf("%w: %d", ErrPayloadTooLarge, header.PayloadLength)
	}
	return nil
}

// Encode serializes a frame into the exact bytes sent over a TLS/TCP stream.
func (frame Frame) Encode() ([]byte, error) {
	if uint64(len(frame.Payload)) > uint64(MaxPayloadLength) {
		return nil, ErrPayloadTooLarge
	}
	if frame.Header.PayloadLength != uint32(len(frame.Payload)) {
		return nil, ErrPayloadLength
	}
	if err := frame.Header.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, HeaderSize+len(frame.Payload))
	binary.BigEndian.PutUint16(encoded[0:2], frame.Header.Magic)
	encoded[2] = frame.Header.Version
	encoded[3] = byte(frame.Header.Type)
	binary.BigEndian.PutUint16(encoded[4:6], uint16(frame.Header.Flags))
	binary.BigEndian.PutUint64(encoded[6:14], frame.Header.MessageID)
	binary.BigEndian.PutUint32(encoded[14:18], frame.Header.PayloadLength)
	copy(encoded[HeaderSize:], frame.Payload)

	return encoded, nil
}

// Decode reads exactly one bounded HLCP frame from reader.
func Decode(reader io.Reader) (Frame, error) {
	headerBytes := make([]byte, HeaderSize)
	if _, err := io.ReadFull(reader, headerBytes); err != nil {
		return Frame{}, fmt.Errorf("read HLCP header: %w", err)
	}

	header := Header{
		Magic:         binary.BigEndian.Uint16(headerBytes[0:2]),
		Version:       headerBytes[2],
		Type:          MessageType(headerBytes[3]),
		Flags:         Flags(binary.BigEndian.Uint16(headerBytes[4:6])),
		MessageID:     binary.BigEndian.Uint64(headerBytes[6:14]),
		PayloadLength: binary.BigEndian.Uint32(headerBytes[14:18]),
	}
	if err := header.Validate(); err != nil {
		return Frame{}, err
	}

	payload := make([]byte, header.PayloadLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Frame{}, fmt.Errorf("read HLCP payload: %w", err)
	}

	return Frame{Header: header, Payload: payload}, nil
}

// WriteFrame writes a complete encoded frame, including when writer performs
// a short write without returning an error.
func WriteFrame(writer io.Writer, frame Frame) error {
	encoded, err := frame.Encode()
	if err != nil {
		return err
	}

	for len(encoded) > 0 {
		written, err := writer.Write(encoded)
		if err != nil {
			return fmt.Errorf("write HLCP frame: %w", err)
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		encoded = encoded[written:]
	}
	return nil
}
