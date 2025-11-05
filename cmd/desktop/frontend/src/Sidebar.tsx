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

import { useState } from 'react';
import './Sidebar.css';

interface SidebarProps {
  activeSection: 'crawl-results' | 'config' | 'ai-crawlers' | 'competitors';
  onSectionChange: (section: 'crawl-results' | 'config' | 'ai-crawlers' | 'competitors') => void;
  onHomeClick: () => void;
}

function Sidebar({ activeSection, onSectionChange, onHomeClick }: SidebarProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  const toggleSidebar = () => {
    setIsExpanded(!isExpanded);
  };

  return (
    <div className={`sidebar ${isExpanded ? 'expanded' : 'collapsed'}`}>
      <div className="sidebar-header">
        <button className="sidebar-toggle-button" onClick={toggleSidebar} title={isExpanded ? "Collapse sidebar" : "Expand sidebar"}>
          <svg className={`toggle-arrow ${isExpanded ? 'expanded' : 'collapsed'}`} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="9 18 15 12 9 6"></polyline>
          </svg>
        </button>
      </div>

      <div className="sidebar-home-button-container">
        <button
          className="sidebar-home-button"
          onClick={onHomeClick}
          title={isExpanded ? '' : 'Home'}
        >
          <div className="sidebar-nav-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"></path>
              <polyline points="9 22 9 12 15 12 15 22"></polyline>
            </svg>
          </div>
          {isExpanded && <span className="sidebar-nav-label">Home</span>}
          {!isExpanded && <div className="sidebar-tooltip">Home</div>}
        </button>
      </div>

      <nav className="sidebar-nav">
        <div
          className={`sidebar-nav-item ${activeSection === 'crawl-results' ? 'active' : ''}`}
          onClick={() => onSectionChange('crawl-results')}
          title={isExpanded ? '' : 'Crawl Results'}
        >
          <div className="sidebar-nav-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M8 6h13"></path>
              <path d="M8 12h13"></path>
              <path d="M8 18h13"></path>
              <path d="M3 6h.01"></path>
              <path d="M3 12h.01"></path>
              <path d="M3 18h.01"></path>
            </svg>
          </div>
          {isExpanded && <span className="sidebar-nav-label">Crawl Results</span>}
          {!isExpanded && <div className="sidebar-tooltip">Crawl Results</div>}
        </div>

        <div
          className={`sidebar-nav-item ${activeSection === 'ai-crawlers' ? 'active' : ''}`}
          onClick={() => onSectionChange('ai-crawlers')}
          title={isExpanded ? '' : 'AI Crawlers'}
        >
          <div className="sidebar-nav-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="11" width="18" height="10" rx="2"></rect>
              <circle cx="12" cy="5" r="2"></circle>
              <path d="M12 7v4"></path>
              <line x1="8" y1="16" x2="8" y2="16"></line>
              <line x1="16" y1="16" x2="16" y2="16"></line>
            </svg>
          </div>
          {isExpanded && <span className="sidebar-nav-label">AI Crawlers</span>}
          {!isExpanded && <div className="sidebar-tooltip">AI Crawlers</div>}
        </div>

        <div
          className={`sidebar-nav-item ${activeSection === 'competitors' ? 'active' : ''}`}
          onClick={() => onSectionChange('competitors')}
          title={isExpanded ? '' : 'Competitors'}
        >
          <div className="sidebar-nav-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10"></circle>
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"></path>
              <path d="M2 12h20"></path>
            </svg>
          </div>
          {isExpanded && <span className="sidebar-nav-label">Competitors</span>}
          {!isExpanded && <div className="sidebar-tooltip">Competitors</div>}
        </div>

        <div
          className={`sidebar-nav-item ${activeSection === 'config' ? 'active' : ''}`}
          onClick={() => onSectionChange('config')}
          title={isExpanded ? '' : 'Configuration'}
        >
          <div className="sidebar-nav-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <line x1="4" y1="21" x2="4" y2="14"></line>
              <line x1="4" y1="10" x2="4" y2="3"></line>
              <line x1="12" y1="21" x2="12" y2="12"></line>
              <line x1="12" y1="8" x2="12" y2="3"></line>
              <line x1="20" y1="21" x2="20" y2="16"></line>
              <line x1="20" y1="12" x2="20" y2="3"></line>
              <line x1="1" y1="14" x2="7" y2="14"></line>
              <line x1="9" y1="8" x2="15" y2="8"></line>
              <line x1="17" y1="16" x2="23" y2="16"></line>
            </svg>
          </div>
          {isExpanded && <span className="sidebar-nav-label">Configuration</span>}
          {!isExpanded && <div className="sidebar-tooltip">Configuration</div>}
        </div>
      </nav>
    </div>
  );
}

export default Sidebar;
