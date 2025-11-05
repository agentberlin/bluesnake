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
import './Competitors.css';
import {
  GetCompetitors,
  GetCompetitorStats,
  StartCompetitorCrawl,
  DeleteCompetitor,
  StopCrawl,
  GetFaviconData,
} from '../wailsjs/go/main/DesktopApp';

interface CompetitorInfo {
  id: number;
  url: string;
  domain: string;
  faviconPath: string;
  crawlDateTime: number;
  crawlDuration: number;
  pagesCrawled: number;
  totalUrls: number;
  latestCrawlId: number;
  isCrawling: boolean;
}

interface CompetitorStats {
  totalCompetitors: number;
  totalPages: number;
  lastCrawlTime: number;
  activeCrawls: number;
}

interface CompetitorsProps {
  onCompetitorClick: (competitor: CompetitorInfo) => void;
}

function Competitors({ onCompetitorClick }: CompetitorsProps) {
  const [competitors, setCompetitors] = useState<CompetitorInfo[]>([]);
  const [stats, setStats] = useState<CompetitorStats | null>(null);
  const [isAddingCompetitor, setIsAddingCompetitor] = useState(false);
  const [newCompetitorUrl, setNewCompetitorUrl] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [faviconCache, setFaviconCache] = useState<Map<string, string>>(new Map());

  // Load competitors and stats
  useEffect(() => {
    loadData();
    const interval = setInterval(loadData, 2000); // Poll every 2 seconds
    return () => clearInterval(interval);
  }, []);

  const loadData = async () => {
    try {
      const [competitorsData, statsData] = await Promise.all([
        GetCompetitors(),
        GetCompetitorStats(),
      ]);
      setCompetitors(competitorsData || []);
      setStats(statsData);

      // Load favicons
      competitorsData?.forEach(async (competitor: CompetitorInfo) => {
        if (competitor.faviconPath && !faviconCache.has(competitor.faviconPath)) {
          try {
            const faviconData = await GetFaviconData(competitor.faviconPath);
            setFaviconCache((prev) => new Map(prev).set(competitor.faviconPath, faviconData));
          } catch (err) {
            console.error('Failed to load favicon:', err);
          }
        }
      });
    } catch (err) {
      console.error('Failed to load competitors:', err);
    }
  };

  const handleAddCompetitor = async () => {
    if (!newCompetitorUrl.trim()) {
      setError('Please enter a valid URL');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      await StartCompetitorCrawl(newCompetitorUrl);
      setNewCompetitorUrl('');
      setIsAddingCompetitor(false);
      loadData();
    } catch (err: any) {
      setError(err?.message || 'Failed to add competitor');
    } finally {
      setLoading(false);
    }
  };

  const handleDeleteCompetitor = async (competitorId: number) => {
    if (!confirm('Are you sure you want to delete this competitor and all its crawl data?')) {
      return;
    }

    try {
      await DeleteCompetitor(competitorId);
      loadData();
    } catch (err) {
      console.error('Failed to delete competitor:', err);
      alert('Failed to delete competitor');
    }
  };

  const handleStopCrawl = async (competitorId: number) => {
    try {
      await StopCrawl(competitorId);
      loadData();
    } catch (err) {
      console.error('Failed to stop crawl:', err);
    }
  };

  const formatDuration = (ms: number) => {
    if (!ms) return 'N/A';
    const seconds = Math.floor(ms / 1000);
    const minutes = Math.floor(seconds / 60);
    const hours = Math.floor(minutes / 60);

    if (hours > 0) return `${hours}h ${minutes % 60}m`;
    if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
    return `${seconds}s`;
  };

  const formatTimeAgo = (timestamp: number) => {
    if (!timestamp) return 'Never';
    const now = Date.now();
    const diff = now - timestamp;
    const seconds = Math.floor(diff / 1000);
    const minutes = Math.floor(seconds / 60);
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);

    if (days > 0) return `${days}d ago`;
    if (hours > 0) return `${hours}h ago`;
    if (minutes > 0) return `${minutes}m ago`;
    return `${seconds}s ago`;
  };

  return (
    <div className="competitors-container">
      <div className="competitors-header">
        <h1>Competitors</h1>
        <button
          className="add-competitor-button"
          onClick={() => setIsAddingCompetitor(true)}
        >
          + Add Competitor
        </button>
      </div>

      {/* Overview Statistics */}
      {stats && (
        <div className="competitors-stats">
          <div className="stat-card">
            <div className="stat-label">Total Competitors</div>
            <div className="stat-value">{stats.totalCompetitors}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Total Pages</div>
            <div className="stat-value">{stats.totalPages.toLocaleString()}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Last Crawl</div>
            <div className="stat-value">{formatTimeAgo(stats.lastCrawlTime)}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Active Crawls</div>
            <div className="stat-value">{stats.activeCrawls}</div>
          </div>
        </div>
      )}

      {/* Add Competitor Modal */}
      {isAddingCompetitor && (
        <div className="modal-overlay" onClick={() => setIsAddingCompetitor(false)}>
          <div className="modal-content" onClick={(e) => e.stopPropagation()}>
            <h2>Add Competitor</h2>
            <input
              type="text"
              className="competitor-url-input"
              placeholder="https://example.com"
              value={newCompetitorUrl}
              onChange={(e) => setNewCompetitorUrl(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleAddCompetitor()}
              autoFocus
            />
            {error && <div className="error-message">{error}</div>}
            <div className="modal-actions">
              <button
                className="modal-button cancel"
                onClick={() => setIsAddingCompetitor(false)}
                disabled={loading}
              >
                Cancel
              </button>
              <button
                className="modal-button primary"
                onClick={handleAddCompetitor}
                disabled={loading}
              >
                {loading ? 'Starting...' : 'Start Crawl'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Competitors List */}
      <div className="competitors-section">
        <h2>Competitor Domains</h2>
        {competitors.length === 0 ? (
          <div className="empty-state">
            <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
              <circle cx="12" cy="12" r="10"></circle>
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"></path>
              <path d="M2 12h20"></path>
            </svg>
            <h3>No Competitors Yet</h3>
            <p>Add competitor domains to track and analyze their websites.</p>
          </div>
        ) : (
          <div className="competitors-grid">
            {competitors.map((competitor) => (
              <div key={competitor.id} className="competitor-card">
                <div className="competitor-header">
                  <div className="competitor-info">
                    {competitor.faviconPath && faviconCache.has(competitor.faviconPath) ? (
                      <img
                        src={faviconCache.get(competitor.faviconPath)}
                        alt="Favicon"
                        className="competitor-favicon"
                      />
                    ) : (
                      <div className="competitor-favicon-placeholder">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor">
                          <circle cx="12" cy="12" r="10"></circle>
                        </svg>
                      </div>
                    )}
                    <div className="competitor-domain">{competitor.domain}</div>
                  </div>
                  <button
                    className="delete-competitor-button"
                    onClick={(e) => {
                      e.stopPropagation();
                      handleDeleteCompetitor(competitor.id);
                    }}
                    title="Delete competitor"
                  >
                    ×
                  </button>
                </div>

                {competitor.isCrawling ? (
                  <div className="competitor-crawling">
                    <div className="crawling-indicator">
                      <div className="spinner"></div>
                      <span>Crawling...</span>
                    </div>
                    <button
                      className="stop-crawl-button"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleStopCrawl(competitor.id);
                      }}
                    >
                      Stop
                    </button>
                  </div>
                ) : (
                  <div className="competitor-stats">
                    <div className="stat-item">
                      <span className="stat-label">Pages:</span>
                      <span className="stat-value">{competitor.pagesCrawled}</span>
                    </div>
                    <div className="stat-item">
                      <span className="stat-label">Total URLs:</span>
                      <span className="stat-value">{competitor.totalUrls}</span>
                    </div>
                    <div className="stat-item">
                      <span className="stat-label">Last crawl:</span>
                      <span className="stat-value">{formatTimeAgo(competitor.crawlDateTime)}</span>
                    </div>
                    <div className="stat-item">
                      <span className="stat-label">Duration:</span>
                      <span className="stat-value">{formatDuration(competitor.crawlDuration)}</span>
                    </div>
                  </div>
                )}

                <button
                  className="view-competitor-button"
                  onClick={() => onCompetitorClick(competitor)}
                >
                  View Results
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export default Competitors;
