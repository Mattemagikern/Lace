package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/mattemagikern/lace/exec"
	"github.com/mattemagikern/lace/files"
)

var ErrLocalFile = errors.New("local file error")

type MessageType uint16

const (
	TypeIndex   MessageType = 0x01
	TypeWrite   MessageType = 0x02
	TypeRead    MessageType = 0x03
	TypeDelete  MessageType = 0x04
	TypeSync    MessageType = 0x05
	TypeSyncAck MessageType = 0x06
)

const maxPayloadSize = 1 << 30

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
	pipe exec.Pipe
	enc  *zstd.Encoder
	dec  *zstd.Decoder

	sendBuf bytes.Buffer
	readBuf []byte
}

func NewConnection(pipe exec.Pipe) *Connection {
	enc, _ := zstd.NewWriter(pipe,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1))
	dec, _ := zstd.NewReader(pipe, zstd.WithDecoderConcurrency(1))
	return &Connection{
		pipe: pipe,
		enc:  enc,
		dec:  dec,
	}
}

func (c *Connection) Write(p []byte) (int, error) { return c.enc.Write(p) }
func (c *Connection) Read(p []byte) (int, error)  { return c.dec.Read(p) }

func (c *Connection) Flush() error { return c.enc.Flush() }

func (c *Connection) readMessage() (MessageType, []byte, error) {
	var header [6]byte
	if _, err := io.ReadFull(c.dec, header[:]); err != nil {
		return 0, nil, err
	}
	msgType := MessageType(binary.LittleEndian.Uint16(header[:2]))
	length := binary.LittleEndian.Uint32(header[2:6])

	if length == 0 {
		return msgType, nil, nil
	}
	if length > maxPayloadSize {
		return 0, nil, fmt.Errorf("payload too large: %d", length)
	}

	if cap(c.readBuf) < int(length) {
		c.readBuf = make([]byte, length)
	} else {
		c.readBuf = c.readBuf[:length]
	}
	if _, err := io.ReadFull(c.dec, c.readBuf); err != nil {
		return 0, nil, err
	}
	return msgType, c.readBuf, nil
}

func (c *Connection) SendMessageNoFlush(msgType MessageType, payload interface{}) error {
	c.sendBuf.Reset()
	var header [6]byte
	c.sendBuf.Write(header[:])

	if err := msgpack.NewEncoder(&c.sendBuf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	data := c.sendBuf.Bytes()
	binary.LittleEndian.PutUint16(data[0:2], uint16(msgType))
	binary.LittleEndian.PutUint32(data[2:6], uint32(len(data)-6))

	_, err := c.enc.Write(data)
	return err
}

func (c *Connection) SendMessage(msgType MessageType, payload interface{}) error {
	if err := c.SendMessageNoFlush(msgType, payload); err != nil {
		return err
	}
	return c.enc.Flush()
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
		return 0, nil, fmt.Errorf("unknown message type: %d", msgType)
	}
	p := factory()
	if err := msgpack.Unmarshal(raw, p); err != nil {
		return 0, nil, err
	}
	return msgType, p, nil
}

func (c *Connection) SendFile(localPath string, meta files.MetaData, progress io.Writer) error {
	if meta.IsDir || (meta.Mode&os.ModeSymlink) != 0 {
		return c.SendMessageNoFlush(TypeWrite, &WritePayload{MetaData: meta})
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("%w: open %s: %w", ErrLocalFile, localPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat %s: %w", ErrLocalFile, localPath, err)
	}
	meta.Size = info.Size()

	if err := c.SendMessageNoFlush(TypeWrite, &WritePayload{MetaData: meta}); err != nil {
		return err
	}

	dst := io.Writer(c)
	if progress != nil {
		dst = io.MultiWriter(c, progress)
	}
	return files.StreamExact(dst, f, meta.Size)
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

func (c *Connection) Close() error {
	c.enc.Close()
	c.dec.Close()
	return nil
}
