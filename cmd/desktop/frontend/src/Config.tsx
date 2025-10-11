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

import { useState, useEffect } from 'react';
import { GetConfigForDomain, UpdateConfigForDomain } from "../wailsjs/go/main/DesktopApp";
import './Config.css';

interface ConfigProps {
  url: string;
  onClose: () => void;
}

interface ConfigData {
  domain: string;
  jsRenderingEnabled: boolean;
  parallelism: number;
  userAgent: string;
  discoveryMechanisms: string[];  // Not exposed directly, derived from checkboxes
}

function Config({ url, onClose }: ConfigProps) {
  const [jsRendering, setJsRendering] = useState(false);
  const [parallelism, setParallelism] = useState(5);
  const [userAgent, setUserAgent] = useState('bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)');
  const [domain, setDomain] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [sitemapEnabled, setSitemapEnabled] = useState(false);

  useEffect(() => {
    if (url) {
      loadConfig();
    }
  }, [url]);

  const loadConfig = async () => {
    setLoading(true);
    setError('');
    try {
      const config: ConfigData = await GetConfigForDomain(url);
      setDomain(config.domain);
      setJsRendering(config.jsRenderingEnabled);
      setParallelism(config.parallelism);
      setUserAgent(config.userAgent || 'bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)');

      // Derive sitemap state from mechanisms (spider is always enabled)
      const mechanisms = config.discoveryMechanisms || ["spider"];
      setSitemapEnabled(mechanisms.includes("sitemap"));
    } catch (err) {
      // Project doesn't exist yet - use defaults and extract domain from URL
      console.log('Project not found, using default configuration');
      try {
        let normalizedUrl = url.trim();
        if (!normalizedUrl.startsWith('http://') && !normalizedUrl.startsWith('https://')) {
          normalizedUrl = 'https://' + normalizedUrl;
        }
        const urlObj = new URL(normalizedUrl);
        setDomain(urlObj.hostname);
      } catch (urlErr) {
        setDomain(url);
      }

      // Set default values
      setJsRendering(false);
      setParallelism(5);
      setSitemapEnabled(false);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');

    try {
      await UpdateConfigForDomain(
        url,
        jsRendering,
        parallelism,
        userAgent,
        true, // Spider is always enabled
        sitemapEnabled,
        [] // No custom sitemap URLs in this version
      );
      onClose();
    } catch (err) {
      setError('Failed to save configuration');
      console.error('Error saving config:', err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="app">
      <div className="config-page">
        <div className="config-header">
          <div className="config-header-content">
            <h2 className="config-title">Configuration</h2>
            <button className="config-close-button" onClick={onClose} title="Close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="6" x2="6" y2="18"></line>
                <line x1="6" y1="6" x2="18" y2="18"></line>
              </svg>
            </button>
          </div>
          {domain && (
            <div className="config-domain-info">
              <span className="domain-label">Domain:</span>
              <span className="domain-value">{domain}</span>
            </div>
          )}
        </div>

        {loading ? (
          <div className="config-content">
            <div className="config-loading">Loading configuration...</div>
          </div>
        ) : (
          <div className="config-content">
            <div className="config-section">
              {error && <div className="config-error">{error}</div>}

              <div className="config-field">
                <label className="config-label">
                  <input
                    type="checkbox"
                    checked={jsRendering}
                    onChange={(e) => setJsRendering(e.target.checked)}
                    className="config-checkbox"
                  />
                  <div>
                    <span className="checkbox-label">Enable JavaScript Rendering</span>
                    <p className="config-hint">
                      When enabled, pages will be rendered with a headless browser to execute JavaScript
                    </p>
                  </div>
                </label>
              </div>

              <div className="config-field">
                <label className="config-label-text">
                  Number of Concurrent Requests
                </label>
                <input
                  type="number"
                  min="1"
                  max="100"
                  value={parallelism}
                  onChange={(e) => setParallelism(parseInt(e.target.value) || 1)}
                  className="config-number-input"
                />
                <p className="config-hint">
                  Maximum number of links to process at the same time (default: 5)
                </p>
              </div>

              <div className="config-field">
                <label className="config-label-text">
                  User Agent
                </label>
                <input
                  type="text"
                  value={userAgent}
                  onChange={(e) => setUserAgent(e.target.value)}
                  className="config-number-input"
                  placeholder="bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)"
                />
                <p className="config-hint">
                  Custom User-Agent string for HTTP requests (default: bluesnake/1.0)
                </p>
              </div>

              <div className="config-field">
                <label className="config-label-text">Discovery Mechanisms</label>

                <label className="config-label">
                  <input
                    type="checkbox"
                    checked={true}
                    disabled={true}
                    className="config-checkbox"
                  />
                  <div>
                    <span className="checkbox-label">Spider</span>
                    <p className="config-hint">Follow links discovered in HTML pages (always enabled)</p>
                  </div>
                </label>

                <label className="config-label">
                  <input
                    type="checkbox"
                    checked={sitemapEnabled}
                    onChange={(e) => setSitemapEnabled(e.target.checked)}
                    className="config-checkbox"
                  />
                  <div>
                    <span className="checkbox-label">Sitemap</span>
                    <p className="config-hint">
                      Discover URLs from sitemap.xml at default location (/sitemap.xml). When enabled, Spider is automatically included for comprehensive crawling.
                    </p>
                  </div>
                </label>
              </div>

              <div className="config-actions">
                <button className="config-cancel-button" onClick={onClose}>
                  Cancel
                </button>
                <button className="config-save-button" onClick={handleSave} disabled={saving}>
                  {saving ? 'Saving...' : 'Save Configuration'}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export default Config;
