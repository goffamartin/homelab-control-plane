package protocol

import (
	"bytes"
	"testing"
)

func FuzzFrameDecode(f *testing.F) {
	frame, _ := NewFrame(MessageHello, 1, []byte{0xa0})
	seed, _ := frame.Encode()
	f.Add(seed)
	f.Add([]byte("HL"))
	f.Add(rawHeaderForFuzz(MaxPayloadLength + 1))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > int(MaxPayloadLength)+HeaderSize {
			t.Skip()
		}
		frame, err := Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		if frame.Header.Validate() != nil || int(frame.Header.PayloadLength) != len(frame.Payload) {
			t.Fatal("invalid accepted frame")
		}
		encoded, err := frame.Encode()
		if err != nil || !bytes.Equal(encoded, data[:len(encoded)]) {
			t.Fatal("accepted frame did not round-trip")
		}
	})
}

func rawHeaderForFuzz(length uint32) []byte {
	// Encode refuses mismatched payloads; seed the wire header directly instead.
	data := make([]byte, HeaderSize)
	data[0], data[1], data[2], data[3] = 'H', 'L', ProtocolVersion, byte(MessageHello)
	data[14], data[15], data[16], data[17] = byte(length>>24), byte(length>>16), byte(length>>8), byte(length)
	return data
}

func FuzzCommandPayloadDecode(f *testing.F) {
	seed, _ := EncodePayload(CommandPayload{CommandID: "op-1", ResourceID: "catalog", Action: "restart"})
	f.Add(seed)
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		var command CommandPayload
		if DecodePayload(data, &command) != nil || command.Validate() != nil {
			return
		}
		encoded, err := EncodePayload(command)
		if err != nil {
			t.Fatal(err)
		}
		var again CommandPayload
		if DecodePayload(encoded, &again) != nil || again != command {
			t.Fatal("valid command did not round-trip")
		}
	})
}
