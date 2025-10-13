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
  initialWaitMs: number;
  scrollWaitMs: number;
  finalWaitMs: number;
  parallelism: number;
  userAgent: string;
  includeSubdomains: boolean;
  discoveryMechanisms: string[];  // Not exposed directly, derived from checkboxes
  checkExternalResources: boolean;
  singlePageMode: boolean;
}

function Config({ url, onClose }: ConfigProps) {
  const [jsRendering, setJsRendering] = useState(false);
  const [initialWaitMs, setInitialWaitMs] = useState(1500);
  const [scrollWaitMs, setScrollWaitMs] = useState(2000);
  const [finalWaitMs, setFinalWaitMs] = useState(1000);
  const [parallelism, setParallelism] = useState(5);
  const [userAgent, setUserAgent] = useState('bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)');
  const [domain, setDomain] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [includeSubdomains, setIncludeSubdomains] = useState(true); // Default to true as per requirement
  const [sitemapEnabled, setSitemapEnabled] = useState(false);
  const [checkExternalResources, setCheckExternalResources] = useState(true); // Default to true
  const [singlePageMode, setSinglePageMode] = useState(false); // Default to false

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
      setInitialWaitMs(config.initialWaitMs || 1500);
      setScrollWaitMs(config.scrollWaitMs || 2000);
      setFinalWaitMs(config.finalWaitMs || 1000);
      setParallelism(config.parallelism);
      setUserAgent(config.userAgent || 'bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)');
      setIncludeSubdomains(config.includeSubdomains !== undefined ? config.includeSubdomains : true);

      // Derive sitemap state from mechanisms (spider is always enabled)
      const mechanisms = config.discoveryMechanisms || ["spider"];
      setSitemapEnabled(mechanisms.includes("sitemap"));
      setCheckExternalResources(config.checkExternalResources !== undefined ? config.checkExternalResources : true);
      setSinglePageMode(config.singlePageMode !== undefined ? config.singlePageMode : false);
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
      setInitialWaitMs(1500);
      setScrollWaitMs(2000);
      setFinalWaitMs(1000);
      setParallelism(5);
      setIncludeSubdomains(true); // Default to including subdomains
      setSitemapEnabled(false);
      setCheckExternalResources(true); // Default to checking external resources
      setSinglePageMode(false); // Default to full website crawl
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
        initialWaitMs,
        scrollWaitMs,
        finalWaitMs,
        parallelism,
        userAgent,
        includeSubdomains,
        true, // Spider is always enabled
        sitemapEnabled,
        [], // No custom sitemap URLs in this version
        checkExternalResources,
        singlePageMode
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

              {jsRendering && (
                <>
                  <div className="config-field">
                    <label className="config-label-text">
                      Initial Wait Time (ms)
                    </label>
                    <input
                      type="number"
                      min="0"
                      max="30000"
                      step="100"
                      value={initialWaitMs}
                      onChange={(e) => setInitialWaitMs(parseInt(e.target.value) || 0)}
                      className="config-number-input"
                    />
                    <p className="config-hint">
                      Wait time after page load for JavaScript frameworks (React/Next.js) to hydrate (default: 1500ms)
                    </p>
                  </div>

                  <div className="config-field">
                    <label className="config-label-text">
                      Scroll Wait Time (ms)
                    </label>
                    <input
                      type="number"
                      min="0"
                      max="30000"
                      step="100"
                      value={scrollWaitMs}
                      onChange={(e) => setScrollWaitMs(parseInt(e.target.value) || 0)}
                      className="config-number-input"
                    />
                    <p className="config-hint">
                      Wait time after scrolling to bottom to trigger lazy-loaded images and content (default: 2000ms)
                    </p>
                  </div>

                  <div className="config-field">
                    <label className="config-label-text">
                      Final Wait Time (ms)
                    </label>
                    <input
                      type="number"
                      min="0"
                      max="30000"
                      step="100"
                      value={finalWaitMs}
                      onChange={(e) => setFinalWaitMs(parseInt(e.target.value) || 0)}
                      className="config-number-input"
                    />
                    <p className="config-hint">
                      Final wait time before capturing HTML for remaining network requests and DOM updates (default: 1000ms)
                    </p>
                  </div>
                </>
              )}

              <div className="config-field">
                <label className="config-label">
                  <input
                    type="checkbox"
                    checked={includeSubdomains}
                    onChange={(e) => setIncludeSubdomains(e.target.checked)}
                    className="config-checkbox"
                  />
                  <div>
                    <span className="checkbox-label">Include Subdomains</span>
                    <p className="config-hint">
                      When enabled, the crawler will include all subdomains of the main domain (e.g., blog.example.com, api.example.com)
                    </p>
                  </div>
                </label>
              </div>

              <div className="config-field">
                <label className="config-label">
                  <input
                    type="checkbox"
                    checked={checkExternalResources}
                    onChange={(e) => setCheckExternalResources(e.target.checked)}
                    className="config-checkbox"
                  />
                  <div>
                    <span className="checkbox-label">Check External Resources</span>
                    <p className="config-hint">
                      When enabled, validates external resources (images, CSS, JS) for broken links
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
                <label className="config-label">
                  <input
                    type="checkbox"
                    checked={singlePageMode}
                    onChange={(e) => setSinglePageMode(e.target.checked)}
                    className="config-checkbox"
                  />
                  <div>
                    <span className="checkbox-label">Single Page Mode</span>
                    <p className="config-hint">
                      When enabled, only the starting URL will be crawled without following any links or sitemaps. This overrides all discovery mechanism settings.
                    </p>
                  </div>
                </label>
              </div>

              {singlePageMode && (
                <div className="config-field" style={{ marginTop: '-8px', marginBottom: '16px' }}>
                  <div style={{ padding: '12px', backgroundColor: 'rgba(255, 196, 0, 0.1)', border: '1px solid rgba(255, 196, 0, 0.3)', borderRadius: '4px' }}>
                    <div style={{ display: 'flex', alignItems: 'flex-start', gap: '8px' }}>
                      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgb(255, 196, 0)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, marginTop: '2px' }}>
                        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
                        <line x1="12" y1="9" x2="12" y2="13"></line>
                        <line x1="12" y1="17" x2="12.01" y2="17"></line>
                      </svg>
                      <p style={{ margin: 0, fontSize: '13px', lineHeight: '1.5', color: 'rgb(255, 196, 0)' }}>
                        Note: Single Page Mode is active. Discovery mechanisms (Spider/Sitemap) will be ignored.
                      </p>
                    </div>
                  </div>
                </div>
              )}

              <div className="config-field">
                <label className="config-label-text">Discovery Mechanisms</label>

                <div style={{ opacity: singlePageMode ? 0.4 : 1, pointerEvents: singlePageMode ? 'none' : 'auto' }}>
                  <label className="config-label">
                    <input
                      type="checkbox"
                      checked={true}
                      disabled={true}
                      className="config-checkbox"
                    />
                    <div>
                      <span className="checkbox-label">Spider</span>
                      <p className="config-hint">Follow links discovered in HTML pages (always enabled{singlePageMode ? ', ignored in Single Page Mode' : ''})</p>
                    </div>
                  </label>

                  <label className="config-label">
                    <input
                      type="checkbox"
                      checked={sitemapEnabled}
                      onChange={(e) => setSitemapEnabled(e.target.checked)}
                      className="config-checkbox"
                      disabled={singlePageMode}
                    />
                    <div>
                      <span className="checkbox-label">Sitemap</span>
                      <p className="config-hint">
                        Discover URLs from sitemap.xml at default location (/sitemap.xml). When enabled, Spider is automatically included for comprehensive crawling{singlePageMode ? ' (ignored in Single Page Mode)' : ''}.
                      </p>
                    </div>
                  </label>
                </div>
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
