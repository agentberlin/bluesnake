// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tunnel

import "errors"

const (
	// DEFAULT_START_PORT is the starting port for port retry logic
	DEFAULT_START_PORT = 38888

	// MAX_PORT_ATTEMPTS is the number of ports to try before giving up
	MAX_PORT_ATTEMPTS = 10
)

// TunnelStatus represents the current status of the tunnel
type TunnelStatus struct {
	IsRunning bool   `json:"isRunning"`
	PublicURL string `json:"publicURL"`
	LocalPort int    `json:"localPort"`
	Error     string `json:"error,omitempty"`
}

// Common errors
var (
	ErrNoAvailablePorts  = errors.New("no available ports found after multiple attempts")
	ErrTunnelNotRunning  = errors.New("tunnel is not running")
	ErrBinaryNotFound    = errors.New("cloudflared binary not found")
	ErrTunnelStartFailed = errors.New("failed to start cloudflared tunnel")
	ErrURLParsingFailed  = errors.New("failed to parse tunnel URL from cloudflared output")
)
