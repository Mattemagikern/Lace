package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/files"
)

type MessageType uint16

const (
	TypeIndex   MessageType = 0x01
	TypeWrite   MessageType = 0x02
	TypeRead    MessageType = 0x03
	TypeDelete  MessageType = 0x04
	TypeSync    MessageType = 0x05
	TypeSyncAck MessageType = 0x06
)

type WritePayload struct {
	MetaData files.MetaData
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
}

func NewConnection(pipe exec.Pipe) *Connection {
	return &Connection{r: pipe, w: pipe}
}

func (c *Connection) Write(w []byte) (int, error) { return c.w.Write(w) }
func (c *Connection) Read(b []byte) (int, error)  { return c.r.Read(b) }

func (c *Connection) readMessage() (MessageType, []byte, error) {
	var header [6]byte
	if _, err := io.ReadFull(c.r, header[:]); err != nil {
		return 0, nil, err
	}
	msgType := MessageType(binary.LittleEndian.Uint16(header[:2]))
	length := binary.LittleEndian.Uint32(header[2:6])

	if length == 0 {
		return msgType, nil, nil
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return 0, nil, err
	}

	return msgType, payload, nil
}

func (c *Connection) SendMessage(msgType MessageType, payload interface{}) error {
	b := &bytes.Buffer{}

	// Reserve header
	b.Write(make([]byte, 6))

	var err error

	switch msgType {
	case TypeIndex:
		// msgpack encode first
		var tmp bytes.Buffer
		enc := msgpack.NewEncoder(&tmp)
		if err := enc.Encode(payload); err != nil {
			return fmt.Errorf("encode index payload: %w", err)
		}

		// Compress
		zw, err := zstd.NewWriter(b, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
		if err != nil {
			return fmt.Errorf("zstd writer: %w", err)
		}

		if _, err := io.Copy(zw, &tmp); err != nil {
			zw.Close()
			return fmt.Errorf("zstd compress: %w", err)
		}

		if err := zw.Close(); err != nil {
			return fmt.Errorf("zstd close: %w", err)
		}

	default:
		enc := msgpack.NewEncoder(b)
		if err := enc.Encode(payload); err != nil {
			return fmt.Errorf("encode payload: %w", err)
		}
	}

	// Now rewrite header
	data := b.Bytes()
	payloadLen := uint32(len(data) - 6)

	binary.LittleEndian.PutUint16(data[0:2], uint16(msgType))
	binary.LittleEndian.PutUint32(data[2:6], payloadLen)

	_, err = c.Write(data)
	return err
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

	factory := payloadFactories[msgType]
	p := factory()

	switch msgType {
	case TypeIndex:
		rd, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return 0, nil, fmt.Errorf("zstd decoder: %w", err)
		}
		defer rd.Close()

		decoded, err := io.ReadAll(rd)
		if err != nil {
			return 0, nil, fmt.Errorf("zstd read: %w", err)
		}

		return msgType, p, msgpack.Unmarshal(decoded, p)

	default:
		return msgType, p, msgpack.Unmarshal(raw, p)
	}
}

func (c *Connection) Sync() error {
	if err := c.SendMessage(TypeSync, SyncPayload{Time: time.Now()}); err != nil {
		return err
	}

	ty, _, err := c.ReadMessage()
	if err != nil {
		return err
	}

	if ty != TypeSyncAck {
		return fmt.Errorf("expected SYNC ACK, got %v", ty)
	}

	return nil
}

func (c *Connection) Close() error { return nil }
