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
import './AICrawlers.css';
import { GetAICrawlerData, RunAICrawlerChecks } from '../wailsjs/go/main/DesktopApp';
import { types } from '../wailsjs/go/models';

interface ContentVisibilityData {
  score: number;
  screenshot: string;
  status_code: number;
  is_error: boolean;
}

interface RobotsTxtData {
  [botName: string]: {
    allowed: boolean;
    domain: string;
  };
}

interface HTTPCheckData {
  [botName: string]: {
    status: string;
    status_code: number;
    success: boolean;
    domain: string;
  };
}

interface ScreenshotsData {
  js_enabled_screenshot: string;
  js_disabled_screenshot: string;
}

interface BotAccessData {
  allowed: boolean;
  domain: string;
  message?: string;
}

interface BotCardProps {
  botName: string;
  data: BotAccessData;
  isLoading: boolean;
  index: number;
}

function BotCard({ botName, data, isLoading, index }: BotCardProps) {
  if (isLoading) {
    return (
      <div className="bot-card bot-card-loading">
        <div className="bot-card-header">
          <div className="bot-icon-skeleton"></div>
          <div className="bot-info-skeleton">
            <div className="bot-name-skeleton"></div>
            <div className="bot-status-skeleton"></div>
          </div>
        </div>
      </div>
    );
  }

  const statusClass = data.allowed ? 'bot-card-allowed' : 'bot-card-blocked';
  const statusDotClass = data.allowed ? 'status-dot-allowed' : 'status-dot-blocked';
  const statusTextClass = data.allowed ? 'status-text-allowed' : 'status-text-blocked';
  const statusText = data.allowed ? 'Allowed' : 'Blocked';

  return (
    <div className={`bot-card ${statusClass}`}>
      <div className="bot-card-header">
        <img
          src={`https://www.google.com/s2/favicons?domain=${data.domain}&sz=32`}
          alt={botName}
          className="bot-icon"
          onError={(e) => {
            const target = e.target as HTMLImageElement;
            target.style.display = 'none';
            const parent = target.parentElement;
            if (parent) {
              const fallback = document.createElement('div');
              fallback.className = 'bot-icon-fallback';
              fallback.innerHTML = `
                <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <circle cx="12" cy="12" r="10"></circle>
                  <path d="M2 12h20"></path>
                </svg>
              `;
              parent.insertBefore(fallback, target);
            }
          }}
        />
        <div className="bot-info">
          <div className="bot-name">{botName}</div>
          <div className="bot-status">
            <span className={`status-dot ${statusDotClass}`}></span>
            <span className={statusTextClass}>
              {statusText}
              {data.message && <span className="status-message"> ({data.message})</span>}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

interface CircularProgressProps {
  percentage: number;
  isLoading: boolean;
}

function CircularProgress({ percentage, isLoading }: CircularProgressProps) {
  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (circumference * percentage / 100);

  if (isLoading) {
    return (
      <div className="content-visibility-loading">
        <div className="circular-progress-skeleton">
          <svg width="180" height="180" viewBox="0 0 100 100" className="progress-ring">
            <circle
              className="progress-ring-skeleton"
              cx="50"
              cy="50"
              r={radius}
              strokeWidth="3"
              fill="none"
            />
          </svg>
        </div>
        <div className="progress-text-skeleton"></div>
      </div>
    );
  }

  return (
    <div className="circular-progress-container">
      <svg width="180" height="180" viewBox="0 0 100 100" className="progress-ring">
        <circle
          className="progress-ring-bg"
          cx="50"
          cy="50"
          r={radius}
          strokeWidth="3"
          fill="none"
        />
        <circle
          className="progress-ring-progress"
          cx="50"
          cy="50"
          r={radius}
          strokeWidth="3"
          fill="none"
          strokeDasharray={circumference}
          strokeDashoffset={strokeDashoffset}
          transform="rotate(-90 50 50)"
        />
      </svg>
      <div className="progress-text">{percentage.toFixed(1)}%</div>
    </div>
  );
}

interface AICrawlersProps {
  url: string;
}

function AICrawlers({ url }: AICrawlersProps) {
  const [showComparison, setShowComparison] = useState(false);
  const [hasData, setHasData] = useState(false);
  const [isLoading, setIsLoading] = useState(true);

  // Results
  const [contentVisibility, setContentVisibility] = useState<ContentVisibilityData | null>(null);
  const [robotsTxt, setRobotsTxt] = useState<RobotsTxtData | null>(null);
  const [httpCheck, setHttpCheck] = useState<HTTPCheckData | null>(null);
  const [screenshots, setScreenshots] = useState<ScreenshotsData | null>(null);

  // Errors
  const [errorSSR, setErrorSSR] = useState<string>('');
  const [errorRobots, setErrorRobots] = useState<string>('');
  const [errorHTTP, setErrorHTTP] = useState<string>('');

  useEffect(() => {
    if (url) {
      loadExistingData();
    }
  }, [url]);

  const loadExistingData = async () => {
    setIsLoading(true);
    try {
      const response = await GetAICrawlerData(url);

      if (response && response.data) {
        // Load existing data
        const data = response.data;
        setHasData(true);

        // Load content visibility
        if (data.contentVisibility) {
          setContentVisibility({
            score: data.contentVisibility.score,
            screenshot: response.ssrScreenshot || '',
            status_code: data.contentVisibility.statusCode,
            is_error: data.contentVisibility.isError
          });
        }

        // Load robots.txt
        if (data.robotsTxt) {
          const robotsData: RobotsTxtData = {};
          Object.entries(data.robotsTxt).forEach(([botName, botData]: [string, any]) => {
            robotsData[botName] = {
              allowed: botData.allowed,
              domain: botData.domain
            };
          });
          setRobotsTxt(robotsData);
        }

        // Load HTTP check
        if (data.httpCheck) {
          const httpData: HTTPCheckData = {};
          Object.entries(data.httpCheck).forEach(([botName, botData]: [string, any]) => {
            httpData[botName] = {
              status: botData.message || '',
              status_code: 0,
              success: botData.allowed,
              domain: botData.domain
            };
          });
          setHttpCheck(httpData);
        }

        // Load screenshots
        if (response.jsScreenshot && response.noJSScreenshot) {
          setScreenshots({
            js_enabled_screenshot: response.jsScreenshot,
            js_disabled_screenshot: response.noJSScreenshot
          });
        }
      } else {
        // No existing data, run checks
        setHasData(false);
        await handleRunChecks();
      }
    } catch (error) {
      console.error('Error loading AI crawler data:', error);
      setHasData(false);
      await handleRunChecks();
    } finally {
      setIsLoading(false);
    }
  };

  const handleRunChecks = async () => {
    setIsLoading(true);

    // Reset states
    setShowComparison(false);
    setContentVisibility(null);
    setRobotsTxt(null);
    setHttpCheck(null);
    setScreenshots(null);
    setErrorSSR('');
    setErrorRobots('');
    setErrorHTTP('');

    try {
      // Run all checks in the backend
      await RunAICrawlerChecks(url);

      // Load the results
      await loadExistingData();
    } catch (error) {
      console.error('Error running AI crawler checks:', error);
      setErrorSSR(String(error));
    } finally {
      setIsLoading(false);
    }
  };


  // Show loader during initial load or when running checks
  if (isLoading) {
    return (
      <div className="ai-crawlers-page">
        <div className="ai-crawlers-loading">
          <div className="loading-spinner">
            <svg width="32" height="32" viewBox="0 0 24 24" className="spinner-svg">
              <circle cx="12" cy="12" r="10" stroke="#e0e0e0" strokeWidth="2" fill="none" />
              <circle className="spinner-circle" cx="12" cy="12" r="10" stroke="#2196f3" strokeWidth="2" fill="none" />
            </svg>
          </div>
          <p className="loading-text">Loading AI Crawler data...</p>
        </div>
      </div>
    );
  }

  return (
    <div className="ai-crawlers-page">
      <div className="ai-crawlers-content">
        {/* Run Again Button */}
        <div className="ai-crawlers-header">
          <button
            className="run-again-button"
            onClick={handleRunChecks}
          >
            Run Again
          </button>
        </div>

        {/* Main Content Grid */}
        <div className="ai-crawlers-main-grid">
          {/* Bot vs Human View */}
          <div className="content-card bot-human-card">
            <div className="card-header">
              <h4 className="card-title">Bot vs. Human View</h4>
              <button
                className="compare-button"
                onClick={() => setShowComparison(true)}
                disabled={!screenshots}
              >
                Compare
              </button>
            </div>
            <p className="card-description">
              See how your page appears to bots compared to human visitors
            </p>
            {contentVisibility?.screenshot ? (
              <img
                src={`data:image/png;base64,${contentVisibility.screenshot}`}
                alt="Page screenshot"
                className="page-screenshot"
              />
            ) : (
              <div className="screenshot-placeholder">No screenshot available</div>
            )}
          </div>

          {/* Content Visibility */}
          <div className="content-card content-visibility-card">
            <h4 className="card-title">Content Visibility</h4>
            <p className="card-description">
              Percentage of content visible to bots without JavaScript
            </p>
            {errorSSR ? (
              <div className="error-message">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <circle cx="12" cy="12" r="10"></circle>
                  <line x1="12" y1="8" x2="12" y2="12"></line>
                  <line x1="12" y1="16" x2="12.01" y2="16"></line>
                </svg>
                <span>{errorSSR}</span>
              </div>
            ) : (
              <CircularProgress
                percentage={contentVisibility?.score || 0}
                isLoading={false}
              />
            )}
          </div>
        </div>

        {/* Robots.txt Analysis */}
        <div className="ai-crawlers-section">
          <h4 className="section-title">Robots.txt Analysis</h4>
          <p className="section-description">
            Shows which bots are allowed or blocked by your robots.txt file
          </p>
          {errorRobots ? (
            <div className="error-message">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="10"></circle>
                <line x1="12" y1="8" x2="12" y2="12"></line>
                <line x1="12" y1="16" x2="12.01" y2="16"></line>
              </svg>
              <span>{errorRobots}</span>
            </div>
          ) : robotsTxt ? (
            <div className="bot-cards-grid">
              {Object.entries(robotsTxt).map(([botName, data], index) => (
                <BotCard
                  key={botName}
                  botName={botName}
                  data={{ allowed: data.allowed, domain: data.domain }}
                  isLoading={false}
                  index={index}
                />
              ))}
            </div>
          ) : null}
        </div>

        {/* Firewall & Proxy Check */}
        <div className="ai-crawlers-section">
          <h4 className="section-title">Firewall & Proxy Check</h4>
          <p className="section-description">
            Tests if bots are blocked by firewalls or proxy services
          </p>
          {errorHTTP ? (
            <div className="error-message">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="10"></circle>
                <line x1="12" y1="8" x2="12" y2="12"></line>
                <line x1="12" y1="16" x2="12.01" y2="16"></line>
              </svg>
              <span>{errorHTTP}</span>
            </div>
          ) : httpCheck ? (
            <div className="bot-cards-grid">
              {Object.entries(httpCheck).map(([botName, data], index) => (
                <BotCard
                  key={botName}
                  botName={botName}
                  data={{
                    allowed: data.success,
                    domain: data.domain,
                    message: data.status
                  }}
                  isLoading={false}
                  index={index}
                />
              ))}
            </div>
          ) : null}
        </div>
      </div>

      {/* Comparison Modal */}
      {showComparison && (
        <div className="comparison-modal-overlay" onClick={() => setShowComparison(false)}>
          <div className="comparison-modal" onClick={(e) => e.stopPropagation()}>
            <div className="comparison-header">
              <div className="comparison-header-content">
                <h3 className="comparison-title">Bot View vs Human View</h3>
                <p className="comparison-description">
                  Compare how your website appears with and without JavaScript
                </p>
              </div>
              <button className="comparison-close-button" onClick={() => setShowComparison(false)}>
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <line x1="18" y1="6" x2="6" y2="18"></line>
                  <line x1="6" y1="6" x2="18" y2="18"></line>
                </svg>
              </button>
            </div>
            <div className="comparison-content">
              <div className="comparison-panel">
                <div className="comparison-panel-header">
                  <h4>Human View (JS Enabled)</h4>
                </div>
                <div className="comparison-panel-body">
                  {screenshots?.js_enabled_screenshot ? (
                    <img
                      src={`data:image/png;base64,${screenshots.js_enabled_screenshot}`}
                      alt="With JavaScript"
                      className="comparison-screenshot"
                    />
                  ) : (
                    <div className="comparison-loading">Loading...</div>
                  )}
                </div>
              </div>
              <div className="comparison-divider"></div>
              <div className="comparison-panel">
                <div className="comparison-panel-header">
                  <h4>Bot View (JS Disabled)</h4>
                </div>
                <div className="comparison-panel-body">
                  {screenshots?.js_disabled_screenshot ? (
                    <img
                      src={`data:image/png;base64,${screenshots.js_disabled_screenshot}`}
                      alt="Without JavaScript"
                      className="comparison-screenshot"
                    />
                  ) : (
                    <div className="comparison-loading">Loading...</div>
                  )}
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default AICrawlers;
