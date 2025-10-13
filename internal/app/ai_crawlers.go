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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/agentberlin/bluesnake/internal/types"
)

// GetAICrawlerData retrieves AI crawler data for a project
func (a *App) GetAICrawlerData(projectURL string) (*types.AICrawlerResponse, error) {
	// Get or create project
	project, err := a.store.GetProjectByDomain(extractDomain(projectURL))
	if err != nil {
		return nil, fmt.Errorf("project not found: %w", err)
	}

	// If no data, return nil (indicating no checks have been run)
	if project.AICrawlerData == "" {
		return nil, nil
	}

	// Parse the JSON data
	var data types.AICrawlerData
	if err := json.Unmarshal([]byte(project.AICrawlerData), &data); err != nil {
		return nil, fmt.Errorf("failed to parse AI crawler data: %w", err)
	}

	response := &types.AICrawlerResponse{
		Data: &data,
	}

	// Load screenshots as base64
	if project.SSRScreenshot != "" {
		if screenshotData, err := loadScreenshotAsBase64(project.SSRScreenshot); err == nil {
			response.SSRScreenshot = screenshotData
		}
	}

	if project.JSScreenshot != "" {
		if screenshotData, err := loadScreenshotAsBase64(project.JSScreenshot); err == nil {
			response.JSScreenshot = screenshotData
		}
	}

	if project.NoJSScreenshot != "" {
		if screenshotData, err := loadScreenshotAsBase64(project.NoJSScreenshot); err == nil {
			response.NoJSScreenshot = screenshotData
		}
	}

	return response, nil
}

// SaveAICrawlerData saves AI crawler data for a project
func (a *App) SaveAICrawlerData(projectURL string, data *types.AICrawlerData, ssrScreenshot, jsScreenshot, noJSScreenshot string) error {
	// Get or create project
	project, err := a.store.GetProjectByDomain(extractDomain(projectURL))
	if err != nil {
		return fmt.Errorf("project not found: %w", err)
	}

	// Set timestamp
	data.CheckedAt = time.Now().Unix()

	// Serialize data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to serialize AI crawler data: %w", err)
	}

	// Save screenshots to disk
	screenshotsDir := getScreenshotsDir(project.ID)
	if err := os.MkdirAll(screenshotsDir, 0755); err != nil {
		return fmt.Errorf("failed to create screenshots directory: %w", err)
	}

	var ssrPath, jsPath, noJSPath string

	if ssrScreenshot != "" {
		ssrPath = filepath.Join(screenshotsDir, "ssr_screenshot.png")
		if err := saveBase64Screenshot(ssrScreenshot, ssrPath); err != nil {
			return fmt.Errorf("failed to save SSR screenshot: %w", err)
		}
	}

	if jsScreenshot != "" {
		jsPath = filepath.Join(screenshotsDir, "js_screenshot.png")
		if err := saveBase64Screenshot(jsScreenshot, jsPath); err != nil {
			return fmt.Errorf("failed to save JS screenshot: %w", err)
		}
	}

	if noJSScreenshot != "" {
		noJSPath = filepath.Join(screenshotsDir, "nojs_screenshot.png")
		if err := saveBase64Screenshot(noJSScreenshot, noJSPath); err != nil {
			return fmt.Errorf("failed to save NoJS screenshot: %w", err)
		}
	}

	// Update project
	updates := map[string]interface{}{
		"ai_crawler_data":  string(jsonData),
		"ssr_screenshot":   ssrPath,
		"js_screenshot":    jsPath,
		"no_js_screenshot": noJSPath,
	}

	if err := a.store.UpdateProject(project.ID, updates); err != nil {
		return fmt.Errorf("failed to update project: %w", err)
	}

	return nil
}

// Helper functions

func extractDomain(rawURL string) string {
	// Try to parse as URL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	// If not a valid URL, return as-is
	return rawURL
}

func getScreenshotsDir(projectID uint) string {
	// Get user's home directory or use a default
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".bluesnake", "screenshots", fmt.Sprintf("project_%d", projectID))
}

func saveBase64Screenshot(base64Data, filePath string) error {
	// Decode base64 data
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func loadScreenshotAsBase64(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return base64.StdEncoding.EncodeToString(data), nil
}
