package session

import (
	"hlcp/internal/protocol"
	"net"
	"sync"
)

// Writer serializes every agent-to-controller frame on one connection.
type Writer struct {
	connection net.Conn
	mutex      sync.Mutex
	nextID     uint64
}

func NewWriter(connection net.Conn, nextID uint64) *Writer {
	return &Writer{connection: connection, nextID: nextID}
}
func (writer *Writer) Send(messageType protocol.MessageType, payload any) error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	encoded, err := protocol.EncodePayload(payload)
	if err != nil {
		return err
	}
	frame, err := protocol.NewFrame(messageType, writer.nextID, encoded)
	if err != nil {
		return err
	}
	writer.nextID++
	return protocol.WriteFrame(writer.connection, frame)
}
