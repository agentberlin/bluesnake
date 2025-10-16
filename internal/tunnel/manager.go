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

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// TunnelManager manages the cloudflared tunnel lifecycle
type TunnelManager struct {
	cmd       *exec.Cmd
	publicURL string
	localPort int
	isRunning bool
	mu        sync.RWMutex
}

// NewTunnelManager creates a new TunnelManager instance
func NewTunnelManager() *TunnelManager {
	return &TunnelManager{}
}

// Start starts a cloudflared tunnel pointing to the specified local port
// It waits for the tunnel URL to be ready and returns it
func (tm *TunnelManager) Start(localPort int) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.isRunning {
		return "", fmt.Errorf("tunnel is already running")
	}

	// Get cloudflared binary path
	binaryPath, err := GetCloudflaredBinary()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBinaryNotFound, err)
	}

	// Prepare command
	localURL := fmt.Sprintf("http://localhost:%d", localPort)
	tm.cmd = exec.Command(binaryPath, "tunnel", "--url", localURL)

	// Get both stdout and stderr pipes
	stdout, err := tm.cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := tm.cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Start the command
	if err := tm.cmd.Start(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTunnelStartFailed, err)
	}

	fmt.Printf("Started cloudflared process (PID: %d) for port %d\n", tm.cmd.Process.Pid, localPort)

	// Parse stdout and stderr for the tunnel URL
	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Regex to match trycloudflare.com URLs
	urlRegex := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

	// Track if we've sent the URL
	urlSent := make(chan struct{})

	// Scan stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if matches := urlRegex.FindString(line); matches != "" {
				select {
				case urlChan <- matches:
					close(urlSent)
				case <-urlSent:
					// Already sent
				}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Printf("Cloudflared error reading output: %v\n", err)
		}
	}()

	// Scan stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if matches := urlRegex.FindString(line); matches != "" {
				select {
				case urlChan <- matches:
					close(urlSent)
				case <-urlSent:
					// Already sent
				}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Printf("Cloudflared error reading error stream: %v\n", err)
		}
	}()

	// Wait for URL or timeout
	select {
	case url := <-urlChan:
		tm.publicURL = url
		tm.localPort = localPort
		tm.isRunning = true
		return url, nil

	case err := <-errChan:
		// Kill the process if URL parsing failed
		if tm.cmd.Process != nil {
			tm.cmd.Process.Kill()
		}
		return "", err

	case <-time.After(60 * time.Second):
		// Timeout waiting for URL
		fmt.Printf("Timeout waiting for tunnel URL after 60 seconds\n")
		if tm.cmd.Process != nil {
			tm.cmd.Process.Kill()
		}
		return "", fmt.Errorf("timeout waiting for tunnel URL (60s)")
	}
}

// Stop stops the cloudflared tunnel
func (tm *TunnelManager) Stop() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.isRunning {
		return ErrTunnelNotRunning
	}

	if tm.cmd != nil && tm.cmd.Process != nil {
		if err := tm.cmd.Process.Kill(); err != nil {
			fmt.Printf("Failed to stop Cloudflare: %v\n", err)
			return fmt.Errorf("failed to stop Cloudflare: %w", err)
		}

		// Wait for process to exit
		tm.cmd.Wait()
		fmt.Printf("Cloudflare stopped successfully\n")
	}

	tm.isRunning = false
	tm.publicURL = ""
	tm.localPort = 0
	tm.cmd = nil

	return nil
}

// GetStatus returns the current tunnel status
func (tm *TunnelManager) GetStatus() TunnelStatus {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	return TunnelStatus{
		IsRunning: tm.isRunning,
		PublicURL: tm.publicURL,
		LocalPort: tm.localPort,
	}
}

// IsRunning returns whether the tunnel is currently running
func (tm *TunnelManager) IsRunning() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.isRunning
}
