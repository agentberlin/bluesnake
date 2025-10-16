// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// This file includes modifications to code originally developed by Adam Tauber,
// licensed under the Apache License, Version 2.0.
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
	"net/http"
	"testing"
)

func TestNoAcceptHeader(t *testing.T) {
	mock := setupMockTransport()

	var receivedHeader string
	// checks if Accept is enabled by default
	func() {
		c := NewCollector(nil)
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedHeader = string(resp.Body)
		})
		c.Visit(testBaseURL + "/accept_header")
		if receivedHeader != "*/*" {
			t.Errorf("default Accept header isn't */*. got: %v", receivedHeader)
		}
	}()

	// checks if Accept can be disabled
	func() {
		c := NewCollector(nil)
		c.SetClient(&http.Client{Transport: mock})
		c.OnRequest(func(r *Request) {
			r.Headers.Del("Accept")
		})
		c.OnResponse(func(resp *Response) {
			receivedHeader = string(resp.Body)
		})
		c.Visit(testBaseURL + "/accept_header")
		if receivedHeader != "" {
			t.Errorf("failed to pass request with no Accept header. got: %v", receivedHeader)
		}
	}()
}

func TestNewCollector(t *testing.T) {
	t.Run("Functional Options", func(t *testing.T) {
		for name, test := range newCollectorTests {
			t.Run(name, test)
		}
	})
}

func TestRenderingConfigDefaults(t *testing.T) {
	config := NewDefaultConfig()

	if config.RenderingConfig == nil {
		t.Fatal("RenderingConfig should not be nil in default config")
	}

	if config.RenderingConfig.InitialWaitMs != 1500 {
		t.Errorf("Expected InitialWaitMs to be 1500, got %d", config.RenderingConfig.InitialWaitMs)
	}

	if config.RenderingConfig.ScrollWaitMs != 2000 {
		t.Errorf("Expected ScrollWaitMs to be 2000, got %d", config.RenderingConfig.ScrollWaitMs)
	}

	if config.RenderingConfig.FinalWaitMs != 1000 {
		t.Errorf("Expected FinalWaitMs to be 1000, got %d", config.RenderingConfig.FinalWaitMs)
	}
}

func TestRenderingConfigCustomValues(t *testing.T) {
	customConfig := &CollectorConfig{
		RenderingConfig: &RenderingConfig{
			InitialWaitMs: 3000,
			ScrollWaitMs:  4000,
			FinalWaitMs:   2000,
		},
	}

	collector := NewCollector(customConfig)

	if collector.RenderingConfig == nil {
		t.Fatal("Collector RenderingConfig should not be nil")
	}

	if collector.RenderingConfig.InitialWaitMs != 3000 {
		t.Errorf("Expected InitialWaitMs to be 3000, got %d", collector.RenderingConfig.InitialWaitMs)
	}

	if collector.RenderingConfig.ScrollWaitMs != 4000 {
		t.Errorf("Expected ScrollWaitMs to be 4000, got %d", collector.RenderingConfig.ScrollWaitMs)
	}

	if collector.RenderingConfig.FinalWaitMs != 2000 {
		t.Errorf("Expected FinalWaitMs to be 2000, got %d", collector.RenderingConfig.FinalWaitMs)
	}
}

func TestRenderingConfigMerging(t *testing.T) {
	// Test that custom RenderingConfig overrides defaults
	customConfig := &CollectorConfig{
		RenderingConfig: &RenderingConfig{
			InitialWaitMs: 5000,
			ScrollWaitMs:  6000,
			FinalWaitMs:   3000,
		},
	}

	collector := NewCollector(customConfig)

	// Verify custom values are preserved
	if collector.RenderingConfig.InitialWaitMs != 5000 {
		t.Errorf("Custom InitialWaitMs should be preserved, got %d", collector.RenderingConfig.InitialWaitMs)
	}

	if collector.RenderingConfig.ScrollWaitMs != 6000 {
		t.Errorf("Custom ScrollWaitMs should be preserved, got %d", collector.RenderingConfig.ScrollWaitMs)
	}

	if collector.RenderingConfig.FinalWaitMs != 3000 {
		t.Errorf("Custom FinalWaitMs should be preserved, got %d", collector.RenderingConfig.FinalWaitMs)
	}
}

func TestRenderingConfigNilHandling(t *testing.T) {
	// Test that nil RenderingConfig in custom config uses defaults
	customConfig := &CollectorConfig{
		RenderingConfig: nil,
	}

	collector := NewCollector(customConfig)

	if collector.RenderingConfig == nil {
		t.Fatal("Collector RenderingConfig should use defaults when nil is provided")
	}

	// Should fall back to defaults
	if collector.RenderingConfig.InitialWaitMs != 1500 {
		t.Errorf("Expected default InitialWaitMs to be 1500, got %d", collector.RenderingConfig.InitialWaitMs)
	}

	if collector.RenderingConfig.ScrollWaitMs != 2000 {
		t.Errorf("Expected default ScrollWaitMs to be 2000, got %d", collector.RenderingConfig.ScrollWaitMs)
	}

	if collector.RenderingConfig.FinalWaitMs != 1000 {
		t.Errorf("Expected default FinalWaitMs to be 1000, got %d", collector.RenderingConfig.FinalWaitMs)
	}
}

func TestRenderingConfigPartialOverride(t *testing.T) {
	// Test partial override scenario - only override one value
	customConfig := &CollectorConfig{
		RenderingConfig: &RenderingConfig{
			InitialWaitMs: 2500,
			ScrollWaitMs:  2000, // Keep default
			FinalWaitMs:   1000, // Keep default
		},
	}

	collector := NewCollector(customConfig)

	if collector.RenderingConfig.InitialWaitMs != 2500 {
		t.Errorf("Custom InitialWaitMs should be 2500, got %d", collector.RenderingConfig.InitialWaitMs)
	}

	if collector.RenderingConfig.ScrollWaitMs != 2000 {
		t.Errorf("ScrollWaitMs should be 2000, got %d", collector.RenderingConfig.ScrollWaitMs)
	}

	if collector.RenderingConfig.FinalWaitMs != 1000 {
		t.Errorf("FinalWaitMs should be 1000, got %d", collector.RenderingConfig.FinalWaitMs)
	}
}

func TestRenderingConfigZeroValues(t *testing.T) {
	// Test that zero values are preserved (user explicitly set to 0)
	customConfig := &CollectorConfig{
		RenderingConfig: &RenderingConfig{
			InitialWaitMs: 0,
			ScrollWaitMs:  0,
			FinalWaitMs:   0,
		},
	}

	collector := NewCollector(customConfig)

	if collector.RenderingConfig.InitialWaitMs != 0 {
		t.Errorf("Zero InitialWaitMs should be preserved, got %d", collector.RenderingConfig.InitialWaitMs)
	}

	if collector.RenderingConfig.ScrollWaitMs != 0 {
		t.Errorf("Zero ScrollWaitMs should be preserved, got %d", collector.RenderingConfig.ScrollWaitMs)
	}

	if collector.RenderingConfig.FinalWaitMs != 0 {
		t.Errorf("Zero FinalWaitMs should be preserved, got %d", collector.RenderingConfig.FinalWaitMs)
	}
}

func TestRenderingConfigBoundaryValues(t *testing.T) {
	// Test extreme values
	customConfig := &CollectorConfig{
		RenderingConfig: &RenderingConfig{
			InitialWaitMs: 30000, // 30 seconds (max in UI)
			ScrollWaitMs:  30000,
			FinalWaitMs:   30000,
		},
	}

	collector := NewCollector(customConfig)

	if collector.RenderingConfig.InitialWaitMs != 30000 {
		t.Errorf("Max InitialWaitMs should be preserved, got %d", collector.RenderingConfig.InitialWaitMs)
	}

	if collector.RenderingConfig.ScrollWaitMs != 30000 {
		t.Errorf("Max ScrollWaitMs should be preserved, got %d", collector.RenderingConfig.ScrollWaitMs)
	}

	if collector.RenderingConfig.FinalWaitMs != 30000 {
		t.Errorf("Max FinalWaitMs should be preserved, got %d", collector.RenderingConfig.FinalWaitMs)
	}
}

func TestCollectorConfigWithRenderingDisabled(t *testing.T) {
	// Test that RenderingConfig exists even when JS rendering might not be used
	// This ensures the configuration is always available
	collector := NewCollector(&CollectorConfig{})

	if collector.RenderingConfig == nil {
		t.Fatal("RenderingConfig should always be initialized")
	}

	// Should have default values
	if collector.RenderingConfig.InitialWaitMs != 1500 {
		t.Errorf("Expected default InitialWaitMs, got %d", collector.RenderingConfig.InitialWaitMs)
	}
}
