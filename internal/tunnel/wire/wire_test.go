package wire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := AuthRequest{V: Version, TunnelID: "k3x9qzpw04ab", ConnectSecret: "s3cr3t"}
	if err := WriteFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out AuthRequest
	if err := ReadFrame(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

func TestReadFrameStopsAtNewline(t *testing.T) {
	// Bytes after the frame belong to the yamux session and must not be
	// consumed.
	r := strings.NewReader(`{"ok":true,"host":"a.t.snake.blue"}` + "\nYAMUX")
	var resp AuthResponse
	if err := ReadFrame(r, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Host != "a.t.snake.blue" {
		t.Errorf("unexpected frame: %+v", resp)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "YAMUX" {
		t.Errorf("over-read past newline: remaining %q", rest)
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	r := strings.NewReader(strings.Repeat("x", MaxFrame+2) + "\n")
	var resp AuthResponse
	if err := ReadFrame(r, &resp); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameEOF(t *testing.T) {
	var resp AuthResponse
	if err := ReadFrame(strings.NewReader(`{"ok":true}`), &resp); err == nil {
		t.Error("want error for frame without trailing newline at EOF")
	}
}

func TestReadFrameBadJSON(t *testing.T) {
	var resp AuthResponse
	if err := ReadFrame(strings.NewReader("not json\n"), &resp); err == nil {
		t.Error("want error for malformed JSON")
	}
}

func TestWriteFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	big := AuthRequest{ConnectSecret: strings.Repeat("x", MaxFrame)}
	if err := WriteFrame(&buf, big); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("got %v, want ErrFrameTooLarge", err)
	}
}
