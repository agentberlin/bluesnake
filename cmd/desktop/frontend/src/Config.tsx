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

import { useState, useEffect, useRef } from 'react';
import { GetConfigForDomain, UpdateConfigForDomain, GetDomainFrameworks, GetAllFrameworks, SetDomainFramework, GetProjects } from "../wailsjs/go/main/DesktopApp";
import './Config.css';

// Framework icons
import nextjsIcon from './assets/images/frameworks/nextjs.svg';
import reactIcon from './assets/images/frameworks/react.svg';
import vueIcon from './assets/images/frameworks/vue.svg';
import angularIcon from './assets/images/frameworks/angular.svg';
import nuxtjsIcon from './assets/images/frameworks/nuxtjs.svg';
import gatsbyIcon from './assets/images/frameworks/gatsby.svg';
import wordpressIcon from './assets/images/frameworks/wordpress.svg';
import shopifyIcon from './assets/images/frameworks/shopify.svg';
import webflowIcon from './assets/images/frameworks/webflow.svg';
import wixIcon from './assets/images/frameworks/wix.svg';
import drupalIcon from './assets/images/frameworks/drupal.svg';
import joomlaIcon from './assets/images/frameworks/joomla.svg';

type ConfigTab = 'scope' | 'rendering' | 'performance' | 'advanced' | 'frameworks';

// Map framework IDs to their icons
const frameworkIcons: Record<string, string> = {
  nextjs: nextjsIcon,
  react: reactIcon,
  vue: vueIcon,
  angular: angularIcon,
  nuxtjs: nuxtjsIcon,
  gatsby: gatsbyIcon,
  wordpress: wordpressIcon,
  shopify: shopifyIcon,
  webflow: webflowIcon,
  wix: wixIcon,
  drupal: drupalIcon,
  joomla: joomlaIcon,
};

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
  robotsTxtMode?: string;
  followInternalNofollow?: boolean;
  followExternalNofollow?: boolean;
  respectMetaRobotsNoindex?: boolean;
  respectNoindex?: boolean;
}

interface FrameworkInfo {
  id: string;
  name: string;
  category: string;
  description: string;
}

interface DomainFramework {
  domain: string;
  framework: string;
  detectedAt: number;
  manuallySet: boolean;
}

interface FrameworkDropdownProps {
  value: string;
  options: FrameworkInfo[];
  onChange: (frameworkId: string) => void;
}

function FrameworkDropdown({ value, options, onChange }: FrameworkDropdownProps) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const selectedOption = options.find(opt => opt.id === value && opt.id !== 'unknown');
  const displayName = selectedOption ? selectedOption.name : ((value === 'other' || value === 'unknown') ? 'Other' : 'Select framework');
  const selectedIcon = value !== 'other' && value !== 'unknown' && frameworkIcons[value];

  return (
    <div className="framework-custom-dropdown" ref={dropdownRef}>
      <div
        className={`framework-dropdown-header ${isOpen ? 'open' : ''}`}
        onClick={() => setIsOpen(!isOpen)}
      >
        <span className="framework-dropdown-value">
          {selectedIcon && <img src={selectedIcon} alt={displayName} className="framework-icon" />}
          {displayName}
        </span>
        <svg className="framework-dropdown-arrow" width="12" height="8" viewBox="0 0 12 8" fill="none">
          <path d="M1 1.5L6 6.5L11 1.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </div>
      {isOpen && (
        <div className="framework-dropdown-menu">
          {options.filter(opt => opt.id !== 'unknown').map((option) => {
            const icon = frameworkIcons[option.id];
            return (
              <div
                key={option.id}
                className={`framework-dropdown-option ${option.id === value ? 'selected' : ''}`}
                onClick={() => {
                  onChange(option.id);
                  setIsOpen(false);
                }}
              >
                <span className="framework-option-name">
                  {icon && <img src={icon} alt={option.name} className="framework-icon" />}
                  {option.name}
                </span>
              </div>
            );
          })}
          <div
            className={`framework-dropdown-option ${value === 'other' || value === 'unknown' ? 'selected' : ''}`}
            onClick={() => {
              onChange('other');
              setIsOpen(false);
            }}
          >
            <span className="framework-option-name">Other</span>
          </div>
        </div>
      )}
    </div>
  );
}

function Config({ url, onClose }: ConfigProps) {
  const [activeTab, setActiveTab] = useState<ConfigTab>('scope');
  const [jsRendering, setJsRendering] = useState(false);
  const [initialWaitMs, setInitialWaitMs] = useState(1500);
  const [scrollWaitMs, setScrollWaitMs] = useState(2000);
  const [finalWaitMs, setFinalWaitMs] = useState(1000);
  const [parallelism, setParallelism] = useState(5);
  const [userAgent, setUserAgent] = useState('bluesnake/1.0 (+https://snake.blue)');
  const [domain, setDomain] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [includeSubdomains, setIncludeSubdomains] = useState(true); // Default to true as per requirement
  const [sitemapEnabled, setSitemapEnabled] = useState(false);
  const [checkExternalResources, setCheckExternalResources] = useState(true); // Default to true
  const [singlePageMode, setSinglePageMode] = useState(false); // Default to false
  const [projectID, setProjectID] = useState<number | null>(null);
  const [allFrameworks, setAllFrameworks] = useState<FrameworkInfo[]>([]);
  const [domainFrameworks, setDomainFrameworks] = useState<DomainFramework[]>([]);
  const [frameworksLoading, setFrameworksLoading] = useState(false);

  // Crawler directive settings
  const [respectRobotsTxt, setRespectRobotsTxt] = useState(true);
  const [respectInternalNofollow, setRespectInternalNofollow] = useState(true);
  const [respectExternalNofollow, setRespectExternalNofollow] = useState(true);
  const [respectMetaRobotsNoindex, setRespectMetaRobotsNoindex] = useState(true);
  const [respectNoindex, setRespectNoindex] = useState(true);

  useEffect(() => {
    if (url) {
      loadConfig();
      loadFrameworks();
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
      setUserAgent(config.userAgent || 'bluesnake/1.0 (+https://snake.blue)');
      setIncludeSubdomains(config.includeSubdomains !== undefined ? config.includeSubdomains : true);

      // Derive sitemap state from mechanisms (spider is always enabled)
      const mechanisms = config.discoveryMechanisms || ["spider"];
      setSitemapEnabled(mechanisms.includes("sitemap"));
      setCheckExternalResources(config.checkExternalResources !== undefined ? config.checkExternalResources : true);
      setSinglePageMode(config.singlePageMode !== undefined ? config.singlePageMode : false);

      // Load crawler directive settings
      setRespectRobotsTxt(config.robotsTxtMode !== 'ignore');
      setRespectInternalNofollow(!config.followInternalNofollow);
      setRespectExternalNofollow(!config.followExternalNofollow);
      setRespectMetaRobotsNoindex(config.respectMetaRobotsNoindex !== undefined ? config.respectMetaRobotsNoindex : true);
      setRespectNoindex(config.respectNoindex !== undefined ? config.respectNoindex : true);
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

      // Set crawler directive defaults
      setRespectRobotsTxt(true);
      setRespectInternalNofollow(true);
      setRespectExternalNofollow(true);
      setRespectMetaRobotsNoindex(true);
      setRespectNoindex(true);
    } finally {
      setLoading(false);
    }
  };

  const loadFrameworks = async () => {
    setFrameworksLoading(true);
    try {
      // Get all supported frameworks
      const frameworks = await GetAllFrameworks();
      setAllFrameworks(frameworks || []);

      // Get the project for this URL to fetch domain frameworks
      const projects = await GetProjects();
      let normalizedUrl = url.trim();
      if (!normalizedUrl.startsWith('http://') && !normalizedUrl.startsWith('https://')) {
        normalizedUrl = 'https://' + normalizedUrl;
      }
      const urlObj = new URL(normalizedUrl);
      const urlDomain = urlObj.hostname;

      const project = projects.find(p => p.domain === urlDomain || p.url === url);
      if (project) {
        setProjectID(project.id);
        const domainFws = await GetDomainFrameworks(project.id);
        setDomainFrameworks(domainFws || []);
      }
    } catch (err) {
      console.error('Error loading frameworks:', err);
      // Ensure arrays are set even on error
      setAllFrameworks([]);
      setDomainFrameworks([]);
    } finally {
      setFrameworksLoading(false);
    }
  };

  const handleFrameworkChange = async (domain: string, frameworkID: string) => {
    if (!projectID) return;

    try {
      await SetDomainFramework(projectID, domain, frameworkID);
      // Reload frameworks to get updated data
      const domainFws = await GetDomainFrameworks(projectID);
      setDomainFrameworks(domainFws);
    } catch (err) {
      console.error('Error setting framework:', err);
      setError('Failed to update framework');
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
        singlePageMode,
        respectRobotsTxt ? "respect" : "ignore",
        !respectInternalNofollow,
        !respectExternalNofollow,
        respectMetaRobotsNoindex,
        respectNoindex
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
          <>
            <div className="config-tabs">
              <button
                className={`config-tab ${activeTab === 'scope' ? 'active' : ''}`}
                onClick={() => setActiveTab('scope')}
              >
                Scope
              </button>
              <button
                className={`config-tab ${activeTab === 'rendering' ? 'active' : ''}`}
                onClick={() => setActiveTab('rendering')}
              >
                Rendering
              </button>
              <button
                className={`config-tab ${activeTab === 'performance' ? 'active' : ''}`}
                onClick={() => setActiveTab('performance')}
              >
                Performance
              </button>
              <button
                className={`config-tab ${activeTab === 'frameworks' ? 'active' : ''}`}
                onClick={() => setActiveTab('frameworks')}
              >
                Frameworks
              </button>
              <button
                className={`config-tab ${activeTab === 'advanced' ? 'active' : ''}`}
                onClick={() => setActiveTab('advanced')}
              >
                Advanced
              </button>
            </div>

            <div className="config-content">
              <div className="config-tab-content">
                {error && <div className="config-error">{error}</div>}

                {activeTab === 'scope' && (
                  <div className="config-tab-panel">
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
                      <div className="config-warning-box">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
                          <line x1="12" y1="9" x2="12" y2="13"></line>
                          <line x1="12" y1="17" x2="12.01" y2="17"></line>
                        </svg>
                        <p>
                          Note: Single Page Mode is active. Discovery mechanisms (Spider/Sitemap) will be ignored.
                        </p>
                      </div>
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
                  </div>
                )}

                {activeTab === 'rendering' && (
                  <div className="config-tab-panel">
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
                            When enabled, pages will be rendered with a headless browser to execute JavaScript. On first crawl, this setting is automatically detected based on whether the site uses client-side rendering.
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
                  </div>
                )}

                {activeTab === 'performance' && (
                  <div className="config-tab-panel">
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
                  </div>
                )}

                {activeTab === 'frameworks' && (
                  <div className="config-tab-panel">
                    {frameworksLoading ? (
                      <div className="config-loading">Loading frameworks...</div>
                    ) : !projectID ? (
                      <div className="config-hint">
                        No project found. Please run a crawl first to detect frameworks.
                      </div>
                    ) : (!domainFrameworks || domainFrameworks.length === 0) ? (
                      <div className="config-hint">
                        No domains found. Frameworks will be detected during the crawl.
                      </div>
                    ) : (
                      <div className="config-field">
                        <label className="config-label-text">
                          Domain Frameworks
                        </label>
                        <p className="config-hint">
                          Select the framework for each domain. Frameworks are automatically detected during crawls, but you can override them here.
                        </p>
                        <div className="frameworks-list">
                          {domainFrameworks && domainFrameworks.map((df) => (
                            <div key={df.domain} className="framework-item">
                              <div className="framework-domain">
                                <span className="domain-name">{df.domain}</span>
                                {df.manuallySet && (
                                  <span className="manual-badge" title="Manually set by user">Manual</span>
                                )}
                              </div>
                              <FrameworkDropdown
                                value={df.framework}
                                options={allFrameworks}
                                onChange={(frameworkId) => handleFrameworkChange(df.domain, frameworkId)}
                              />
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {activeTab === 'advanced' && (
                  <div className="config-tab-panel">
                    <div className="config-field">
                      <label className="config-label-text">
                        User Agent
                      </label>
                      <input
                        type="text"
                        value={userAgent}
                        onChange={(e) => setUserAgent(e.target.value)}
                        className="config-number-input"
                        placeholder="bluesnake/1.0 (+https://snake.blue)"
                      />
                      <p className="config-hint">
                        Custom User-Agent string for HTTP requests (default: bluesnake/1.0)
                      </p>
                    </div>

                    <div className="config-field">
                      <label className="config-label">
                        <input
                          type="checkbox"
                          checked={respectRobotsTxt}
                          onChange={(e) => setRespectRobotsTxt(e.target.checked)}
                          className="config-checkbox"
                        />
                        <div>
                          <span className="checkbox-label">Respect Robots.txt</span>
                          <p className="config-hint">
                            When enabled, the crawler will respect robots.txt and block disallowed URLs. When disabled, robots.txt is completely ignored.
                          </p>
                        </div>
                      </label>
                    </div>

                    <div className="config-field">
                      <label className="config-label">
                        <input
                          type="checkbox"
                          checked={respectInternalNofollow}
                          onChange={(e) => setRespectInternalNofollow(e.target.checked)}
                          className="config-checkbox"
                        />
                        <div>
                          <span className="checkbox-label">Respect Internal Nofollow</span>
                          <p className="config-hint">
                            When enabled, the crawler will respect rel="nofollow", rel="sponsored", and rel="ugc" on internal links and skip them. When disabled, these links will be crawled.
                          </p>
                        </div>
                      </label>
                    </div>

                    <div className="config-field">
                      <label className="config-label">
                        <input
                          type="checkbox"
                          checked={respectExternalNofollow}
                          onChange={(e) => setRespectExternalNofollow(e.target.checked)}
                          className="config-checkbox"
                        />
                        <div>
                          <span className="checkbox-label">Respect External Nofollow</span>
                          <p className="config-hint">
                            When enabled, the crawler will respect rel="nofollow", rel="sponsored", and rel="ugc" on external links and skip them. When disabled, these links will be crawled.
                          </p>
                        </div>
                      </label>
                    </div>

                    <div className="config-field">
                      <label className="config-label">
                        <input
                          type="checkbox"
                          checked={respectMetaRobotsNoindex}
                          onChange={(e) => setRespectMetaRobotsNoindex(e.target.checked)}
                          className="config-checkbox"
                        />
                        <div>
                          <span className="checkbox-label">Respect Meta Robots Noindex</span>
                          <p className="config-hint">
                            When enabled, pages with &lt;meta name="robots" content="noindex"&gt; will be skipped during crawling
                          </p>
                        </div>
                      </label>
                    </div>

                    <div className="config-field">
                      <label className="config-label">
                        <input
                          type="checkbox"
                          checked={respectNoindex}
                          onChange={(e) => setRespectNoindex(e.target.checked)}
                          className="config-checkbox"
                        />
                        <div>
                          <span className="checkbox-label">Respect X-Robots-Tag Noindex</span>
                          <p className="config-hint">
                            When enabled, responses with X-Robots-Tag: noindex header will be skipped during crawling
                          </p>
                        </div>
                      </label>
                    </div>
                  </div>
                )}

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
          </>
        )}
      </div>
    </div>
  );
}

export default Config;
