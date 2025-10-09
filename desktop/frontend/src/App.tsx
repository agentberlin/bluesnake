import { useState, useEffect, useRef } from 'react';
import './App.css';
import { StartCrawl, GetProjects } from "../wailsjs/go/main/App";
import { EventsOn, BrowserOpenURL } from "../wailsjs/runtime/runtime";
import logo from './assets/images/bluesnake-logo.png';

interface CrawlResult {
  url: string;
  status: number;
  title: string;
  indexable: string;
  error?: string;
}

interface ProjectInfo {
  id: number;
  url: string;
  domain: string;
  crawlDateTime: number;
  crawlDuration: number;
  pagesCrawled: number;
}

function App() {
  const [url, setUrl] = useState('');
  const [isCrawling, setIsCrawling] = useState(false);
  const [results, setResults] = useState<CrawlResult[]>([]);
  const [currentUrl, setCurrentUrl] = useState('');
  const [hasStarted, setHasStarted] = useState(false);
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Load projects on start
    loadProjects();

    // Listen for crawl events
    EventsOn("crawl:started", (data: any) => {
      setIsCrawling(true);
      setHasStarted(true);
      setResults([]);
    });

    EventsOn("crawl:request", (data: any) => {
      setCurrentUrl(data.url);
    });

    EventsOn("crawl:result", (result: CrawlResult) => {
      setResults(prev => [...prev, result]);
    });

    EventsOn("crawl:error", (result: CrawlResult) => {
      setResults(prev => [...prev, result]);
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

  const handleNewCrawl = () => {
    setHasStarted(false);
    setResults([]);
    setUrl('');
    setIsCrawling(false);
    setCurrentUrl('');
  };

  const handleProjectClick = async (projectUrl: string) => {
    setUrl(projectUrl);
    await handleStartCrawl();
  };

  const formatDate = (timestamp: number): string => {
    const date = new Date(timestamp * 1000);
    return date.toLocaleDateString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric'
    });
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

  if (!hasStarted) {
    // Start screen
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
                    onClick={() => handleProjectClick(project.url)}
                  >
                    <div className="project-header">
                      <div className="project-domain">{project.domain}</div>
                      <div className="project-date">{formatDate(project.crawlDateTime)}</div>
                    </div>
                    <div className="project-url">{project.url}</div>
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
              <button className="new-crawl-button" onClick={handleNewCrawl}>
                New Crawl
              </button>
            </div>
          </div>
          {currentUrl && (
            <div className="current-url">
              <span className="current-label">Current:</span>
              <span className="current-value">{currentUrl}</span>
            </div>
          )}
        </div>

        <div className="results-container">
          <div className="results-header">
            <div className="header-cell url-col">URL</div>
            <div className="header-cell status-col">Status</div>
            <div className="header-cell title-col">Title</div>
            <div className="header-cell indexable-col">Indexable</div>
          </div>

          <div className="results-body">
            {results.map((result, index) => (
              <div key={index} className="result-row">
                <div className="result-cell url-col">
                  <span
                    onClick={() => handleOpenUrl(result.url)}
                    className="url-link"
                    style={{ cursor: 'pointer' }}
                  >
                    {result.url}
                  </span>
                </div>
                <div className={`result-cell status-col ${getStatusColor(result.status)}`}>
                  {result.error ? 'Error' : result.status}
                </div>
                <div className="result-cell title-col">
                  {result.error ? result.error : result.title || '(no title)'}
                </div>
                <div className="result-cell indexable-col">
                  <span className={`indexable-badge ${result.indexable === 'Yes' ? 'indexable-yes' : 'indexable-no'}`}>
                    {result.indexable}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="footer">
          <div className="footer-content">
            {isCrawling && (
              <div className="status-indicator">
                <span className="pulse"></span>
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
      </div>
    </div>
  );
}

export default App;
