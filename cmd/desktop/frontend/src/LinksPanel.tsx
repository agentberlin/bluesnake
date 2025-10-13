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
import { BrowserOpenURL } from "../wailsjs/runtime/runtime";
import { GetPageContent } from "../wailsjs/go/main/DesktopApp";
import './LinksPanel.css';

interface Link {
  url: string;
  linkType: string;  // "anchor", "image", "script", "stylesheet", etc.
  anchorText: string;
  context?: string;
  isInternal: boolean;
  status?: number;
  position?: string;
  domPath?: string;
  urlAction: string;  // "crawl", "record", "skip"
}

interface LinksPanelProps {
  isOpen: boolean;
  onClose: () => void;
  selectedUrl: string;
  inlinks: Link[];
  outlinks: Link[];
  crawlId?: number;  // Add crawlId for fetching content
}

type TabType = 'inlinks' | 'outlinks' | 'content';
type OutlinkSubTab = 'all' | 'pages' | 'images' | 'scripts' | 'styles' | 'other';

const DEFAULT_WIDTH = 480;
const MIN_WIDTH = 400;
const MAX_WIDTH_VW = 90;

function LinksPanel({ isOpen, onClose, selectedUrl, inlinks, outlinks, crawlId }: LinksPanelProps) {
  const [activeTab, setActiveTab] = useState<TabType>('inlinks');
  const [outlinkSubTab, setOutlinkSubTab] = useState<OutlinkSubTab>('all');
  const [showContentOnly, setShowContentOnly] = useState<boolean>(() => {
    // Load saved filter preference from localStorage, default to true (content only)
    const savedFilter = localStorage.getItem('linksPanelShowContentOnly');
    return savedFilter !== null ? savedFilter === 'true' : true;
  });
  const [panelWidth, setPanelWidth] = useState<number>(() => {
    // Load saved width from localStorage or use default
    const savedWidth = localStorage.getItem('linksPanelWidth');
    return savedWidth ? parseInt(savedWidth, 10) : DEFAULT_WIDTH;
  });
  const [isResizing, setIsResizing] = useState(false);

  // Content tab state
  const [content, setContent] = useState<string>('');
  const [isLoadingContent, setIsLoadingContent] = useState(false);
  const [contentError, setContentError] = useState<string>('');

  // Handle Escape key to close panel
  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };

    document.addEventListener('keydown', handleEscape);
    return () => document.removeEventListener('keydown', handleEscape);
  }, [isOpen, onClose]);

  // Handle resize
  useEffect(() => {
    if (!isResizing) return;

    const handleMouseMove = (e: MouseEvent) => {
      const maxWidth = (window.innerWidth * MAX_WIDTH_VW) / 100;
      const newWidth = window.innerWidth - e.clientX;
      const constrainedWidth = Math.min(Math.max(newWidth, MIN_WIDTH), maxWidth);
      setPanelWidth(constrainedWidth);
    };

    const handleMouseUp = () => {
      setIsResizing(false);
      // Save to localStorage
      localStorage.setItem('linksPanelWidth', panelWidth.toString());
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isResizing, panelWidth]);

  const handleResizeStart = (e: React.MouseEvent) => {
    e.preventDefault();
    setIsResizing(true);
  };

  // Save filter preference when it changes
  useEffect(() => {
    localStorage.setItem('linksPanelShowContentOnly', showContentOnly.toString());
  }, [showContentOnly]);

  // Fetch content when Content tab is selected
  useEffect(() => {
    if (activeTab === 'content' && crawlId && selectedUrl && isOpen) {
      setIsLoadingContent(true);
      setContentError('');

      GetPageContent(crawlId, selectedUrl)
        .then((fetchedContent: string) => {
          setContent(fetchedContent);
          setIsLoadingContent(false);
        })
        .catch((error: any) => {
          console.error('Failed to load content:', error);
          setContentError(error?.toString() || 'Failed to load content');
          setIsLoadingContent(false);
        });
    }
  }, [activeTab, crawlId, selectedUrl, isOpen]);

  // Copy to clipboard handler
  const handleCopyToClipboard = () => {
    if (content) {
      navigator.clipboard.writeText(content)
        .then(() => {
          // Could add a toast notification here
          console.log('Content copied to clipboard');
        })
        .catch((err) => {
          console.error('Failed to copy content:', err);
        });
    }
  };

  if (!isOpen) return null;

  // Categorize outlinks by type (include all outlinks regardless of urlAction)
  const outlinksByType = {
    all: outlinks,
    pages: outlinks.filter(link => link.linkType === 'anchor'),
    images: outlinks.filter(link => link.linkType === 'image'),
    scripts: outlinks.filter(link => link.linkType === 'script' || link.linkType === 'modulepreload'),
    styles: outlinks.filter(link => link.linkType === 'stylesheet'),
    other: outlinks.filter(link =>
      !['anchor', 'image', 'script', 'modulepreload', 'stylesheet'].includes(link.linkType)
    )
  };

  // Determine which links to show based on active tab
  let allCurrentLinks: Link[];
  if (activeTab === 'inlinks') {
    allCurrentLinks = inlinks;
  } else {
    allCurrentLinks = outlinksByType[outlinkSubTab];
  }

  // Filter links based on showContentOnly toggle (only for inlinks and anchor outlinks)
  const currentLinks = showContentOnly && (activeTab === 'inlinks' || outlinkSubTab === 'pages')
    ? allCurrentLinks.filter(link => link.position === 'content')
    : allCurrentLinks;

  // Sort links: internal links first, then external links
  const sortedLinks = [...currentLinks].sort((a, b) => {
    if (a.isInternal && !b.isInternal) return -1;
    if (!a.isInternal && b.isInternal) return 1;
    return 0;
  });

  return (
    <>
      {/* Backdrop */}
      <div className="links-panel-backdrop" onClick={onClose} />

      {/* Panel */}
      <div className="links-panel" style={{ width: `${panelWidth}px` }}>
        {/* Resize Handle */}
        <div
          className={`resize-handle ${isResizing ? 'resizing' : ''}`}
          onMouseDown={handleResizeStart}
        />
        {/* Header */}
        <div className="links-panel-header">
          <div className="links-panel-header-content">
            <h3 className="links-panel-title">Internal Links</h3>
            <button className="links-panel-close" onClick={onClose} title="Close">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="6" x2="6" y2="18"></line>
                <line x1="6" y1="6" x2="18" y2="18"></line>
              </svg>
            </button>
          </div>
          <div className="links-panel-url">{selectedUrl}</div>

          {/* Filter Toggle */}
          <div className="links-panel-filter">
            <label className="filter-toggle-label">
              <input
                type="checkbox"
                checked={showContentOnly}
                onChange={(e) => setShowContentOnly(e.target.checked)}
                className="filter-toggle-checkbox"
              />
              <span className="filter-toggle-switch"></span>
              <span className="filter-toggle-text">Show only content links</span>
            </label>
          </div>
        </div>

        {/* Tabs */}
        <div className="links-panel-tabs">
          <button
            className={`links-panel-tab ${activeTab === 'inlinks' ? 'active' : ''}`}
            onClick={() => setActiveTab('inlinks')}
          >
            Inlinks ({inlinks.length})
          </button>
          <button
            className={`links-panel-tab ${activeTab === 'outlinks' ? 'active' : ''}`}
            onClick={() => setActiveTab('outlinks')}
          >
            Outlinks ({outlinks.length})
          </button>
          <button
            className={`links-panel-tab ${activeTab === 'content' ? 'active' : ''}`}
            onClick={() => setActiveTab('content')}
          >
            Content
          </button>
        </div>

        {/* Outlink Sub-tabs */}
        {activeTab === 'outlinks' && (
          <div className="links-panel-subtabs">
            <button
              className={`links-panel-subtab ${outlinkSubTab === 'all' ? 'active' : ''}`}
              onClick={() => setOutlinkSubTab('all')}
            >
              All ({outlinksByType.all.length})
            </button>
            <button
              className={`links-panel-subtab ${outlinkSubTab === 'pages' ? 'active' : ''}`}
              onClick={() => setOutlinkSubTab('pages')}
            >
              Pages ({outlinksByType.pages.length})
            </button>
            <button
              className={`links-panel-subtab ${outlinkSubTab === 'images' ? 'active' : ''}`}
              onClick={() => setOutlinkSubTab('images')}
            >
              Images ({outlinksByType.images.length})
            </button>
            <button
              className={`links-panel-subtab ${outlinkSubTab === 'scripts' ? 'active' : ''}`}
              onClick={() => setOutlinkSubTab('scripts')}
            >
              Scripts ({outlinksByType.scripts.length})
            </button>
            <button
              className={`links-panel-subtab ${outlinkSubTab === 'styles' ? 'active' : ''}`}
              onClick={() => setOutlinkSubTab('styles')}
            >
              Styles ({outlinksByType.styles.length})
            </button>
            <button
              className={`links-panel-subtab ${outlinkSubTab === 'other' ? 'active' : ''}`}
              onClick={() => setOutlinkSubTab('other')}
            >
              Other ({outlinksByType.other.length})
            </button>
          </div>
        )}

        {/* Content */}
        <div className="links-panel-content">
          {activeTab === 'content' ? (
            // Content tab display
            <div className="content-tab-container">
              {isLoadingContent ? (
                <div className="content-loading">
                  <p>Loading content...</p>
                </div>
              ) : contentError ? (
                <div className="content-error">
                  <p>Error: {contentError}</p>
                </div>
              ) : (
                <>
                  <div className="content-header">
                    <button
                      className="copy-button"
                      onClick={handleCopyToClipboard}
                      title="Copy to clipboard"
                    >
                      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                      </svg>
                      Copy
                    </button>
                    <div className="content-stats">
                      {content.length > 0 && (
                        <>
                          <span>{content.split(/\s+/).filter(w => w.length > 0).length} words</span>
                          <span className="content-separator">•</span>
                          <span>{content.length} characters</span>
                        </>
                      )}
                    </div>
                  </div>
                  <div className="content-display">
                    {content || 'No content available for this page.'}
                  </div>
                </>
              )}
            </div>
          ) : currentLinks.length === 0 ? (
            <div className="links-panel-empty">
              <p>
                {activeTab === 'inlinks' && 'No inlinks found'}
                {activeTab === 'outlinks' && 'No outlinks found'}
              </p>
            </div>
          ) : (
            <>
              {/* Legend */}
              <div className="links-legend">
                <div className="legend-item">
                  <div className="legend-color legend-internal"></div>
                  <span className="legend-label">Internal</span>
                </div>
                <div className="legend-item">
                  <div className="legend-color legend-external"></div>
                  <span className="legend-label">External</span>
                </div>
              </div>
              <div className="links-table">
              <div className="links-table-header">
                <div className="links-header-cell url-column">URL</div>
                <div className="links-header-cell type-column">Type</div>
                <div className="links-header-cell anchor-column">Anchor Text</div>
                <div className="links-header-cell position-column">Position</div>
                <div className="links-header-cell status-column">Status</div>
              </div>
              <div className="links-table-body">
                {sortedLinks.map((link, index) => (
                  <div key={index} className={`link-row ${link.isInternal ? 'link-internal' : 'link-external'}`}>
                    <div
                      className="link-cell url-column clickable"
                      title={link.url}
                      onClick={() => BrowserOpenURL(link.url)}
                    >
                      {link.url}
                    </div>
                    <div className="link-cell type-column">
                      <span className="link-type-badge">
                        {link.linkType || 'unknown'}
                      </span>
                    </div>
                    <div className="link-cell anchor-column" title={link.anchorText}>
                      {link.anchorText || '-'}
                    </div>
                    <div className="link-cell position-column" title={link.domPath || ''}>
                      {link.position && (
                        <span className={`position-badge position-${link.position === 'content' ? 'content' : 'boilerplate'}`}>
                          {link.position}
                        </span>
                      )}
                    </div>
                    <div className="link-cell status-column">
                      {link.status ? (
                        <span className={`status-badge status-${Math.floor(link.status / 100)}`}>
                          {link.status}
                        </span>
                      ) : link.urlAction === 'record' ? (
                        <span className="status-badge status-not-crawled">
                          Not crawled
                        </span>
                      ) : null}
                    </div>
                  </div>
                ))}
              </div>
            </div>
            </>
          )}
        </div>

        {/* Footer with stats */}
        {activeTab !== 'content' && (
          <div className="links-panel-footer">
            <div className="links-panel-stats">
              <span className="stat-item">
                <span className="stat-label">
                  {activeTab === 'inlinks' && 'Total Inlinks:'}
                  {activeTab === 'outlinks' && `${outlinkSubTab.charAt(0).toUpperCase() + outlinkSubTab.slice(1)}:`}
                </span>
                <span className="stat-value">{currentLinks.length}</span>
              </span>
            </div>
          </div>
        )}
      </div>
    </>
  );
}

export default LinksPanel;
