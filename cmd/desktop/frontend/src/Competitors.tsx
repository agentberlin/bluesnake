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
  StartCompetitorCrawl,
  DeleteCompetitor,
  StopCrawl,
  GetFaviconData,
} from '../wailsjs/go/main/DesktopApp';
import { Button, Input, Modal, ModalContent, ModalActions, Card, CardHeader, CardContent, CardFooter, Spinner, Icon } from './design-system';

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

interface ProjectInfo {
  id: number;
  url: string;
  domain: string;
  faviconPath: string;
}

interface CompetitorsProps {
  onCompetitorClick: (competitor: CompetitorInfo) => void;
  projects: ProjectInfo[];
}

function Competitors({ onCompetitorClick, projects }: CompetitorsProps) {
  const [competitors, setCompetitors] = useState<CompetitorInfo[]>([]);
  const [isAddingCompetitor, setIsAddingCompetitor] = useState(false);
  const [newCompetitorUrl, setNewCompetitorUrl] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [faviconCache, setFaviconCache] = useState<Map<string, string>>(new Map());
  const [recrawlingAll, setRecrawlingAll] = useState(false);
  const [selectedProjectId, setSelectedProjectId] = useState<number | null>(null);

  // Set initial project when projects load
  useEffect(() => {
    if (projects.length > 0 && selectedProjectId === null) {
      setSelectedProjectId(projects[0].id);
    }
  }, [projects]);

  // Load competitors when selected project changes
  useEffect(() => {
    if (selectedProjectId !== null) {
      loadData();
      const interval = setInterval(loadData, 2000); // Poll every 2 seconds
      return () => clearInterval(interval);
    }
  }, [selectedProjectId]);

  const loadData = async () => {
    if (selectedProjectId === null) return;

    try {
      const competitorsData = await GetCompetitors(selectedProjectId);
      setCompetitors(competitorsData || []);

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

    if (selectedProjectId === null) {
      setError('Please select a parent project');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      await StartCompetitorCrawl(newCompetitorUrl, selectedProjectId);
      setNewCompetitorUrl('');
      setIsAddingCompetitor(false);
      loadData();
    } catch (err: any) {
      setError(err?.message || 'Failed to add competitor');
    } finally {
      setLoading(false);
    }
  };

  const handleDeleteCompetitor = async (competitorId: number, event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();

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

  const handleStopCrawl = async (competitorId: number, event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();

    try {
      await StopCrawl(competitorId);
      loadData();
    } catch (err) {
      console.error('Failed to stop crawl:', err);
    }
  };

  const handleRecrawlCompetitor = async (competitorUrl: string, event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();

    if (selectedProjectId === null) return;

    try {
      await StartCompetitorCrawl(competitorUrl, selectedProjectId);
      loadData();
    } catch (err) {
      console.error('Failed to re-crawl competitor:', err);
      alert('Failed to start re-crawl');
    }
  };

  const handleRecrawlAll = async () => {
    if (competitors.length === 0 || selectedProjectId === null) return;

    if (!confirm(`Start re-crawl for all ${competitors.length} competitors?`)) {
      return;
    }

    setRecrawlingAll(true);

    try {
      // Start crawls for all competitors sequentially
      for (const competitor of competitors) {
        if (!competitor.isCrawling) {
          try {
            await StartCompetitorCrawl(competitor.url, selectedProjectId);
            // Small delay between starting crawls
            await new Promise(resolve => setTimeout(resolve, 500));
          } catch (err) {
            console.error(`Failed to start crawl for ${competitor.domain}:`, err);
          }
        }
      }
      loadData();
    } finally {
      setRecrawlingAll(false);
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
      <div className="competitors-content">
        {/* Header with Actions */}
        <div className="competitors-header">
          <div className="competitors-header-left">
            <h2 className="competitors-title">Competitors</h2>
            <div className="competitors-subtitle">
              Track and analyze competitor websites
            </div>
            {projects.length > 0 && (
              <div className="competitors-project-selector">
                <label htmlFor="project-select">For project:</label>
                <select
                  id="project-select"
                  value={selectedProjectId || ''}
                  onChange={(e) => setSelectedProjectId(Number(e.target.value))}
                  className="project-select"
                >
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.domain}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>
          <div className="competitors-header-actions">
            <Button
              variant="secondary"
              size="medium"
              onClick={handleRecrawlAll}
              disabled={competitors.length === 0 || recrawlingAll || selectedProjectId === null}
              loading={recrawlingAll}
            >
              Re-crawl All
            </Button>
            <Button
              variant="primary"
              size="medium"
              onClick={() => setIsAddingCompetitor(true)}
              disabled={selectedProjectId === null}
            >
              Add Competitor
            </Button>
          </div>
        </div>

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

        {/* Competitors Grid */}
        {projects.length === 0 ? (
          <div className="competitors-empty-state">
            <Icon name="globe" size={48} />
            <h3>No Projects Available</h3>
            <p>You need to create a project first before adding competitors.</p>
          </div>
        ) : selectedProjectId === null ? (
          <div className="competitors-empty-state">
            <Icon name="globe" size={48} />
            <h3>Select a Project</h3>
            <p>Choose a project above to view and manage its competitors.</p>
          </div>
        ) : competitors.length === 0 ? (
          <div className="competitors-empty-state">
            <Icon name="globe" size={48} />
            <h3>No Competitors Yet</h3>
            <p>Add competitor domains to track and analyze their websites.</p>
            <Button variant="secondary" size="medium" onClick={() => setIsAddingCompetitor(true)} style={{ marginTop: '16px' }}>
              Add Your First Competitor
            </Button>
          </div>
        ) : (
          <div className="competitors-grid">
            {competitors.map((competitor) => (
              <Card key={competitor.id} variant="default">
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
                    <button
                      className="competitor-delete-button"
                      onClick={(e) => handleDeleteCompetitor(competitor.id, e)}
                      title="Delete competitor"
                    >
                      <Icon name="x" size={14} />
                    </button>
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
                        onClick={(e) => handleStopCrawl(competitor.id, e)}
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
                  <div className="competitor-card-actions">
                    {!competitor.isCrawling && (
                      <Button
                        variant="ghost"
                        size="small"
                        onClick={(e) => handleRecrawlCompetitor(competitor.url, e)}
                        icon={<Icon name="arrow-right" size={14} />}
                      >
                        Re-crawl
                      </Button>
                    )}
                    <Button
                      variant="secondary"
                      size="small"
                      onClick={() => onCompetitorClick(competitor)}
                    >
                      View Results
                    </Button>
                  </div>
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
