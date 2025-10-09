import { useState, useEffect, useRef } from 'react';
import './App.css';
import { StartCrawl, GetProjects, GetCrawls, GetCrawlWithResults, DeleteCrawlByID, DeleteProjectByID } from "../wailsjs/go/main/App";
import { EventsOn, BrowserOpenURL } from "../wailsjs/runtime/runtime";
import logo from './assets/images/bluesnake-logo.png';
import Config from './Config';

interface CustomDropdownProps {
  value: number;
  options: CrawlInfo[];
  onChange: (crawlId: number) => void;
  disabled?: boolean;
  formatOption: (crawl: CrawlInfo) => string;
}

function CustomDropdown({ value, options, onChange, disabled, formatOption }: CustomDropdownProps) {
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

  const selectedOption = options.find(opt => opt.id === value);

  return (
    <div className={`custom-dropdown ${disabled ? 'disabled' : ''}`} ref={dropdownRef}>
      <div
        className={`custom-dropdown-header ${isOpen ? 'open' : ''}`}
        onClick={() => !disabled && setIsOpen(!isOpen)}
      >
        <span className="custom-dropdown-value">
          {selectedOption ? formatOption(selectedOption) : 'Select crawl'}
        </span>
        <svg className="custom-dropdown-arrow" width="12" height="8" viewBox="0 0 12 8" fill="none">
          <path d="M1 1.5L6 6.5L11 1.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </div>
      {isOpen && !disabled && (
        <div className="custom-dropdown-menu">
          {options.map((option) => (
            <div
              key={option.id}
              className={`custom-dropdown-option ${option.id === value ? 'selected' : ''}`}
              onClick={() => {
                onChange(option.id);
                setIsOpen(false);
              }}
            >
              {formatOption(option)}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

interface CrawlResult {
  url: string;
  status: number;
  title: string;
  indexable: string;
  error?: string;
}

type CrawlStatus = 'discovered' | 'crawling' | 'completed';

interface UrlStatus {
  url: string;
  status: CrawlStatus;
  result?: CrawlResult;
}

interface ProjectInfo {
  id: number;
  url: string;
  domain: string;
  crawlDateTime: number;
  crawlDuration: number;
  pagesCrawled: number;
  latestCrawlId: number;
}

interface CrawlInfo {
  id: number;
  projectId: number;
  crawlDateTime: number;
  crawlDuration: number;
  pagesCrawled: number;
}



type View = 'start' | 'crawl' | 'config';

interface CircularProgressProps {
  crawled: number;
  total: number;
}

function CircularProgress({ crawled, total }: CircularProgressProps) {
  const percentage = total > 0 ? (crawled / total) * 100 : 0;
  const radius = 8;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (percentage / 100) * circumference;

  return (
    <div className="circular-progress">
      <svg width="20" height="20" viewBox="0 0 20 20" className="progress-ring">
        <circle
          className="progress-ring-circle-bg"
          cx="10"
          cy="10"
          r={radius}
          strokeWidth="2"
          fill="none"
        />
        <circle
          className="progress-ring-circle"
          cx="10"
          cy="10"
          r={radius}
          strokeWidth="2"
          fill="none"
          strokeDasharray={circumference}
          strokeDashoffset={strokeDashoffset}
          transform="rotate(-90 10 10)"
        />
      </svg>
      <span className="progress-text">
        {crawled} / {total}
      </span>
    </div>
  );
}

function SmallLoadingSpinner() {
  return (
    <div className="small-loading-spinner">
      <svg width="16" height="16" viewBox="0 0 16 16" className="spinner-svg">
        <circle
          className="spinner-circle"
          cx="8"
          cy="8"
          r="6"
          strokeWidth="2"
          fill="none"
        />
      </svg>
    </div>
  );
}

function App() {
  const [url, setUrl] = useState('');
  const [isCrawling, setIsCrawling] = useState(false);
  const [results, setResults] = useState<CrawlResult[]>([]);
  const [currentUrl, setCurrentUrl] = useState('');
  const [view, setView] = useState<View>('start');
  const [hasStarted, setHasStarted] = useState(false);
  const [urlStatuses, setUrlStatuses] = useState<Map<string, UrlStatus>>(new Map());
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const [currentProject, setCurrentProject] = useState<ProjectInfo | null>(null);
  const [availableCrawls, setAvailableCrawls] = useState<CrawlInfo[]>([]);
  const [selectedCrawl, setSelectedCrawl] = useState<CrawlInfo | null>(null);
  const [showDeleteCrawlModal, setShowDeleteCrawlModal] = useState(false);
  const [showDeleteProjectModal, setShowDeleteProjectModal] = useState(false);
  const [projectToDelete, setProjectToDelete] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Load projects on start
    loadProjects();

    // Listen for crawl events
    EventsOn("crawl:started", (data: any) => {
      setIsCrawling(true);
      setView('crawl');
      setResults([]);
      setUrlStatuses(new Map());
    });

    EventsOn("crawl:request", (data: any) => {
      setCurrentUrl(data.url);
      setUrlStatuses(prev => {
        const newMap = new Map(prev);
        newMap.set(data.url, {
          url: data.url,
          status: 'crawling'
        });
        return newMap;
      });
    });

    EventsOn("crawl:result", (result: CrawlResult) => {
      setResults(prev => {
        // Check if URL already exists in results
        const existingIndex = prev.findIndex(r => r.url === result.url);
        if (existingIndex !== -1) {
          // Replace existing result
          const newResults = [...prev];
          newResults[existingIndex] = result;
          return newResults;
        }
        // Add new result
        return [...prev, result];
      });

      setUrlStatuses(prev => {
        const newMap = new Map(prev);
        newMap.set(result.url, {
          url: result.url,
          status: 'completed',
          result: result
        });
        return newMap;
      });
    });

    EventsOn("crawl:error", (result: CrawlResult) => {
      setResults(prev => {
        // Check if URL already exists in results
        const existingIndex = prev.findIndex(r => r.url === result.url);
        if (existingIndex !== -1) {
          // Replace existing result
          const newResults = [...prev];
          newResults[existingIndex] = result;
          return newResults;
        }
        // Add new result
        return [...prev, result];
      });

      setUrlStatuses(prev => {
        const newMap = new Map(prev);
        newMap.set(result.url, {
          url: result.url,
          status: 'completed',
          result: result
        });
        return newMap;
      });
    });

    EventsOn("crawl:completed", () => {
      setIsCrawling(false);
      setCurrentUrl('');
      // Reload projects after crawl completes
      loadProjects();
    });
  }, []);

  const loadProjects = async () => {
    try {
      const projectList = await GetProjects();
      setProjects(projectList || []);
    } catch (error) {
      console.error('Failed to load projects:', error);
    }
  };

  const handleStartCrawl = async () => {
    if (!url.trim()) return;

    try {
      await StartCrawl(url);
    } catch (error) {
      console.error('Failed to start crawl:', error);
      setIsCrawling(false);
    }
  };

  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      handleStartCrawl();
    }
  };

  const handleHome = () => {
    setView('start');
    setResults([]);
    setUrl('');
    setIsCrawling(false);
    setCurrentUrl('');
    setUrlStatuses(new Map());
  };

  const handleNewCrawl = async () => {
    if (!url.trim()) return;

    try {
      await StartCrawl(url);
    } catch (error) {
      console.error('Failed to start crawl:', error);
      setIsCrawling(false);
    }
  };

  const handleOpenConfig = () => {
    setView('config');
  };

  const handleCloseConfig = () => {
    setView('crawl');
  };

  const handleProjectClick = async (project: ProjectInfo) => {
    setCurrentProject(project);
    setUrl(project.url);

    // Load all crawls for this project
    try {
      const crawls = await GetCrawls(project.id);
      setAvailableCrawls(crawls);

      // Load the latest crawl results
      if (project.latestCrawlId) {
        const crawlData = await GetCrawlWithResults(project.latestCrawlId);
        setSelectedCrawl(crawlData.crawlInfo);
        setResults(crawlData.results);

        // Populate urlStatuses from the results
        const newUrlStatuses = new Map<string, UrlStatus>();
        crawlData.results.forEach((result: CrawlResult) => {
          newUrlStatuses.set(result.url, {
            url: result.url,
            status: 'completed',
            result: result
          });
        });
        setUrlStatuses(newUrlStatuses);
      }

      setView('crawl');
    } catch (error) {
      console.error('Failed to load project crawls:', error);
    }
  };

  const handleCrawlSelect = async (crawlId: number) => {
    try {
      const crawlData = await GetCrawlWithResults(crawlId);
      setSelectedCrawl(crawlData.crawlInfo);
      setResults(crawlData.results);

      // Populate urlStatuses from the results
      const newUrlStatuses = new Map<string, UrlStatus>();
      crawlData.results.forEach((result: CrawlResult) => {
        newUrlStatuses.set(result.url, {
          url: result.url,
          status: 'completed',
          result: result
        });
      });
      setUrlStatuses(newUrlStatuses);
    } catch (error) {
      console.error('Failed to load crawl:', error);
    }
  };

  const formatDate = (timestamp: number): string => {
    const date = new Date(timestamp * 1000);
    return date.toLocaleDateString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric'
    });
  };

  const formatDateTime = (timestamp: number): string => {
    const date = new Date(timestamp * 1000);
    return date.toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      hour12: true
    });
  };

  const handleDeleteProject = (projectId: number, e: React.MouseEvent) => {
    e.stopPropagation();
    setProjectToDelete(projectId);
    setShowDeleteProjectModal(true);
  };

  const confirmDeleteProject = async () => {
    if (projectToDelete === null) return;

    try {
      await DeleteProjectByID(projectToDelete);
      setShowDeleteProjectModal(false);
      setProjectToDelete(null);
      await loadProjects();

      // If we deleted the current project, go back to home
      if (currentProject && currentProject.id === projectToDelete) {
        handleHome();
      }
    } catch (error) {
      console.error('Failed to delete project:', error);
    }
  };

  const handleDeleteCrawl = () => {
    if (selectedCrawl) {
      setShowDeleteCrawlModal(true);
    }
  };

  const confirmDeleteCrawl = async () => {
    if (!selectedCrawl || !currentProject) return;

    try {
      await DeleteCrawlByID(selectedCrawl.id);
      setShowDeleteCrawlModal(false);

      // Reload crawls for this project
      const crawls = await GetCrawls(currentProject.id);
      setAvailableCrawls(crawls);

      // If there are still crawls, load the latest one
      if (crawls.length > 0) {
        const latestCrawl = crawls[0];
        const crawlData = await GetCrawlWithResults(latestCrawl.id);
        setSelectedCrawl(crawlData.crawlInfo);
        setResults(crawlData.results);
      } else {
        // No more crawls, go back to home
        handleHome();
      }

      // Reload projects to update the card
      await loadProjects();
    } catch (error) {
      console.error('Failed to delete crawl:', error);
    }
  };

  const formatDuration = (ms: number): string => {
    const seconds = Math.floor(ms / 1000);
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    return `${minutes}m ${remainingSeconds}s`;
  };

  const getStatusColor = (status: number): string => {
    if (status >= 200 && status < 300) return 'status-success';
    if (status >= 300 && status < 400) return 'status-redirect';
    if (status >= 400 && status < 500) return 'status-client-error';
    if (status >= 500) return 'status-server-error';
    return '';
  };

  const handleOpenUrl = (url: string) => {
    BrowserOpenURL(url);
  };

  // Config page
  if (view === 'config') {
    return <Config url={url} onClose={handleCloseConfig} />;
  }

  // Start screen
  if (view === 'start') {
    return (
      <div className="app">
        <div className="start-screen">
          <div className="logo-container">
            <img src={logo} alt="BlueSnake Logo" className="logo-image" />
          </div>
          <h1 className="title">BlueSnake</h1>
          <p className="subtitle">Web Crawler</p>

          <div className="input-container">
            <input
              ref={inputRef}
              type="text"
              className="url-input"
              placeholder="https://example.com"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onKeyPress={handleKeyPress}
              autoFocus
            />
            <button
              className="go-button"
              onClick={handleStartCrawl}
              disabled={!url.trim()}
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="5" y1="12" x2="19" y2="12"></line>
                <polyline points="12 5 19 12 12 19"></polyline>
              </svg>
            </button>
          </div>

          {projects.length > 0 && (
            <div className="projects-section">
              <h3 className="projects-title">Recent Projects</h3>
              <div className="projects-grid">
                {projects.map((project) => (
                  <div
                    key={project.id}
                    className="project-card"
                    onClick={() => handleProjectClick(project)}
                  >
                    <div className="project-header">
                      <div className="project-domain">{project.url}</div>
                      <button
                        className="delete-project-button"
                        onClick={(e) => handleDeleteProject(project.id, e)}
                        title="Delete project"
                      >
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <polyline points="3 6 5 6 21 6"></polyline>
                          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                          <line x1="10" y1="11" x2="10" y2="17"></line>
                          <line x1="14" y1="11" x2="14" y2="17"></line>
                        </svg>
                      </button>
                    </div>
                    <div className="project-date">{formatDate(project.crawlDateTime)}</div>
                    <div className="project-stats">
                      <div className="project-stat">
                        <span className="project-stat-value">{project.pagesCrawled}</span>
                        <span className="project-stat-label">pages</span>
                      </div>
                      <div className="project-stat">
                        <span className="project-stat-value">{formatDuration(project.crawlDuration)}</span>
                        <span className="project-stat-label">duration</span>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Delete Project Modal */}
        {showDeleteProjectModal && (
          <div className="modal-overlay" onClick={() => setShowDeleteProjectModal(false)}>
            <div className="modal" onClick={(e) => e.stopPropagation()}>
              <h3>Delete Project</h3>
              <p>Are you sure you want to delete this project and all its crawls? This action cannot be undone.</p>
              <div className="modal-actions">
                <button className="modal-button cancel" onClick={() => setShowDeleteProjectModal(false)}>
                  Cancel
                </button>
                <button className="modal-button delete" onClick={confirmDeleteProject}>
                  Delete
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    );
  }

  // Crawl screen
  return (
    <div className="app">
      <div className="crawl-screen">
        <div className="header">
          <div className="header-content">
            <div className="brand">
              <img src={logo} alt="BlueSnake Logo" className="brand-logo" />
              <h2 className="crawl-title">BlueSnake</h2>
            </div>
            <div className="header-actions">
              <button className="icon-button" onClick={handleHome} title="Home">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"></path>
                  <polyline points="9 22 9 12 15 12 15 22"></polyline>
                </svg>
              </button>
              <button className="icon-button" onClick={handleOpenConfig} title="Settings">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
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
              </button>
              <button className="new-crawl-button" onClick={handleNewCrawl} disabled={isCrawling}>
                New Crawl
              </button>
            </div>
          </div>
          <div className="header-crawl-info">
            {availableCrawls.length > 0 && selectedCrawl && (
              <div className="crawl-selector">
                <label>Crawl:</label>
                <CustomDropdown
                  value={selectedCrawl.id}
                  options={availableCrawls}
                  onChange={handleCrawlSelect}
                  disabled={isCrawling}
                  formatOption={(crawl) => formatDateTime(crawl.crawlDateTime)}
                />
                {!isCrawling && availableCrawls.length > 1 && (
                  <button
                    className="delete-crawl-button"
                    onClick={handleDeleteCrawl}
                    title="Delete this crawl"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="3 6 5 6 21 6"></polyline>
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                      <line x1="10" y1="11" x2="10" y2="17"></line>
                      <line x1="14" y1="11" x2="14" y2="17"></line>
                    </svg>
                  </button>
                )}
              </div>
            )}
            {currentUrl && isCrawling && (
              <div className="current-url">
                <span className="current-label">Current:</span>
                <span className="current-value">{currentUrl}</span>
              </div>
            )}
          </div>
        </div>

        <div className="results-container">
          <div className="results-header">
            <div className="header-cell url-col">URL</div>
            <div className="header-cell status-col">Status</div>
            <div className="header-cell title-col">Title</div>
            <div className="header-cell indexable-col">Indexable</div>
          </div>

          <div className="results-body">
            {Array.from(urlStatuses.values()).map((urlStatus, index) => (
              <div key={index} className="result-row">
                <div className="result-cell url-col">
                  {urlStatus.status === 'crawling' ? (
                    <span className="url-shimmer">
                      {urlStatus.url}
                    </span>
                  ) : urlStatus.result ? (
                    <span
                      onClick={() => handleOpenUrl(urlStatus.result!.url)}
                      className="url-link"
                      style={{ cursor: 'pointer' }}
                    >
                      {urlStatus.result.url}
                    </span>
                  ) : (
                    <span>{urlStatus.url}</span>
                  )}
                </div>
                <div className={`result-cell status-col ${urlStatus.result ? getStatusColor(urlStatus.result.status) : ''}`}>
                  {urlStatus.status === 'crawling' ? (
                    <SmallLoadingSpinner />
                  ) : urlStatus.result ? (
                    urlStatus.result.error ? 'Error' : urlStatus.result.status
                  ) : (
                    <SmallLoadingSpinner />
                  )}
                </div>
                <div className="result-cell title-col">
                  {urlStatus.status === 'crawling' ? (
                    <SmallLoadingSpinner />
                  ) : urlStatus.result ? (
                    urlStatus.result.error ? urlStatus.result.error : urlStatus.result.title || '(no title)'
                  ) : (
                    <SmallLoadingSpinner />
                  )}
                </div>
                <div className="result-cell indexable-col">
                  {urlStatus.status === 'crawling' ? (
                    <SmallLoadingSpinner />
                  ) : urlStatus.result ? (
                    <span className={`indexable-badge ${urlStatus.result.indexable === 'Yes' ? 'indexable-yes' : 'indexable-no'}`}>
                      {urlStatus.result.indexable}
                    </span>
                  ) : (
                    <SmallLoadingSpinner />
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="footer">
          <div className="footer-content">
            {isCrawling && (
              <div className="status-indicator">
                <CircularProgress crawled={results.length} total={urlStatuses.size} />
                <span className="status-text">Crawling...</span>
              </div>
            )}
            {!isCrawling && (
              <div className="status-indicator completed">
                <span className="status-text">Completed</span>
              </div>
            )}
            <div className="stats">
              <span className="stat-item">
                <span className="stat-label">Total:</span>
                <span className="stat-value">{results.length}</span>
              </span>
              <span className="stat-item">
                <span className="stat-label">Indexable:</span>
                <span className="stat-value">{results.filter(r => r.indexable === 'Yes').length}</span>
              </span>
              <span className="stat-item">
                <span className="stat-label">Non-indexable:</span>
                <span className="stat-value">{results.filter(r => r.indexable === 'No').length}</span>
              </span>
            </div>
          </div>
        </div>

        {/* Delete Crawl Modal */}
        {showDeleteCrawlModal && (
          <div className="modal-overlay" onClick={() => setShowDeleteCrawlModal(false)}>
            <div className="modal" onClick={(e) => e.stopPropagation()}>
              <h3>Delete Crawl</h3>
              <p>Are you sure you want to delete this crawl? This action cannot be undone.</p>
              <div className="modal-actions">
                <button className="modal-button cancel" onClick={() => setShowDeleteCrawlModal(false)}>
                  Cancel
                </button>
                <button className="modal-button delete" onClick={confirmDeleteCrawl}>
                  Delete
                </button>
              </div>
            </div>
          </div>
        )}

        {/* Delete Project Modal */}
        {showDeleteProjectModal && (
          <div className="modal-overlay" onClick={() => setShowDeleteProjectModal(false)}>
            <div className="modal" onClick={(e) => e.stopPropagation()}>
              <h3>Delete Project</h3>
              <p>Are you sure you want to delete this project and all its crawls? This action cannot be undone.</p>
              <div className="modal-actions">
                <button className="modal-button cancel" onClick={() => setShowDeleteProjectModal(false)}>
                  Cancel
                </button>
                <button className="modal-button delete" onClick={confirmDeleteProject}>
                  Delete
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export default App;
