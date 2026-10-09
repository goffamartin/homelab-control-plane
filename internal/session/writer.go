package session

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"net"
	"time"
)

// Writer serializes every outbound frame on one connection.
type Writer struct {
	connection net.Conn
	gate       chan struct{}
	slots      chan struct{}
	nextID     uint64
}

func NewWriter(connection net.Conn, nextID uint64) *Writer {
	return &Writer{connection: connection, nextID: nextID, gate: make(chan struct{}, 1), slots: make(chan struct{}, 16)}
}
func (writer *Writer) Send(messageType protocol.MessageType, payload any) error {
	return writer.SendContext(context.Background(), messageType, payload)
}

var ErrWriteQueueFull = errors.New("hlcp session: write queue is full")

func (writer *Writer) Close() error { return writer.connection.Close() }

// SendContext bounds queue admission and the entire socket write. A failed
// write closes the stream because a partial frame cannot safely be retried there.
func (writer *Writer) SendContext(parent context.Context, messageType protocol.MessageType, payload any) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case writer.slots <- struct{}{}:
	default:
		return ErrWriteQueueFull
	}
	defer func() { <-writer.slots }()
	select {
	case writer.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-writer.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := protocol.EncodePayload(payload)
	if err != nil {
		return err
	}
	frame, err := protocol.NewFrame(messageType, writer.nextID, encoded)
	if err != nil {
		return err
	}
	writer.nextID++
	deadline, _ := ctx.Deadline()
	if err := writer.connection.SetWriteDeadline(deadline); err != nil {
		_ = writer.Close()
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = writer.Close() })
	err = protocol.WriteFrame(writer.connection, frame)
	stop()
	if err != nil {
		_ = writer.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if err := writer.connection.SetWriteDeadline(time.Time{}); err != nil {
		_ = writer.Close()
		return err
	}
	return nil
}
