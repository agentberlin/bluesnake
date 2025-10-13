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

	"github.com/agentberlin/bluesnake/internal/framework"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/types"
)

var _ = store.Store{} // Force import usage

// GetDomainFrameworks returns all frameworks for domains in a project
func (a *App) GetDomainFrameworks(projectID uint) ([]types.DomainFrameworkResponse, error) {
	frameworks, err := a.store.GetAllDomainFrameworks(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get domain frameworks: %v", err)
	}

	var response []types.DomainFrameworkResponse
	for _, fw := range frameworks {
		response = append(response, types.DomainFrameworkResponse{
			Domain:      fw.Domain,
			Framework:   fw.Framework,
			DetectedAt:  fw.CreatedAt,
			ManuallySet: fw.ManuallySet,
		})
	}

	return response, nil
}

// GetDomainFramework returns the framework for a specific domain
func (a *App) GetDomainFramework(projectID uint, domain string) (*types.DomainFrameworkResponse, error) {
	fw, err := a.store.GetDomainFramework(projectID, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get domain framework: %v", err)
	}

	if fw == nil {
		return nil, nil // No framework detected yet
	}

	return &types.DomainFrameworkResponse{
		Domain:      fw.Domain,
		Framework:   fw.Framework,
		DetectedAt:  fw.CreatedAt,
		ManuallySet: fw.ManuallySet,
	}, nil
}

// SetDomainFramework sets the framework for a specific domain (user override)
func (a *App) SetDomainFramework(projectID uint, domain string, frameworkID string) error {
	// Validate framework ID - allow "other" as a special case
	validFramework := frameworkID == "other"
	if !validFramework {
		for _, fw := range framework.GetAllFrameworks() {
			if fw.ID == frameworkID {
				validFramework = true
				break
			}
		}
	}

	if !validFramework {
		return fmt.Errorf("invalid framework ID: %s", frameworkID)
	}

	// Save with manuallySet = true
	err := a.store.SaveDomainFramework(projectID, domain, frameworkID, true)
	if err != nil {
		return fmt.Errorf("failed to set domain framework: %v", err)
	}

	return nil
}

// GetAllFrameworks returns a list of all supported frameworks
func (a *App) GetAllFrameworks() []types.FrameworkInfo {
	fws := framework.GetAllFrameworks()
	result := make([]types.FrameworkInfo, len(fws))
	for i, fw := range fws {
		result[i] = types.FrameworkInfo{
			ID:          fw.ID,
			Name:        fw.Name,
			Category:    fw.Category,
			Description: fw.Description,
		}
	}
	return result
}

// FrameworkFilterConfigResponse represents the filtering configuration for a framework
type FrameworkFilterConfigResponse struct {
	Framework    string   `json:"framework"`
	URLPatterns  []string `json:"urlPatterns"`
	QueryParams  []string `json:"queryParams"`
}

// GetFrameworkFilters returns the filtering rules for a specific framework
func (a *App) GetFrameworkFilters(frameworkID string) (*FrameworkFilterConfigResponse, error) {
	// Validate framework ID - allow "other" as a special case
	validFramework := frameworkID == "other"
	if !validFramework {
		for _, fw := range framework.GetAllFrameworks() {
			if fw.ID == frameworkID {
				validFramework = true
				break
			}
		}
	}

	if !validFramework {
		return nil, fmt.Errorf("invalid framework ID: %s", frameworkID)
	}

	config := framework.GetFilterConfig(framework.Framework(frameworkID))

	return &FrameworkFilterConfigResponse{
		Framework:   frameworkID,
		URLPatterns: config.URLPatterns,
		QueryParams: config.QueryParams,
	}, nil
}

// DetectFramework manually triggers framework detection for a given HTML content
// This is useful for testing or manual re-detection
func (a *App) DetectFramework(html string, networkURLs []string) string {
	detector := framework.NewDetector()
	detected := detector.Detect(html, networkURLs)
	return string(detected)
}
