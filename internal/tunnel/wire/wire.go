// Package wire is the bluesnake reverse-tunnel wire protocol, shared by the
// embedded client (internal/tunnel) and the tunnel server (tunnelserver).
//
// A tunnel connection is a TLS stream negotiated with the ALPN protocol
// below. The client sends exactly one newline-delimited JSON AuthRequest,
// the server answers with one AuthResponse, and on success the same
// connection becomes a yamux session: the server opens one stream per
// inbound public HTTP request, the client accepts streams and serves them.
//
// This package must stay dependency-free and wire-compatible across
// versions: deployed servers and shipped clients upgrade independently. If
// the server code moves to its own repository this package is copied there
// verbatim.
package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	// ALPN is the TLS ALPN protocol name tunnel clients negotiate. The
	// server shares port 443 between public HTTPS and tunnel connects by
	// routing on the negotiated protocol.
	ALPN = "bluesnake-tunnel/1"

	// Version is the auth-frame protocol version this package speaks.
	Version = 1

	// MaxFrame caps an auth frame: nothing legitimate comes close, and the
	// cap bounds what an unauthenticated peer can make the server buffer.
	MaxFrame = 4 << 10
)

// AuthRequest is the single control frame a client sends after the TLS
// handshake. ConnectSecret is the tunnel credential — distinct from the URL
// access token, which never travels on this channel.
type AuthRequest struct {
	V             int    `json:"v"`
	TunnelID      string `json:"tunnel_id"`
	ConnectSecret string `json:"connect_secret"`
}

// AuthResponse is the server's reply. On OK, Host is the public hostname
// bound to this session (e.g. "k3x9qzpw04ab.t.snake.blue") and the yamux
// session begins immediately after the newline.
type AuthResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Host  string `json:"host,omitempty"`
}

// ErrFrameTooLarge is returned by ReadFrame when a peer exceeds MaxFrame.
var ErrFrameTooLarge = errors.New("tunnel wire: frame exceeds size limit")

// WriteFrame writes v as one newline-delimited JSON frame.
func WriteFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > MaxFrame {
		return ErrFrameTooLarge
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// ReadFrame reads one newline-delimited JSON frame into v. It reads byte by
// byte so it never consumes past the newline — the same connection carries
// the yamux session next, and an over-read would corrupt it.
func ReadFrame(r io.Reader, v any) error {
	buf := make([]byte, 0, 256)
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			buf = append(buf, b[0])
			if len(buf) > MaxFrame {
				return ErrFrameTooLarge
			}
		}
		if err != nil {
			return fmt.Errorf("tunnel wire: reading frame: %w", err)
		}
	}
	return json.Unmarshal(buf, v)
}
