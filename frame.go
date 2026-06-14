package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

// TCP framing: each UDP datagram is sent as [2-byte big-endian length][payload].
// Max payload: 65535 bytes (sufficient for any UDP datagram).

func writeFrame(w io.Writer, data []byte) error {
	if len(data) > 65535 {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(data)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint16(header[:])
	if length == 0 {
		return nil, fmt.Errorf("zero-length frame")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
