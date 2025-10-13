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

package store

import (
	"fmt"

	"gorm.io/gorm"
)

// GetDomainFramework gets the framework for a specific domain in a project
func (s *Store) GetDomainFramework(projectID uint, domain string) (*DomainFramework, error) {
	var framework DomainFramework
	result := s.db.Where("project_id = ? AND domain = ?", projectID, domain).First(&framework)

	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil // No framework detected yet
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get domain framework: %v", result.Error)
	}

	return &framework, nil
}

// GetAllDomainFrameworks gets all frameworks for a project
func (s *Store) GetAllDomainFrameworks(projectID uint) ([]DomainFramework, error) {
	var frameworks []DomainFramework
	result := s.db.Where("project_id = ?", projectID).Order("domain ASC").Find(&frameworks)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get domain frameworks: %v", result.Error)
	}

	return frameworks, nil
}

// SaveDomainFramework saves or updates a domain framework
func (s *Store) SaveDomainFramework(projectID uint, domain string, framework string, manuallySet bool) error {
	var existing DomainFramework
	result := s.db.Where("project_id = ? AND domain = ?", projectID, domain).First(&existing)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new
		newFramework := DomainFramework{
			ProjectID:   projectID,
			Domain:      domain,
			Framework:   framework,
			ManuallySet: manuallySet,
		}
		if err := s.db.Create(&newFramework).Error; err != nil {
			return fmt.Errorf("failed to create domain framework: %v", err)
		}
		return nil
	}

	if result.Error != nil {
		return fmt.Errorf("failed to check existing domain framework: %v", result.Error)
	}

	// Update existing
	existing.Framework = framework
	existing.ManuallySet = manuallySet
	if err := s.db.Save(&existing).Error; err != nil {
		return fmt.Errorf("failed to update domain framework: %v", err)
	}

	return nil
}

// DeleteDomainFramework deletes a domain framework entry
func (s *Store) DeleteDomainFramework(projectID uint, domain string) error {
	result := s.db.Where("project_id = ? AND domain = ?", projectID, domain).Delete(&DomainFramework{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete domain framework: %v", result.Error)
	}
	return nil
}
