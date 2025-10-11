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
	"fmt"
	"os"
	"strings"
)

// GetFaviconData reads a favicon file and returns it as a base64 data URL
func (a *App) GetFaviconData(faviconPath string) (string, error) {
	if faviconPath == "" {
		return "", fmt.Errorf("empty favicon path")
	}

	// Read the file
	data, err := os.ReadFile(faviconPath)
	if err != nil {
		return "", fmt.Errorf("failed to read favicon: %v", err)
	}

	// Convert to base64 data URL
	// Determine content type based on file extension
	contentType := "image/png"
	if strings.HasSuffix(strings.ToLower(faviconPath), ".jpg") || strings.HasSuffix(strings.ToLower(faviconPath), ".jpeg") {
		contentType = "image/jpeg"
	} else if strings.HasSuffix(strings.ToLower(faviconPath), ".ico") {
		contentType = "image/x-icon"
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", contentType, base64Data), nil
}
