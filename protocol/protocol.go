package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
	"bytes"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/mattemagikern/lace/files"
	"github.com/mattemagikern/lace/exec"
)

type MessageType uint16

const (
	TypeIndex   = 0x01
	TypeWrite   = 0x02
	TypeRead    = 0x03
	TypeDelete  = 0x04
	TypeSync    = 0x05
	TypeSyncAck = 0x06
)

type WritePayload struct {
	MetaData files.MetaData
	Content  []byte
}

type DeletePayload struct {
	Path string
}

type ReadPayload struct {
	Path string
}

type IndexPayload struct {
	Root  string
	Index files.FileIndex
}

type SyncPayload struct {
	Time time.Time
}

type Connection struct {
	r io.Reader
	w io.Writer
	read []byte
	write *bytes.Buffer
	enc *msgpack.Encoder
}

func NewConnection(pipe exec.Pipe) *Connection {
	write := bytes.NewBuffer(make([]byte, 0, 10*1024*1024))
	return &Connection{
		r: pipe,
		w: pipe,
		read: make([]byte, 0, 10*1024*1024),
		write: write,
		enc: msgpack.NewEncoder(write),
	}
}

/*
[0..1]   uint16 message type
[2..5]   uint32 payload length
[6..n]   payload bytes
*/
func (c *Connection) readMessage() (MessageType, []byte, error) {
	header := c.read[:6]
	if _, err := io.ReadFull(c.r, header); err != nil {
		return 0, nil, err
	}

	msgType := MessageType(binary.LittleEndian.Uint16(header[0:2]))
	length := binary.LittleEndian.Uint32(header[2:6])

	if cap(c.read) < int(length) {
		newCap := int(length)
		newBuf := make([]byte, newCap)
		c.read = newBuf
	}

	// Extend buffer length without zeroing the new area
	buf := c.read[:length]

	// Read payload directly into reusable buffer
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return 0, nil, err
	}

	return msgType, buf, nil
}

func (c *Connection) SendMessage(msgType MessageType, payload interface{}) error {
	c.write.Reset()
	c.write.Write([]byte{0,0,0,0,0,0}) // reserve space for header

	if err := c.enc.Encode(payload); err != nil {
		return err
	}

	data := c.write.Bytes()
	binary.LittleEndian.PutUint16(data[0:2], uint16(msgType))
	binary.LittleEndian.PutUint32(data[2:6], uint32(len(data)-6))

	if _, err := c.w.Write(c.write.Bytes()); err != nil {
		return err
	}
	return nil
}
var payloadFactories = map[MessageType]func() any{
	TypeWrite:   func() any { return &WritePayload{} },
	TypeDelete:  func() any { return &DeletePayload{} },
	TypeRead:    func() any { return &ReadPayload{} },
	TypeIndex:   func() any { return &IndexPayload{} },
	TypeSync:    func() any { return &SyncPayload{} },
	TypeSyncAck: func() any { return &SyncPayload{} },
}

func (c *Connection) ReadMessage() (MessageType, interface{}, error) {
	msgType, raw, err := c.readMessage()
	if err != nil {
		return 0, nil, err
	}

	factory, ok := payloadFactories[msgType]
	if !ok {
		return 0, nil, fmt.Errorf("unknown message type: %v", msgType)
	}

	p := factory()
	return msgType, p, msgpack.Unmarshal(raw, p)
}

func (c *Connection) Sync() error {
	err := c.SendMessage(TypeSync, SyncPayload{Time: time.Now()})
	if err != nil {
		return err
	}

	ty, _, err := c.ReadMessage()
	if err != nil {
		return err
	}

	if ty != TypeSyncAck {
		return fmt.Errorf("expected sync ack, got %v", ty)
	}

	return nil
}

func (c *Connection) Close() error {
	return nil
}
