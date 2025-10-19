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

package bluesnake

import (
	"context"
	"testing"
)

// TestConfigMerging verifies that user config is properly merged with defaults
// rather than completely replacing defaults with zero values.
// This addresses the systemic issue introduced in commit 7ee340f where
// struct-based config was overwriting defaults.
func TestConfigMerging(t *testing.T) {
	t.Run("AllowURLRevisit preserves IgnoreRobotsTxt default", func(t *testing.T) {
		// This was the bug that caused TestQueue to fail:
		// Setting AllowURLRevisit was inadvertently changing IgnoreRobotsTxt from its default
		c := NewCollector(context.Background(), &HTTPConfig{AllowURLRevisit: true})

		if !c.AllowURLRevisit {
			t.Error("AllowURLRevisit should be true")
		}

		// IgnoreRobotsTxt should remain false (the default, controlled by RobotsTxtMode="respect")
		if c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should remain false (default) when only setting AllowURLRevisit")
		}
	})

	t.Run("Single field config with zero values uses defaults where zero makes sense", func(t *testing.T) {
		// When setting just UserAgent, other defaults should be preserved
		// Note: MaxBodySize of 0 means "unlimited", so it won't preserve the default
		c := NewCollector(context.Background(), &HTTPConfig{UserAgent: "test-agent"})

		if c.UserAgent != "test-agent" {
			t.Error("UserAgent should be 'test-agent'")
		}

		// IgnoreRobotsTxt should preserve default (false, controlled by RobotsTxtMode="respect")
		if c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should remain false (default)")
		}
	})

	// Test removed - AllowedDomains is now a Crawler concern, not Collector

	t.Run("Multiple fields can override defaults", func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{
			UserAgent:   "custom-agent",
			MaxBodySize: 1024, // 1KB
		})

		// User-specified values
		if c.UserAgent != "custom-agent" {
			t.Error("UserAgent should be 'custom-agent'")
		}
		if c.MaxBodySize != 1024 {
			t.Error("MaxBodySize should be 1024")
		}

		// Defaults preserved for fields not in config
		if c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should remain false (default)")
		}
	})

	t.Run("Empty config behaves differently from nil config", func(t *testing.T) {
		c1 := NewCollector(context.Background(), &HTTPConfig{})
		c2 := NewCollector(context.Background(), nil)

		// MaxBodySize: empty config has 0 (unlimited), nil config has default 10MB
		// This is a limitation of non-pointer struct fields
		if c1.MaxBodySize == c2.MaxBodySize {
			t.Error("Empty config has MaxBodySize=0 (unlimited), nil config has 10MB")
		}

		// But important defaults like IgnoreRobotsTxt should be preserved
		if c1.IgnoreRobotsTxt != c2.IgnoreRobotsTxt {
			t.Error("Empty config should have same IgnoreRobotsTxt as nil config")
		}
		if c1.UserAgent != c2.UserAgent {
			t.Error("Empty config should have same UserAgent as nil config")
		}
	})

	t.Run("Nil config uses all defaults", func(t *testing.T) {
		c := NewCollector(context.Background(), nil)

		// Check key defaults
		if c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should be false by default")
		}

		// Note: RobotsTxtMode has been moved to Crawler - no longer on Collector

		expectedMaxBodySize := 10 * 1024 * 1024 // 10MB
		if c.MaxBodySize != expectedMaxBodySize {
			t.Errorf("MaxBodySize should be %d (default), got %d", expectedMaxBodySize, c.MaxBodySize)
		}

		defaultUserAgent := "bluesnake/1.0 (+https://snake.blue)"
		if c.UserAgent != defaultUserAgent {
			t.Errorf("UserAgent should be default, got %s", c.UserAgent)
		}
	})
}
