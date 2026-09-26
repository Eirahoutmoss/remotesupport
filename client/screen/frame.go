package screen

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	magic          = "RSF1"
	version        = 1
	headerSize     = 27
	MaxPayloadSize = 2 << 20 // 2 MiB
	MaxWidth       = 16384
	MaxHeight      = 16384
)

var (
	ErrInvalidFrame = errors.New("screen: invalid frame")
	ErrPayloadSize  = errors.New("screen: payload too large")
)

type Frame struct {
	Monitor uint16
	Seq     uint64
	Width   uint32
	Height  uint32
	JPEG    []byte
}

func (f Frame) Validate() error {
	if f.Width == 0 || f.Width > MaxWidth || f.Height == 0 || f.Height > MaxHeight {
		return fmt.Errorf("%w: invalid dimensions %dx%d", ErrInvalidFrame, f.Width, f.Height)
	}
	if len(f.JPEG) == 0 {
		return fmt.Errorf("%w: empty JPEG payload", ErrInvalidFrame)
	}
	if len(f.JPEG) > MaxPayloadSize {
		return ErrPayloadSize
	}
	if len(f.JPEG) < 4 || f.JPEG[0] != 0xff || f.JPEG[1] != 0xd8 ||
		f.JPEG[len(f.JPEG)-2] != 0xff || f.JPEG[len(f.JPEG)-1] != 0xd9 {
		return fmt.Errorf("%w: invalid JPEG markers", ErrInvalidFrame)
	}
	return nil
}

func EncodeFrame(f Frame) ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	out := make([]byte, headerSize+len(f.JPEG))
	copy(out[:4], magic)
	out[4] = version
	binary.BigEndian.PutUint16(out[5:7], f.Monitor)
	binary.BigEndian.PutUint64(out[7:15], f.Seq)
	binary.BigEndian.PutUint32(out[15:19], f.Width)
	binary.BigEndian.PutUint32(out[19:23], f.Height)
	binary.BigEndian.PutUint32(out[23:27], uint32(len(f.JPEG)))
	copy(out[headerSize:], f.JPEG)
	return out, nil
}

func DecodeFrame(data []byte) (Frame, error) {
	if len(data) < headerSize {
		return Frame{}, fmt.Errorf("%w: truncated header", ErrInvalidFrame)
	}
	if string(data[:4]) != magic || data[4] != version {
		return Frame{}, fmt.Errorf("%w: bad magic/version", ErrInvalidFrame)
	}

	payloadLen := uint64(binary.BigEndian.Uint32(data[23:27]))
	if payloadLen > MaxPayloadSize {
		return Frame{}, ErrPayloadSize
	}
	if payloadLen != uint64(len(data)-headerSize) {
		return Frame{}, fmt.Errorf("%w: payload length mismatch", ErrInvalidFrame)
	}

	f := Frame{
		Monitor: binary.BigEndian.Uint16(data[5:7]),
		Seq:     binary.BigEndian.Uint64(data[7:15]),
		Width:   binary.BigEndian.Uint32(data[15:19]),
		Height:  binary.BigEndian.Uint32(data[19:23]),
		JPEG:    append([]byte(nil), data[headerSize:]...),
	}
	if err := f.Validate(); err != nil {
		return Frame{}, err
	}
	return f, nil
}
