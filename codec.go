package prpc

import (
	"bufio"
	"fmt"
	"io"
	"sync"

	"encoding/binary"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/puper/prpc/pb"
)

var emptyPb = &empty.Empty{}

const defaultBufferSize = 4 * 1024

type Codec interface {
	Write(*Header, interface{}) error
	ReadHeader(*Header) error
	ReadBody(interface{}) error

	Close() error
}

type protobufCodec struct {
	mu     sync.Mutex
	header pb.Header
	enc    *Encoder
	w      *bufio.Writer
	dec    *Decoder
	c      io.Closer
}

func NewProtobufCodec(rwc io.ReadWriteCloser) Codec {
	w := bufio.NewWriterSize(rwc, defaultBufferSize)
	r := bufio.NewReaderSize(rwc, defaultBufferSize)
	return &protobufCodec{
		enc: NewEncoder(w),
		w:   w,
		dec: NewDecoder(r),
		c:   rwc,
	}
}

func (c *protobufCodec) Write(header *Header, body interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.header.IsResp = header.IsResp
	c.header.Method = header.ServiceMethod
	c.header.Seq = header.Seq
	c.header.Error = header.Error
	err := encode(c.enc, &c.header)
	if err != nil {
		return err
	}
	if err = encode(c.enc, body); err != nil {
		return err
	}
	err = c.w.Flush()
	return err
}

func (c *protobufCodec) ReadHeader(header *Header) error {
	c.header.Reset()
	if err := c.dec.Decode(&c.header); err != nil {
		return err
	}
	header.IsResp = c.header.IsResp
	header.ServiceMethod = c.header.Method
	header.Seq = c.header.Seq
	header.Error = c.header.Error
	return nil
}

func (c *protobufCodec) ReadBody(body interface{}) (err error) {
	if pb, ok := body.(proto.Message); ok {
		return c.dec.Decode(pb)
	} else if body == nil {
		return c.dec.Decode(emptyPb)
	}
	return fmt.Errorf("%T does not implement proto.Message", body)
}

func (c *protobufCodec) Close() error {
	return c.c.Close()
}

func encode(enc *Encoder, m interface{}) (err error) {
	if pb, ok := m.(proto.Message); ok {
		return enc.Encode(pb)
	} else if m == nil {
		return enc.Encode(emptyPb)
	}
	return fmt.Errorf("%T does not implement proto.Message", m)
}

const bootstrapLen = 128 // memory to hold first slice; helps small buffers avoid allocation

type DecodeReader interface {
	io.ByteReader
	io.Reader
}

// A Decoder manages the receipt of type and data information read from the
// remote side of a connection.
type Decoder struct {
	r   DecodeReader
	buf []byte
}

// NewDecoder returns a new decoder that reads from the io.Reader.
func NewDecoder(r DecodeReader) *Decoder {
	return &Decoder{
		buf: make([]byte, 0, bootstrapLen),
		r:   r,
	}
}

func (d *Decoder) Decode(m proto.Message) (err error) {
	if d.buf, err = readFull(d.r, d.buf); err != nil {
		return err
	}
	if m == nil {
		return err
	}
	return proto.Unmarshal(d.buf, m)
}

func readFull(r DecodeReader, buf []byte) ([]byte, error) {
	val, err := binary.ReadUvarint(r)
	if err != nil {
		return buf[:0], err
	}
	size := int(val)

	if cap(buf) < size {
		buf = make([]byte, size)
	}
	buf = buf[:size]

	_, err = io.ReadFull(r, buf)
	return buf, err
}

// An Encoder manages the transmission of type and data information to the
// other side of a connection.
type Encoder struct {
	size [binary.MaxVarintLen64]byte
	buf  *proto.Buffer
	w    io.Writer
}

// NewEncoder returns a new encoder that will transmit on the io.Writer.
func NewEncoder(w io.Writer) *Encoder {
	buf := make([]byte, 0, bootstrapLen)
	return &Encoder{
		buf: proto.NewBuffer(buf),
		w:   w,
	}
}

// Encode transmits the data item represented by the empty interface value,
// guaranteeing that all necessary type information has been transmitted
// first.
func (e *Encoder) Encode(m proto.Message) (err error) {
	if err = e.buf.Marshal(m); err != nil {
		e.buf.Reset()
		return err
	}
	err = e.writeFrame(e.buf.Bytes())
	e.buf.Reset()
	return err
}

func (e *Encoder) writeFrame(data []byte) (err error) {
	n := binary.PutUvarint(e.size[:], uint64(len(data)))
	if _, err = e.w.Write(e.size[:n]); err != nil {
		return err
	}
	_, err = e.w.Write(data)
	return err
}
