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

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	updateBaseURL = "https://storage.agentberlin.ai/bluesnake"
	versionURL    = updateBaseURL + "/version.txt"
)

// UpdateInfo contains information about available updates
type UpdateInfo struct {
	CurrentVersion   string `json:"currentVersion"`
	LatestVersion    string `json:"latestVersion"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	DownloadURL      string `json:"downloadUrl"`
}

// CheckForUpdate checks if a new version is available
func (a *App) CheckForUpdate() (*UpdateInfo, error) {
	// Fetch the latest version from the server
	resp, err := http.Get(versionURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch version info: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch version info: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read version info: %v", err)
	}

	latestVersion := strings.TrimSpace(string(body))

	// Compare versions
	updateAvailable := compareVersions(latestVersion, CurrentVersion)

	// Build download URL based on platform
	downloadURL := buildDownloadURL(latestVersion)

	return &UpdateInfo{
		CurrentVersion:  CurrentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		DownloadURL:     downloadURL,
	}, nil
}

// compareVersions returns true if latest > current
// Expects versions in format: v0.0.1
func compareVersions(latest, current string) bool {
	// Remove 'v' prefix if present
	latest = strings.TrimPrefix(latest, "v")
	current = strings.TrimPrefix(current, "v")

	latestParts := strings.Split(latest, ".")
	currentParts := strings.Split(current, ".")

	// Ensure we have 3 parts for comparison
	for len(latestParts) < 3 {
		latestParts = append(latestParts, "0")
	}
	for len(currentParts) < 3 {
		currentParts = append(currentParts, "0")
	}

	// Compare each part
	for i := 0; i < 3; i++ {
		var latestNum, currentNum int
		fmt.Sscanf(latestParts[i], "%d", &latestNum)
		fmt.Sscanf(currentParts[i], "%d", &currentNum)

		if latestNum > currentNum {
			return true
		} else if latestNum < currentNum {
			return false
		}
	}

	return false
}

// buildDownloadURL constructs the download URL based on platform and version
func buildDownloadURL(version string) string {
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("%s/BlueSnake-macOS-Universal-%s.dmg", updateBaseURL, version)
	case "windows":
		return fmt.Sprintf("%s/BlueSnake-Windows-x64-%s.exe", updateBaseURL, version)
	default:
		return ""
	}
}

// DownloadAndInstallUpdate downloads and installs the update
func (a *App) DownloadAndInstallUpdate() error {
	// First check if update is available
	updateInfo, err := a.CheckForUpdate()
	if err != nil {
		return err
	}

	if !updateInfo.UpdateAvailable {
		return fmt.Errorf("no update available")
	}

	// Download the installer
	installerPath, err := downloadInstaller(updateInfo.DownloadURL, updateInfo.LatestVersion)
	if err != nil {
		return fmt.Errorf("failed to download update: %v", err)
	}

	// Install based on platform
	switch runtime.GOOS {
	case "darwin":
		return installMacOSUpdate(installerPath)
	case "windows":
		return installWindowsUpdate(installerPath)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// downloadInstaller downloads the installer to a temporary location
func downloadInstaller(url, version string) (string, error) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "bluesnake-update-*")
	if err != nil {
		return "", err
	}

	// Determine file extension
	ext := ".dmg"
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	installerPath := filepath.Join(tempDir, fmt.Sprintf("BlueSnake-%s%s", version, ext))

	// Download the file
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	// Create the file
	out, err := os.Create(installerPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// Copy the response body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", err
	}

	return installerPath, nil
}

// installMacOSUpdate installs the update on macOS
func installMacOSUpdate(dmgPath string) error {
	// Mount the DMG
	mountCmd := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse", "-quiet")
	output, err := mountCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to mount DMG: %v, output: %s", err, string(output))
	}

	// Parse the mount point from output
	// The output format is: /dev/diskXsY    Apple_HFS    /Volumes/BlueSnake
	lines := strings.Split(string(output), "\n")
	var mountPoint string
	for _, line := range lines {
		if strings.Contains(line, "/Volumes/") {
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.HasPrefix(part, "/Volumes/") {
					mountPoint = part
					break
				}
			}
		}
	}

	if mountPoint == "" {
		return fmt.Errorf("could not determine mount point")
	}

	// Get the current app bundle path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	// The executable is at BlueSnake.app/Contents/MacOS/BlueSnake
	// We need to get to BlueSnake.app
	appBundlePath := filepath.Clean(filepath.Join(filepath.Dir(execPath), "..", ".."))

	// Source app in DMG
	sourceApp := filepath.Join(mountPoint, "BlueSnake.app")

	// Create an update script that will:
	// 1. Wait for the app to quit
	// 2. Remove old app
	// 3. Copy new app
	// 4. Unmount DMG
	// 5. Relaunch app
	scriptPath := filepath.Join(os.TempDir(), "bluesnake-update.sh")
	script := fmt.Sprintf(`#!/bin/bash
# Wait for app to quit
sleep 2

# Remove old app
rm -rf "%s"

# Copy new app
cp -R "%s" "%s"

# Unmount DMG
hdiutil detach "%s" -quiet

# Relaunch app
open "%s"

# Clean up
rm -f "%s"
rm -f "$0"
`, appBundlePath, sourceApp, appBundlePath, mountPoint, appBundlePath, dmgPath)

	err = os.WriteFile(scriptPath, []byte(script), 0755)
	if err != nil {
		return fmt.Errorf("failed to create update script: %v", err)
	}

	// Launch the script in background
	cmd := exec.Command("sh", scriptPath)
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start update script: %v", err)
	}

	// Exit the application (the script will handle the rest)
	os.Exit(0)
	return nil
}

// installWindowsUpdate installs the update on Windows
func installWindowsUpdate(exePath string) error {
	// On Windows, we'll launch the installer and exit
	// The installer should handle replacing the running executable

	// Launch the installer
	cmd := exec.Command(exePath, "/SILENT")
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to launch installer: %v", err)
	}

	// Exit the application (the installer will handle the rest)
	os.Exit(0)
	return nil
}
