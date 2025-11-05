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
import { Button, Input, Modal, ModalContent, ModalActions, Card, CardHeader, CardContent, CardFooter, Spinner, Badge, Icon } from './design-system';

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
    <div className="competitors-page">
      {/* Header */}
      <div className="competitors-page-header">
        <h2 className="competitors-page-title">Competitors</h2>
        <Button variant="primary" size="medium" onClick={() => setIsAddingCompetitor(true)}>
          Add Competitor
        </Button>
      </div>

      {/* Overview Statistics */}
      {stats && (
        <div className="competitors-stats-grid">
          <Card variant="default">
            <CardContent>
              <div className="stat-display">
                <div className="stat-label">Total Competitors</div>
                <div className="stat-value">{stats.totalCompetitors}</div>
              </div>
            </CardContent>
          </Card>
          <Card variant="default">
            <CardContent>
              <div className="stat-display">
                <div className="stat-label">Total Pages</div>
                <div className="stat-value">{stats.totalPages.toLocaleString()}</div>
              </div>
            </CardContent>
          </Card>
          <Card variant="default">
            <CardContent>
              <div className="stat-display">
                <div className="stat-label">Last Crawl</div>
                <div className="stat-value">{formatTimeAgo(stats.lastCrawlTime)}</div>
              </div>
            </CardContent>
          </Card>
          <Card variant="default">
            <CardContent>
              <div className="stat-display">
                <div className="stat-label">Active Crawls</div>
                <div className="stat-value">{stats.activeCrawls}</div>
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      {/* Add Competitor Modal */}
      <Modal
        isOpen={isAddingCompetitor}
        onClose={() => setIsAddingCompetitor(false)}
        title="Add Competitor"
        size="small"
      >
        <ModalContent>
          <Input
            type="text"
            placeholder="https://example.com"
            value={newCompetitorUrl}
            onChange={(e) => setNewCompetitorUrl(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleAddCompetitor()}
            error={error || undefined}
            autoFocus
          />
        </ModalContent>
        <ModalActions>
          <Button
            variant="secondary"
            onClick={() => setIsAddingCompetitor(false)}
            disabled={loading}
          >
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={handleAddCompetitor}
            loading={loading}
          >
            Start Crawl
          </Button>
        </ModalActions>
      </Modal>

      {/* Competitors List */}
      <div className="competitors-section">
        <h3 className="competitors-section-title">Competitor Domains</h3>
        {competitors.length === 0 ? (
          <div className="competitors-empty-state">
            <Icon name="globe" size={48} />
            <h4>No Competitors Yet</h4>
            <p>Add competitor domains to track and analyze their websites.</p>
          </div>
        ) : (
          <div className="competitors-grid">
            {competitors.map((competitor) => (
              <Card key={competitor.id} variant="default" hoverable>
                <CardHeader>
                  <div className="competitor-card-header">
                    <div className="competitor-header-left">
                      {competitor.faviconPath && faviconCache.has(competitor.faviconPath) ? (
                        <img
                          src={faviconCache.get(competitor.faviconPath)}
                          alt="Favicon"
                          className="competitor-favicon"
                        />
                      ) : (
                        <div className="competitor-favicon-placeholder">
                          <Icon name="globe" size={16} />
                        </div>
                      )}
                      <div className="competitor-domain-name">{competitor.domain}</div>
                    </div>
                    <Button
                      variant="ghost"
                      size="small"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleDeleteCompetitor(competitor.id);
                      }}
                      icon={<Icon name="x" size={14} />}
                    />
                  </div>
                </CardHeader>

                <CardContent>
                  {competitor.isCrawling ? (
                    <div className="competitor-crawling-state">
                      <div className="crawling-indicator">
                        <Spinner size="small" />
                        <span className="crawling-text">Crawling...</span>
                      </div>
                      <Button
                        variant="secondary"
                        size="small"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleStopCrawl(competitor.id);
                        }}
                      >
                        Stop
                      </Button>
                    </div>
                  ) : (
                    <div className="competitor-stats-list">
                      <div className="stat-row">
                        <span className="stat-row-label">Pages:</span>
                        <span className="stat-row-value">{competitor.pagesCrawled}</span>
                      </div>
                      <div className="stat-row">
                        <span className="stat-row-label">Total URLs:</span>
                        <span className="stat-row-value">{competitor.totalUrls}</span>
                      </div>
                      <div className="stat-row">
                        <span className="stat-row-label">Last crawl:</span>
                        <span className="stat-row-value">{formatTimeAgo(competitor.crawlDateTime)}</span>
                      </div>
                      <div className="stat-row">
                        <span className="stat-row-label">Duration:</span>
                        <span className="stat-row-value">{formatDuration(competitor.crawlDuration)}</span>
                      </div>
                    </div>
                  )}
                </CardContent>

                <CardFooter>
                  <Button
                    variant="secondary"
                    size="medium"
                    onClick={() => onCompetitorClick(competitor)}
                    style={{ width: '100%' }}
                  >
                    View Results
                  </Button>
                </CardFooter>
              </Card>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export default Competitors;
