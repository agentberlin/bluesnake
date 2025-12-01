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
import { GetConfigForDomain, UpdateConfigForDomain, UpdateIncrementalConfigForDomain, GetQueueStatus, ClearCrawlQueue } from "../wailsjs/go/main/DesktopApp";
import { Combobox } from './design-system/components/Combobox';
import { USER_AGENTS } from './constants/userAgents';
import './Config.css';

type ConfigTab = 'scope' | 'rendering' | 'performance' | 'budget' | 'advanced';

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
  robotsTxtMode?: string;
  followInternalNofollow?: boolean;
  followExternalNofollow?: boolean;
  respectMetaRobotsNoindex?: boolean;
  respectNoindex?: boolean;
  incrementalCrawlingEnabled?: boolean;
  crawlBudget?: number;
}

interface QueueStatusData {
  projectId: number;
  hasQueue: boolean;
  visited: number;
  pending: number;
  total: number;
  canResume: boolean;
  lastCrawlId: number;
  lastState: string;
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

  // Crawler directive settings
  const [respectRobotsTxt, setRespectRobotsTxt] = useState(true);
  const [respectInternalNofollow, setRespectInternalNofollow] = useState(true);
  const [respectExternalNofollow, setRespectExternalNofollow] = useState(true);
  const [respectMetaRobotsNoindex, setRespectMetaRobotsNoindex] = useState(true);
  const [respectNoindex, setRespectNoindex] = useState(true);

  // Incremental crawling settings
  const [incrementalCrawlingEnabled, setIncrementalCrawlingEnabled] = useState(false);
  const [crawlBudget, setCrawlBudget] = useState(1000);
  const [queueStatus, setQueueStatus] = useState<QueueStatusData | null>(null);
  const [showClearQueueModal, setShowClearQueueModal] = useState(false);
  const [isClearingQueue, setIsClearingQueue] = useState(false);
  const [projectId, setProjectId] = useState<number | null>(null);

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
      setUserAgent(config.userAgent || 'bluesnake/1.0 (+https://snake.blue)');
      setIncludeSubdomains(config.includeSubdomains !== undefined ? config.includeSubdomains : true);

      // Derive sitemap state from mechanisms (spider is always enabled)
      const mechanisms = config.discoveryMechanisms || ["spider"];
      setSitemapEnabled(mechanisms.includes("sitemap"));
      setCheckExternalResources(config.checkExternalResources !== undefined ? config.checkExternalResources : true);

      // Load crawler directive settings
      setRespectRobotsTxt(config.robotsTxtMode !== 'ignore');
      setRespectInternalNofollow(!config.followInternalNofollow);
      setRespectExternalNofollow(!config.followExternalNofollow);
      setRespectMetaRobotsNoindex(config.respectMetaRobotsNoindex !== undefined ? config.respectMetaRobotsNoindex : true);
      setRespectNoindex(config.respectNoindex !== undefined ? config.respectNoindex : true);

      // Load incremental crawling settings
      setIncrementalCrawlingEnabled(config.incrementalCrawlingEnabled || false);
      setCrawlBudget(config.crawlBudget || 1000);
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

      // Set crawler directive defaults
      setRespectRobotsTxt(true);
      setRespectInternalNofollow(true);
      setRespectExternalNofollow(true);
      setRespectMetaRobotsNoindex(true);
      setRespectNoindex(true);

      // Set incremental crawling defaults
      setIncrementalCrawlingEnabled(false);
      setCrawlBudget(1000);
    } finally {
      setLoading(false);
    }
  };

  // Load queue status when project ID is available
  const loadQueueStatus = async (pid: number) => {
    try {
      const status = await GetQueueStatus(pid);
      setQueueStatus(status);
      setProjectId(pid);
    } catch (err) {
      console.log('Failed to load queue status:', err);
      setQueueStatus(null);
    }
  };

  // Handle clearing the queue
  const handleClearQueue = async () => {
    if (!projectId) return;

    setIsClearingQueue(true);
    try {
      await ClearCrawlQueue(projectId);
      // Reload queue status
      await loadQueueStatus(projectId);
      setShowClearQueueModal(false);
    } catch (err) {
      console.error('Failed to clear queue:', err);
    } finally {
      setIsClearingQueue(false);
    }
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');

    try {
      // Save main config
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
        respectRobotsTxt ? "respect" : "ignore",
        !respectInternalNofollow,
        !respectExternalNofollow,
        respectMetaRobotsNoindex,
        respectNoindex
      );

      // Save incremental crawling settings
      await UpdateIncrementalConfigForDomain(
        url,
        incrementalCrawlingEnabled,
        incrementalCrawlingEnabled ? crawlBudget : 0
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
                className={`config-tab ${activeTab === 'budget' ? 'active' : ''}`}
                onClick={() => setActiveTab('budget')}
              >
                Budget
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

                      <div>
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

                {activeTab === 'budget' && (
                  <div className="config-tab-panel">
                    <div className="config-field">
                      <label className="config-label">
                        <input
                          type="checkbox"
                          checked={incrementalCrawlingEnabled}
                          onChange={(e) => setIncrementalCrawlingEnabled(e.target.checked)}
                          className="config-checkbox"
                        />
                        <div>
                          <span className="checkbox-label">Enable Incremental Crawling</span>
                          <p className="config-hint">
                            Crawl websites in chunks with pause/resume capability. Set a budget for maximum URLs per session.
                          </p>
                        </div>
                      </label>
                    </div>

                    {incrementalCrawlingEnabled && (
                      <div className="config-field">
                        <label className="config-label-text">
                          URLs per Session
                        </label>
                        <input
                          type="number"
                          min="0"
                          step="100"
                          value={crawlBudget}
                          onChange={(e) => setCrawlBudget(parseInt(e.target.value) || 0)}
                          className="config-number-input"
                        />
                        <p className="config-hint">
                          Maximum number of URLs to crawl per session. Set to 0 for unlimited. The crawler will pause when the limit is reached.
                        </p>
                      </div>
                    )}

                    {incrementalCrawlingEnabled && queueStatus && queueStatus.hasQueue && (
                      <div className="config-field queue-status-section">
                        <label className="config-label-text">Queue Status</label>
                        <div className="queue-status-grid">
                          <div className="queue-status-item">
                            <span className="queue-status-value">{queueStatus.visited}</span>
                            <span className="queue-status-label">Visited</span>
                          </div>
                          <div className="queue-status-item">
                            <span className="queue-status-value">{queueStatus.pending}</span>
                            <span className="queue-status-label">Pending</span>
                          </div>
                          <div className="queue-status-item">
                            <span className="queue-status-value">{queueStatus.total}</span>
                            <span className="queue-status-label">Total</span>
                          </div>
                        </div>
                        {queueStatus.pending > 0 && (
                          <button
                            className="clear-queue-button"
                            onClick={() => setShowClearQueueModal(true)}
                            type="button"
                          >
                            Clear Queue
                          </button>
                        )}
                        <p className="config-hint queue-hint">
                          {queueStatus.canResume
                            ? `Last crawl was paused. You can resume from the dashboard.`
                            : queueStatus.pending > 0
                              ? `Queue has pending URLs from previous crawl.`
                              : `Queue is empty.`}
                        </p>
                      </div>
                    )}

                    {incrementalCrawlingEnabled && (
                      <div className="config-info-box">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <circle cx="12" cy="12" r="10"></circle>
                          <line x1="12" y1="16" x2="12" y2="12"></line>
                          <line x1="12" y1="8" x2="12.01" y2="8"></line>
                        </svg>
                        <div>
                          <strong>How it works:</strong>
                          <p>When enabled, the crawler will stop after reaching the URL budget. You can resume crawling from the dashboard to continue where you left off.</p>
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
                      <Combobox
                        value={userAgent}
                        options={USER_AGENTS}
                        onChange={(value) => setUserAgent(value)}
                        placeholder="Type or select a user-agent"
                        allowCustomValue={true}
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

        {/* Clear Queue Confirmation Modal */}
        {showClearQueueModal && (
          <div className="modal-overlay" onClick={() => setShowClearQueueModal(false)}>
            <div className="modal" onClick={(e) => e.stopPropagation()}>
              <h3>Clear Crawl Queue</h3>
              <p>Are you sure you want to clear the crawl queue? This will reset all pending URLs and the next crawl will start fresh.</p>
              <div className="modal-actions">
                <button className="modal-button cancel" onClick={() => setShowClearQueueModal(false)}>
                  Cancel
                </button>
                <button
                  className="modal-button delete"
                  onClick={handleClearQueue}
                  disabled={isClearingQueue}
                >
                  {isClearingQueue ? 'Clearing...' : 'Clear Queue'}
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
