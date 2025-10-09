import { useState, useEffect, useRef } from 'react';
import './App.css';
import { StartCrawl, GetProjects } from "../wailsjs/go/main/App";
import { EventsOn, BrowserOpenURL } from "../wailsjs/runtime/runtime";
import logo from './assets/images/bluesnake-logo.png';
import Config from './Config';

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

function App() {
  const [url, setUrl] = useState('');
  const [isCrawling, setIsCrawling] = useState(false);
  const [results, setResults] = useState<CrawlResult[]>([]);
  const [currentUrl, setCurrentUrl] = useState('');
  const [view, setView] = useState<View>('start');
  const [hasStarted, setHasStarted] = useState(false);
  const [discoveredUrls, setDiscoveredUrls] = useState<Set<string>>(new Set());
  const [completedUrls, setCompletedUrls] = useState<Set<string>>(new Set());
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Load projects on start
    loadProjects();

    // Listen for crawl events
    EventsOn("crawl:started", (data: any) => {
      setIsCrawling(true);
      setView('crawl');
      setResults([]);
      setDiscoveredUrls(new Set());
      setCompletedUrls(new Set());
    });

    EventsOn("crawl:request", (data: any) => {
      setCurrentUrl(data.url);
      setDiscoveredUrls(prev => new Set(prev).add(data.url));
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
      // Mark URL as completed
      setCompletedUrls(prev => new Set(prev).add(result.url));
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
      // Mark URL as completed
      setCompletedUrls(prev => new Set(prev).add(result.url));
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
    setDiscoveredUrls(new Set());
    setCompletedUrls(new Set());
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
                    onClick={() => handleProjectClick(project.url)}
                  >
                    <div className="project-header">
                      <div className="project-domain">{project.url}</div>
                      <div className="project-date">{formatDate(project.crawlDateTime)}</div>
                    </div>
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
                <CircularProgress crawled={completedUrls.size} total={discoveredUrls.size} />
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
