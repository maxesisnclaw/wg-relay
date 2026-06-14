package main

import (
	"bytes"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payloads := [][]byte{
		{0x01},
		bytes.Repeat([]byte{0xAB}, 100),
		bytes.Repeat([]byte{0xCD}, 1400), // typical WG packet
		bytes.Repeat([]byte{0xEF}, 65535), // max size
	}

	for _, payload := range payloads {
		var buf bytes.Buffer
		if err := writeFrame(&buf, payload); err != nil {
			t.Fatalf("writeFrame(%d bytes): %v", len(payload), err)
		}

		got, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("readFrame(%d bytes): %v", len(payload), err)
		}

		if !bytes.Equal(got, payload) {
			t.Fatalf("roundtrip mismatch: wrote %d bytes, got %d bytes", len(payload), len(got))
		}
	}
}

func TestFrameMultipleInStream(t *testing.T) {
	var buf bytes.Buffer
	messages := []string{"hello", "world", "wireguard packet data here"}

	for _, msg := range messages {
		if err := writeFrame(&buf, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}

	for _, want := range messages {
		got, err := readFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}

	// Stream should be exhausted
	_, err := readFrame(&buf)
	if err == nil {
		t.Fatal("expected EOF after all frames consumed")
	}
}

func TestFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	err := writeFrame(&buf, make([]byte, 65536))
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestReadFrameTruncated(t *testing.T) {
	// Header says 100 bytes but only 10 available
	var buf bytes.Buffer
	writeFrame(&buf, bytes.Repeat([]byte{0xFF}, 100))
	truncated := buf.Bytes()[:15] // 2 byte header + 13 bytes of payload (need 100)

	_, err := readFrame(bytes.NewReader(truncated))
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadFrameZeroLength(t *testing.T) {
	// Manually craft a zero-length frame header
	data := []byte{0x00, 0x00}
	_, err := readFrame(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error for zero-length frame")
	}
}
