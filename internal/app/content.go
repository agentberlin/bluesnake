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
	"os"
	"path/filepath"
)

// GetPageContent retrieves the saved text content for a specific URL in a crawl
// Content is read from ~/.bluesnake/<domain>/<crawlid>/<sanitized-url>.txt
func (a *App) GetPageContent(crawlID uint, pageURL string) (string, error) {
	// Get crawl information to find the project ID
	crawl, err := a.store.GetCrawlByID(crawlID)
	if err != nil {
		return "", fmt.Errorf("failed to get crawl: %v", err)
	}

	// Get project information to get the domain
	project, err := a.store.GetProjectByID(crawl.ProjectID)
	if err != nil {
		return "", fmt.Errorf("failed to get project: %v", err)
	}

	domain := project.Domain

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Construct file path
	filename := sanitizeURLToFilename(pageURL)
	filePath := filepath.Join(homeDir, ".bluesnake", domain, fmt.Sprintf("%d", crawlID), filename)

	// Read content from file
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("content not found for URL: %s", pageURL)
		}
		return "", fmt.Errorf("failed to read content file: %v", err)
	}

	return string(content), nil
}
