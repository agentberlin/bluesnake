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
	"embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

//go:embed binaries/*
var embeddedBinaries embed.FS

const (
	cloudflaredDownloadURLDarwin  = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-amd64.tgz"
	cloudflaredDownloadURLWindows = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
	cloudflaredDownloadURLLinux   = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"
)

// GetCloudflaredBinary returns the path to the cloudflared binary
// It checks in the following order:
// 1. System PATH
// 2. Embedded binary (production builds)
// 3. Download to cache directory (dev mode)
func GetCloudflaredBinary() (string, error) {
	// 1. Check if cloudflared is in system PATH
	if path, err := exec.LookPath("cloudflared"); err == nil {
		return path, nil
	}

	// 2. Try to extract embedded binary (production builds)
	if path, err := extractEmbeddedBinary(); err == nil {
		return path, nil
	}

	// 3. Download to cache directory (dev mode)
	return downloadBinary()
}

// extractEmbeddedBinary extracts the embedded cloudflared binary to a temp location
func extractEmbeddedBinary() (string, error) {
	binaryName := getEmbeddedBinaryName()
	embeddedPath := filepath.Join("binaries", runtime.GOOS, binaryName)

	// Try to read embedded file
	data, err := embeddedBinaries.ReadFile(embeddedPath)
	if err != nil {
		return "", fmt.Errorf("embedded binary not found: %w", err)
	}

	// Get app support directory
	cacheDir, err := getCacheDir()
	if err != nil {
		return "", err
	}

	// Write binary to cache directory
	binaryPath := filepath.Join(cacheDir, binaryName)
	if err := os.WriteFile(binaryPath, data, 0755); err != nil {
		return "", fmt.Errorf("failed to write binary: %w", err)
	}

	return binaryPath, nil
}

// downloadBinary downloads cloudflared to the cache directory
func downloadBinary() (string, error) {
	downloadURL := getDownloadURL()
	if downloadURL == "" {
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	// Get cache directory
	cacheDir, err := getCacheDir()
	if err != nil {
		return "", err
	}

	binaryName := getBinaryName()
	binaryPath := filepath.Join(cacheDir, binaryName)

	// Check if already downloaded
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	// Download binary
	fmt.Printf("Downloading cloudflared from %s...\n", downloadURL)
	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to download cloudflared: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download cloudflared: HTTP %d", resp.StatusCode)
	}

	// Create binary file
	out, err := os.OpenFile(binaryPath, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create binary file: %w", err)
	}
	defer out.Close()

	// Write downloaded content
	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("failed to write binary: %w", err)
	}

	fmt.Printf("Downloaded cloudflared to %s\n", binaryPath)
	return binaryPath, nil
}

// getCacheDir returns the cache directory for bluesnake
func getCacheDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	cacheDir := filepath.Join(homeDir, ".bluesnake", "bin")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	return cacheDir, nil
}

// getBinaryName returns the platform-specific binary name
func getBinaryName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

// getEmbeddedBinaryName returns the name of the embedded binary for the current platform
func getEmbeddedBinaryName() string {
	return getBinaryName()
}

// getDownloadURL returns the download URL for the current platform
func getDownloadURL() string {
	switch runtime.GOOS {
	case "darwin":
		return cloudflaredDownloadURLDarwin
	case "windows":
		return cloudflaredDownloadURLWindows
	case "linux":
		return cloudflaredDownloadURLLinux
	default:
		return ""
	}
}
