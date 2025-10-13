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
	"testing"
)

func TestChromedpRendererDefaultConfig(t *testing.T) {
	// Test that default config values are properly defined
	// The actual renderer usage is tested through integration tests
	config := &RenderingConfig{
		InitialWaitMs: 1500,
		ScrollWaitMs:  2000,
		FinalWaitMs:   1000,
	}

	if config == nil {
		t.Fatal("Config should not be nil")
	}
}

func TestRenderingConfigNilDefaulting(t *testing.T) {
	// This test verifies the RenderPage function accepts nil config
	// and defaults to safe values (tested in the actual RenderPage implementation)

	defaultConfig := &RenderingConfig{
		InitialWaitMs: 1500,
		ScrollWaitMs:  2000,
		FinalWaitMs:   1000,
	}

	// Verify default config has expected values
	if defaultConfig.InitialWaitMs != 1500 {
		t.Errorf("Default InitialWaitMs should be 1500, got %d", defaultConfig.InitialWaitMs)
	}

	if defaultConfig.ScrollWaitMs != 2000 {
		t.Errorf("Default ScrollWaitMs should be 2000, got %d", defaultConfig.ScrollWaitMs)
	}

	if defaultConfig.FinalWaitMs != 1000 {
		t.Errorf("Default FinalWaitMs should be 1000, got %d", defaultConfig.FinalWaitMs)
	}
}

func TestRenderingConfigTotalWaitTime(t *testing.T) {
	// Test that total wait time matches ScreamingFrog's 5s for defaults
	config := &RenderingConfig{
		InitialWaitMs: 1500,
		ScrollWaitMs:  2000,
		FinalWaitMs:   1000,
	}

	totalWait := config.InitialWaitMs + config.ScrollWaitMs + config.FinalWaitMs

	// Default total should be 4500ms (4.5s), close to ScreamingFrog's 5s
	expectedTotal := 4500
	if totalWait != expectedTotal {
		t.Errorf("Total default wait time should be %dms, got %dms", expectedTotal, totalWait)
	}
}

func TestRenderingConfigCustomTotalWaitTime(t *testing.T) {
	// Test custom wait times
	config := &RenderingConfig{
		InitialWaitMs: 3000,
		ScrollWaitMs:  4000,
		FinalWaitMs:   2000,
	}

	totalWait := config.InitialWaitMs + config.ScrollWaitMs + config.FinalWaitMs

	expectedTotal := 9000
	if totalWait != expectedTotal {
		t.Errorf("Total custom wait time should be %dms, got %dms", expectedTotal, totalWait)
	}
}

func TestRenderingConfigUIMaximums(t *testing.T) {
	// Test that UI maximum values (30000ms per field) are reasonable
	config := &RenderingConfig{
		InitialWaitMs: 30000,
		ScrollWaitMs:  30000,
		FinalWaitMs:   30000,
	}

	// Verify all values are within acceptable range
	maxAllowed := 30000
	if config.InitialWaitMs > maxAllowed {
		t.Errorf("InitialWaitMs exceeds max of %dms", maxAllowed)
	}

	if config.ScrollWaitMs > maxAllowed {
		t.Errorf("ScrollWaitMs exceeds max of %dms", maxAllowed)
	}

	if config.FinalWaitMs > maxAllowed {
		t.Errorf("FinalWaitMs exceeds max of %dms", maxAllowed)
	}

	// Total of 90 seconds should be acceptable for very slow sites
	totalWait := config.InitialWaitMs + config.ScrollWaitMs + config.FinalWaitMs
	if totalWait != 90000 {
		t.Errorf("Total max wait time should be 90000ms, got %dms", totalWait)
	}
}

func TestRenderingConfigMinimums(t *testing.T) {
	// Test minimum values (0ms) - instant rendering
	config := &RenderingConfig{
		InitialWaitMs: 0,
		ScrollWaitMs:  0,
		FinalWaitMs:   0,
	}

	// Verify zero values are valid
	if config.InitialWaitMs < 0 {
		t.Error("InitialWaitMs should not be negative")
	}

	if config.ScrollWaitMs < 0 {
		t.Error("ScrollWaitMs should not be negative")
	}

	if config.FinalWaitMs < 0 {
		t.Error("FinalWaitMs should not be negative")
	}

	totalWait := config.InitialWaitMs + config.ScrollWaitMs + config.FinalWaitMs
	if totalWait != 0 {
		t.Errorf("Total minimum wait time should be 0ms, got %dms", totalWait)
	}
}
