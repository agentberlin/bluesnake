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
import './LinksPanel.css';

interface Link {
  url: string;
  anchorText: string;
  status?: number;
  position?: string;
  domPath?: string;
}

interface LinksPanelProps {
  isOpen: boolean;
  onClose: () => void;
  selectedUrl: string;
  inlinks: Link[];
  outlinks: Link[];
}

type TabType = 'inlinks' | 'outlinks';

const DEFAULT_WIDTH = 480;
const MIN_WIDTH = 400;
const MAX_WIDTH_VW = 90;

function LinksPanel({ isOpen, onClose, selectedUrl, inlinks, outlinks }: LinksPanelProps) {
  const [activeTab, setActiveTab] = useState<TabType>('inlinks');
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

  if (!isOpen) return null;

  const allCurrentLinks = activeTab === 'inlinks' ? inlinks : outlinks;
  // Filter links based on showContentOnly toggle
  const currentLinks = showContentOnly
    ? allCurrentLinks.filter(link => link.position === 'content')
    : allCurrentLinks;

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
        </div>

        {/* Content */}
        <div className="links-panel-content">
          {currentLinks.length === 0 ? (
            <div className="links-panel-empty">
              <p>No {activeTab === 'inlinks' ? 'inlinks' : 'outlinks'} found</p>
            </div>
          ) : (
            <div className="links-table">
              <div className="links-table-header">
                <div className="links-header-cell url-column">URL</div>
                <div className="links-header-cell anchor-column">Anchor Text</div>
                <div className="links-header-cell position-column">Position</div>
                <div className="links-header-cell status-column">Status</div>
              </div>
              <div className="links-table-body">
                {currentLinks.map((link, index) => (
                  <div key={index} className="link-row">
                    <div
                      className="link-cell url-column clickable"
                      title={link.url}
                      onClick={() => BrowserOpenURL(link.url)}
                    >
                      {link.url}
                    </div>
                    <div className="link-cell anchor-column" title={link.anchorText}>
                      {link.anchorText}
                    </div>
                    <div className="link-cell position-column" title={link.domPath || ''}>
                      {link.position && (
                        <span className={`position-badge position-${link.position === 'content' ? 'content' : 'boilerplate'}`}>
                          {link.position}
                        </span>
                      )}
                    </div>
                    <div className="link-cell status-column">
                      {link.status && (
                        <span className={`status-badge status-${Math.floor(link.status / 100)}`}>
                          {link.status}
                        </span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Footer with stats */}
        <div className="links-panel-footer">
          <div className="links-panel-stats">
            <span className="stat-item">
              <span className="stat-label">Total {activeTab === 'inlinks' ? 'Inlinks' : 'Outlinks'}:</span>
              <span className="stat-value">{currentLinks.length}</span>
            </span>
          </div>
        </div>
      </div>
    </>
  );
}

export default LinksPanel;
