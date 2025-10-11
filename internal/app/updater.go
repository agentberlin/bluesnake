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

package app

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/types"
	"github.com/agentberlin/bluesnake/internal/version"
)

const (
	updateBaseURL = "https://storage.agentberlin.ai/bluesnake"
	versionURL    = updateBaseURL + "/version.txt"
)

// CheckForUpdate checks if a new version is available
func (a *App) CheckForUpdate() (*types.UpdateInfo, error) {
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
	updateAvailable := compareVersions(latestVersion, version.CurrentVersion)

	// Build download URL based on platform
	downloadURL := buildDownloadURL(latestVersion)

	return &types.UpdateInfo{
		CurrentVersion:  version.CurrentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		DownloadURL:     downloadURL,
	}, nil
}

// DownloadAndInstallUpdate downloads and installs the update
func (a *App) DownloadAndInstallUpdate() error {
	// Check if running in development mode
	if isDevMode() {
		return fmt.Errorf("updates are not supported in development mode. Please build and install the app first")
	}

	// First check if update is available
	updateInfo, err := a.CheckForUpdate()
	if err != nil {
		return fmt.Errorf("failed to check for update: %v", err)
	}

	if !updateInfo.UpdateAvailable {
		return fmt.Errorf("no update available")
	}

	fmt.Printf("Downloading update from: %s\n", updateInfo.DownloadURL)

	// Download the installer
	installerPath, err := downloadInstaller(updateInfo.DownloadURL, updateInfo.LatestVersion)
	if err != nil {
		return fmt.Errorf("failed to download update: %v", err)
	}

	fmt.Printf("Downloaded installer to: %s\n", installerPath)

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

// isDevMode detects if the app is running in development mode
func isDevMode() bool {
	execPath, err := os.Executable()
	if err != nil {
		return false
	}

	// In dev mode on macOS, the path typically contains /build/bin/ or similar temp directories
	// In production, it's in /Applications/BlueSnake.app/Contents/MacOS/
	if runtime.GOOS == "darwin" {
		return !strings.Contains(execPath, "/Applications/") && !strings.Contains(execPath, ".app/Contents/MacOS/")
	} else if runtime.GOOS == "windows" {
		// In dev mode on Windows, the path typically contains \build\bin\ or temp directories
		// In production, it's in C:\Program Files\ or similar
		return !strings.Contains(execPath, "Program Files")
	}

	return false
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
	fmt.Printf("Starting macOS update installation from: %s\n", dmgPath)

	// Get the current app bundle path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	fmt.Printf("Executable path: %s\n", execPath)

	// The executable is at BlueSnake.app/Contents/MacOS/BlueSnake
	// We need to get to BlueSnake.app
	appBundlePath := filepath.Clean(filepath.Join(filepath.Dir(execPath), "..", ".."))
	fmt.Printf("App bundle path: %s\n", appBundlePath)

	// Create an update script that will:
	// 1. Wait for the app to quit
	// 2. Mount the DMG
	// 3. Copy the new app to temp location
	// 4. Remove old app and move new one into place
	// 5. Unmount DMG
	// 6. Relaunch app
	scriptPath := filepath.Join(os.TempDir(), "bluesnake-update.sh")
	logPath := filepath.Join(os.TempDir(), "bluesnake-update.log")

	script := fmt.Sprintf(`#!/bin/bash

# Redirect all output to log file (do this BEFORE set -e)
exec > "%s" 2>&1

echo "=== BlueSnake Update Script Started at $(date) ==="
echo "DMG Path: %s"
echo "App Bundle Path: %s"
echo "PID: $$"

# Now enable exit on error after logging is set up
set -e

# Wait for app to quit
echo "Waiting for app to quit..."
sleep 3

# Check if app process is still running
echo "Checking if app is still running..."
if pgrep -f "BlueSnake.app" > /dev/null; then
    echo "WARNING: BlueSnake app is still running, waiting longer..."
    sleep 3
fi

# Mount the DMG
echo "Mounting DMG..."
MOUNT_OUTPUT=$(hdiutil attach "%s" -nobrowse -readonly 2>&1 || echo "MOUNT_FAILED")
echo "Mount output: $MOUNT_OUTPUT"

if echo "$MOUNT_OUTPUT" | grep -q "MOUNT_FAILED"; then
    echo "ERROR: Failed to mount DMG"
    exit 1
fi

# Extract mount point (get the last field from lines containing /Volumes/)
MOUNT_POINT=$(echo "$MOUNT_OUTPUT" | grep "/Volumes/" | tail -1 | awk '{print $NF}')
echo "Mount point: $MOUNT_POINT"

if [ -z "$MOUNT_POINT" ]; then
    echo "ERROR: Failed to extract mount point"
    exit 1
fi

# Verify the new app exists in the DMG
echo "Checking for BlueSnake.app in DMG..."
if [ ! -d "$MOUNT_POINT/BlueSnake.app" ]; then
    echo "ERROR: BlueSnake.app not found in DMG at $MOUNT_POINT"
    echo "Contents of mount point:"
    ls -la "$MOUNT_POINT/"
    hdiutil detach "$MOUNT_POINT" -quiet || true
    exit 1
fi

# Copy new app to temporary location first
TEMP_APP="/tmp/BlueSnake-update-$$.app"
echo "Copying new app to temporary location: $TEMP_APP"
rm -rf "$TEMP_APP"
ditto "$MOUNT_POINT/BlueSnake.app" "$TEMP_APP"

# Verify the copy was successful
if [ ! -d "$TEMP_APP" ]; then
    echo "ERROR: Failed to copy app to temporary location"
    hdiutil detach "$MOUNT_POINT" -quiet || true
    exit 1
fi

echo "Copy successful, size check:"
du -sh "$TEMP_APP"

echo "Now replacing old app..."

# Store the old app path for verification
OLD_APP_PATH="%s"
echo "Old app path: $OLD_APP_PATH"

# Remove old app
echo "Removing old app..."
if [ -d "$OLD_APP_PATH" ]; then
    rm -rf "$OLD_APP_PATH"
    echo "Old app removed"
else
    echo "WARNING: Old app not found at $OLD_APP_PATH"
fi

# Move new app into place
echo "Moving new app into place..."
mv "$TEMP_APP" "$OLD_APP_PATH"

# Verify the app is in place
if [ ! -d "$OLD_APP_PATH" ]; then
    echo "ERROR: Failed to move app into place at $OLD_APP_PATH"
    hdiutil detach "$MOUNT_POINT" -quiet || true
    exit 1
fi

echo "New app is now at: $OLD_APP_PATH"
ls -la "$OLD_APP_PATH"

# Unmount DMG
echo "Unmounting DMG..."
hdiutil detach "$MOUNT_POINT" -quiet

# Clean up DMG file
echo "Cleaning up DMG at: %s"
rm -f "%s"

# Relaunch app
echo "Relaunching app..."
open "$OLD_APP_PATH"

echo "=== Update completed successfully at $(date) ==="

# Clean up script (self-delete)
sleep 1
rm -f "$0"
`, logPath, dmgPath, appBundlePath, dmgPath, appBundlePath, dmgPath, dmgPath)

	err = os.WriteFile(scriptPath, []byte(script), 0755)
	if err != nil {
		return fmt.Errorf("failed to create update script: %v", err)
	}

	fmt.Printf("Update script created at: %s\n", scriptPath)
	fmt.Printf("Update log will be written to: %s\n", logPath)
	fmt.Println("Launching update script...")

	// Launch the script in background and detach from current process
	cmd := exec.Command("sh", scriptPath)
	cmd.Dir = os.TempDir()
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start update script: %v", err)
	}

	// Detach the process
	cmd.Process.Release()

	fmt.Println("Update script started successfully.")
	fmt.Printf("If the update fails, check the log at: %s\n", logPath)
	fmt.Println("Exiting app in 1 second...")

	// Give the script a moment to start
	time.Sleep(time.Second)

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

// GetVersion returns the current version of the application
func (a *App) GetVersion() string {
	return version.CurrentVersion
}
