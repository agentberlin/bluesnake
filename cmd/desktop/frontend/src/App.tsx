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

import { useState, useEffect, useRef } from 'react';
import './App.css';
import { StartCrawl, GetProjects, GetCrawls, GetCrawlWithResults, DeleteCrawlByID, DeleteProjectByID, GetFaviconData, GetActiveCrawls, StopCrawl, GetActiveCrawlData, CheckForUpdate, DownloadAndInstallUpdate, GetVersion, GetPageLinksForURL } from "../wailsjs/go/main/DesktopApp";
import { EventsOn, BrowserOpenURL } from "../wailsjs/runtime/runtime";
import logo from './assets/images/bluesnake-logo.png';
import Config from './Config';
import LinksPanel from './LinksPanel';
import ServerControl from './ServerControl';
import { types } from "../wailsjs/go/models";

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
  metaDescription?: string;
  contentHash?: string;
  indexable: string;
  error?: string;
}

interface Link {
  url: string;
  anchorText: string;
  context?: string;
  isInternal: boolean;
  status?: number;
  position?: string;
  domPath?: string;
}

interface ProjectInfo {
  id: number;
  url: string;
  domain: string;
  faviconPath: string;
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

interface CrawlProgress {
  projectId: number;
  crawlId: number;
  domain: string;
  url: string;
  pagesCrawled: number;
  totalDiscovered: number;
  discoveredUrls: string[];
  isCrawling: boolean;
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

interface FaviconImageProps {
  faviconPath: string;
  alt: string;
  className: string;
  placeholderSize?: number;
}

function FaviconImage({ faviconPath, alt, className, placeholderSize = 20 }: FaviconImageProps) {
  const [faviconSrc, setFaviconSrc] = useState<string>('');
  const [isLoading, setIsLoading] = useState(true);
  const [hasError, setHasError] = useState(false);

  useEffect(() => {
    if (!faviconPath) {
      setIsLoading(false);
      return;
    }

    setIsLoading(true);
    setHasError(false);

    GetFaviconData(faviconPath)
      .then((dataUrl: string) => {
        setFaviconSrc(dataUrl);
        setIsLoading(false);
      })
      .catch((error: any) => {
        console.error('Failed to load favicon:', error);
        setHasError(true);
        setIsLoading(false);
      });
  }, [faviconPath]);

  if (isLoading || !faviconPath || hasError) {
    return (
      <div className={className.replace('favicon', 'favicon-placeholder')}>
        <svg width={placeholderSize} height={placeholderSize} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10"></circle>
          <path d="M2 12h20"></path>
        </svg>
      </div>
    );
  }

  return <img src={faviconSrc} alt={alt} className={className} />;
}

function App() {
  const [url, setUrl] = useState('');
  const [isCrawling, setIsCrawling] = useState(false);
  const [results, setResults] = useState<CrawlResult[]>([]);
  const [view, setView] = useState<View>('start');
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const [currentProject, setCurrentProject] = useState<ProjectInfo | null>(null);
  const [availableCrawls, setAvailableCrawls] = useState<CrawlInfo[]>([]);
  const [selectedCrawl, setSelectedCrawl] = useState<CrawlInfo | null>(null);
  const [currentCrawlId, setCurrentCrawlId] = useState<number | null>(null);
  const [showDeleteCrawlModal, setShowDeleteCrawlModal] = useState(false);
  const [showDeleteProjectModal, setShowDeleteProjectModal] = useState(false);
  const [projectToDelete, setProjectToDelete] = useState<number | null>(null);
  const [crawlToDelete, setCrawlToDelete] = useState<number | null>(null);
  const [activeCrawls, setActiveCrawls] = useState<Map<number, CrawlProgress>>(new Map());
  const [stoppingProjects, setStoppingProjects] = useState<Set<number>>(new Set());
  const inputRef = useRef<HTMLInputElement>(null);
  const [updateInfo, setUpdateInfo] = useState<types.UpdateInfo | null>(null);
  const [isCheckingUpdate, setIsCheckingUpdate] = useState(false);
  const [isUpdating, setIsUpdating] = useState(false);
  const [showWarningModal, setShowWarningModal] = useState(false);
  const [showBlockModal, setShowBlockModal] = useState(false);

  // Links panel state
  const [isPanelOpen, setIsPanelOpen] = useState(false);
  const [selectedUrlForPanel, setSelectedUrlForPanel] = useState('');
  const [inlinksData, setInlinksData] = useState<Link[]>([]);
  const [outlinksData, setOutlinksData] = useState<Link[]>([]);
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearchQuery, setDebouncedSearchQuery] = useState('');
  const [filteredResults, setFilteredResults] = useState<CrawlResult[]>([]);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const [isCrawlDropdownOpen, setIsCrawlDropdownOpen] = useState(false);
  const crawlDropdownRef = useRef<HTMLDivElement>(null);
  const [appVersion, setAppVersion] = useState<string>('');

  useEffect(() => {
    // Load projects on start
    loadProjects();

    // Discover any active crawls (e.g., if app restarted during a crawl)
    GetActiveCrawls().then(crawls => {
      const crawlsMap = new Map<number, CrawlProgress>();
      crawls.forEach((crawl: CrawlProgress) => {
        crawlsMap.set(crawl.projectId, crawl);
      });
      setActiveCrawls(crawlsMap);
    }).catch(error => {
      console.error('Failed to get active crawls:', error);
    });

    // Get the app version
    GetVersion()
      .then((version: string) => {
        setAppVersion(version);
      })
      .catch((error: any) => {
        console.error('Failed to get version:', error);
      });

    // Check for updates on startup
    setIsCheckingUpdate(true);
    CheckForUpdate()
      .then((info: types.UpdateInfo) => {
        setUpdateInfo(info);
        setIsCheckingUpdate(false);

        // Check for version warnings or blocks
        if (info.shouldBlock) {
          setShowBlockModal(true);
        } else if (info.shouldWarn) {
          setShowWarningModal(true);
        }
      })
      .catch((error: any) => {
        console.error('Failed to check for updates:', error);
        setIsCheckingUpdate(false);
      });

    // We decided to not rely on events for data update because at the scale we are operating at,
    // events add more complexity and we needed to make the system more reliable before getting
    // into complication. When we do need to rely on the payload from events, in the future
    // (fetching all the crawl url every 500 ms is not good because there are millions of them),
    // we'll implement events. For now, events are indicational only - they trigger data refresh
    // via polling, but don't carry any payload.

    // Listen for crawl events (indicational only)
    EventsOn("crawl:started", () => {
      // Just trigger a refresh - polling will handle the updates
      loadProjects();
    });

    EventsOn("crawl:completed", () => {
      // Just trigger a refresh - polling will handle the updates
      loadProjects();
    });

    EventsOn("crawl:stopped", () => {
      // Just trigger a refresh - polling will handle the updates
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

  // Home page polling: Poll for project data when on home page and there are active crawls
  useEffect(() => {
    if (view !== 'start') return;

    const pollHomeData = async () => {
      try {
        // Load projects
        const projectList = await GetProjects();
        setProjects(projectList || []);

        // Load active crawls
        const crawls = await GetActiveCrawls();
        const crawlsMap = new Map<number, CrawlProgress>();
        crawls.forEach((crawl: CrawlProgress) => {
          crawlsMap.set(crawl.projectId, crawl);
        });
        setActiveCrawls(crawlsMap);
      } catch (error) {
        console.error('Failed to poll home data:', error);
      }
    };

    // Initial load
    pollHomeData();

    // Poll every 500ms if there are active crawls
    if (activeCrawls.size > 0) {
      const interval = setInterval(pollHomeData, 500);
      return () => clearInterval(interval);
    }
  }, [view, activeCrawls.size]);

  // Crawl page polling: Poll for crawl data when on crawl page and crawl is active or stopping
  useEffect(() => {
    if (view !== 'crawl' || !currentProject) return;

    const pollCrawlData = async () => {
      try {
        // Check if this project has an active crawl
        const crawls = await GetActiveCrawls();
        const activeCrawl = crawls.find((c: CrawlProgress) => c.projectId === currentProject.id);

        if (activeCrawl) {
          // Active crawl - get crawled data from database
          const crawlData = await GetActiveCrawlData(currentProject.id);

          // Create a set of crawled URLs for quick lookup
          const crawledUrlSet = new Set(crawlData.results.map(r => r.url));

          // Add discovered URLs that haven't been crawled yet as "queued" items
          const queuedResults: CrawlResult[] = activeCrawl.discoveredUrls
            .filter(url => !crawledUrlSet.has(url))
            .map(url => ({
              url,
              status: 0,
              title: 'In progress...',
              indexable: '-',
              error: undefined
            }));

          // Combine crawled results with queued URLs
          setResults([...crawlData.results, ...queuedResults]);
          setIsCrawling(true);
          setCurrentCrawlId(activeCrawl.crawlId);
        } else {
          // No active crawl - get data from database if we have a crawl ID
          if (currentCrawlId) {
            const crawlData = await GetCrawlWithResults(currentCrawlId);
            setResults(crawlData.results);
          }
          setIsCrawling(false);

          // Clear from stopping projects if it was there
          setStoppingProjects(prev => {
            const newSet = new Set(prev);
            newSet.delete(currentProject.id);
            return newSet;
          });
        }
      } catch (error) {
        console.error('Failed to poll crawl data:', error);
      }
    };

    // Initial load
    pollCrawlData();

    // Poll at different intervals: 500ms when stopping, 2s when crawling
    const isStoppingProject = currentProject && stoppingProjects.has(currentProject.id);
    if (isCrawling || isStoppingProject) {
      const pollInterval = isStoppingProject ? 500 : 2000;
      const interval = setInterval(pollCrawlData, pollInterval);
      return () => clearInterval(interval);
    }
  }, [view, currentProject, isCrawling, stoppingProjects, currentCrawlId]);

  // Debounce search query
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearchQuery(searchQuery);
    }, 300);

    return () => clearTimeout(timer);
  }, [searchQuery]);

  // Dummy search function - filters results based on URL
  const performSearch = (query: string, data: CrawlResult[]): CrawlResult[] => {
    if (!query.trim()) {
      return data;
    }

    const lowerQuery = query.toLowerCase();
    return data.filter(result =>
      result.url.toLowerCase().includes(lowerQuery) ||
      result.title.toLowerCase().includes(lowerQuery) ||
      result.status.toString().includes(lowerQuery) ||
      result.indexable.toLowerCase().includes(lowerQuery)
    );
  };

  // Update filtered results when debounced search query or results change
  useEffect(() => {
    setFilteredResults(performSearch(debouncedSearchQuery, results));
  }, [debouncedSearchQuery, results]);

  const loadCurrentProjectFromUrl = async (currentUrl: string) => {
    try {
      const projectList = await GetProjects();
      if (!projectList) return;

      // Normalize the current URL for comparison
      let normalizedUrl = currentUrl.trim();
      if (!normalizedUrl.startsWith('http://') && !normalizedUrl.startsWith('https://')) {
        normalizedUrl = 'https://' + normalizedUrl;
      }

      // Find the project matching the current URL or domain
      const project = projectList.find(p => {
        // Try exact URL match first
        if (p.url === normalizedUrl) return true;
        // Try matching by domain
        try {
          const urlObj = new URL(normalizedUrl);
          return p.domain === urlObj.hostname;
        } catch {
          return false;
        }
      });

      if (!project) return;

      // Set as current project
      setCurrentProject(project);

      // Load crawls for this project
      const crawls = await GetCrawls(project.id);
      setAvailableCrawls(crawls);

      // Load the latest crawl
      if (project.latestCrawlId) {
        const crawlData = await GetCrawlWithResults(project.latestCrawlId);
        setSelectedCrawl(crawlData.crawlInfo);
      }
    } catch (error) {
      console.error('Failed to load current project:', error);
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
      handleNewCrawl();
    }
  };

  const handleHome = async () => {
    setView('start');
    setResults([]);
    setUrl('');
    setIsCrawling(false);
    setCurrentCrawlId(null);
    setSearchQuery('');
    setDebouncedSearchQuery('');
    setIsCrawlDropdownOpen(false);

    // Reload projects to show any newly created ones
    await loadProjects();
  };

  // Handle click outside dropdown
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (crawlDropdownRef.current && !crawlDropdownRef.current.contains(event.target as Node)) {
        setIsCrawlDropdownOpen(false);
      }
    };

    if (isCrawlDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isCrawlDropdownOpen]);

  const handleNewCrawl = async () => {
    if (!url.trim()) return;

    try {
      await StartCrawl(url);

      // Immediately load the project and navigate to crawl view
      await loadCurrentProjectFromUrl(url);

      // Set crawling state to start polling
      setIsCrawling(true);

      // Navigate to crawl view
      setView('crawl');
    } catch (error) {
      console.error('Failed to start crawl:', error);
      setIsCrawling(false);
    }
  };

  const handleOpenConfig = () => {
    setView('config');
  };

  const handleOpenConfigFromHome = async () => {
    if (!url.trim()) return;

    // Try to load the project if it exists
    await loadCurrentProjectFromUrl(url);

    setView('config');
  };

  const handleCloseConfig = async () => {
    // If we came from home page with a URL, try to load the project and go to crawl page
    if (url.trim()) {
      await loadCurrentProjectFromUrl(url);

      // Try to find the project - it might exist if user saved config after a previous crawl
      const projectList = await GetProjects();
      let normalizedUrl = url.trim();
      if (!normalizedUrl.startsWith('http://') && !normalizedUrl.startsWith('https://')) {
        normalizedUrl = 'https://' + normalizedUrl;
      }

      const project = projectList?.find(p => {
        if (p.url === normalizedUrl) return true;
        try {
          const urlObj = new URL(normalizedUrl);
          return p.domain === urlObj.hostname;
        } catch {
          return false;
        }
      });

      if (project) {
        // Project exists - load its crawls
        setCurrentProject(project);
        const crawls = await GetCrawls(project.id);
        setAvailableCrawls(crawls);
      } else {
        // Project doesn't exist yet (config was just saved for a new URL)
        // Clear project state and show empty state
        setCurrentProject(null);
        setAvailableCrawls([]);
        setResults([]);
      }

      // Always go to crawl view (will show empty state if no project/crawls)
      setView('crawl');
    } else if (currentProject) {
      // If we already have a current project, stay on crawl page
      setView('crawl');
    } else {
      setView('start');
    }
  };

  const handleProjectClick = async (project: ProjectInfo) => {
    setCurrentProject(project);
    setUrl(project.url);

    // Load all crawls for this project
    try {
      // Check if there's an active crawl for this project
      const activeCrawl = activeCrawls.get(project.id);
      const crawlIdToLoad = activeCrawl ? activeCrawl.crawlId : project.latestCrawlId;
      const isActiveCrawl = !!activeCrawl;

      // Set the current crawl ID for tracking
      setCurrentCrawlId(crawlIdToLoad);
      setIsCrawling(isActiveCrawl);

      const crawls = await GetCrawls(project.id);
      setAvailableCrawls(crawls);

      // Load the crawl results (either active or latest completed)
      if (crawlIdToLoad) {
        const crawlData = await GetCrawlWithResults(crawlIdToLoad);
        setSelectedCrawl(crawlData.crawlInfo);
        setResults(crawlData.results);
      }

      setView('crawl');
    } catch (error) {
      console.error('Failed to load project crawls:', error);
    }
  };

  const handleCrawlSelect = async (crawlId: number) => {
    try {
      // Set the current crawl ID for tracking
      setCurrentCrawlId(crawlId);

      // Check if this is an active crawl
      const isActive = !!(currentProject && activeCrawls.has(currentProject.id) &&
                       activeCrawls.get(currentProject.id)?.crawlId === crawlId);
      setIsCrawling(isActive);

      const crawlData = await GetCrawlWithResults(crawlId);
      setSelectedCrawl(crawlData.crawlInfo);
      setResults(crawlData.results);
      setIsCrawlDropdownOpen(false);
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

  const handleDeleteCrawl = (crawlId: number) => {
    setCrawlToDelete(crawlId);
    setShowDeleteCrawlModal(true);
    setIsCrawlDropdownOpen(false);
  };

  const confirmDeleteCrawl = async () => {
    if (!crawlToDelete || !currentProject) return;

    try {
      await DeleteCrawlByID(crawlToDelete);
      setShowDeleteCrawlModal(false);
      setCrawlToDelete(null);

      // Reload crawls for this project
      const crawls = await GetCrawls(currentProject.id);
      setAvailableCrawls(crawls);

      // If we deleted the currently selected crawl, switch to another one
      if (selectedCrawl && crawlToDelete === selectedCrawl.id) {
        if (crawls.length > 0) {
          const latestCrawl = crawls[0];
          const crawlData = await GetCrawlWithResults(latestCrawl.id);
          setSelectedCrawl(crawlData.crawlInfo);
          setResults(crawlData.results);
          setCurrentCrawlId(latestCrawl.id);
        } else {
          // No more crawls, go back to home
          handleHome();
        }
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

  const getDomainName = (): string => {
    if (currentProject?.domain) {
      return currentProject.domain;
    }
    try {
      return new URL(url).hostname;
    } catch {
      return url || 'Unknown Domain';
    }
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

  const handleStopCrawl = async () => {
    if (!currentProject) return;

    try {
      setStoppingProjects(prev => new Set(prev).add(currentProject.id));
      await StopCrawl(currentProject.id);
    } catch (error) {
      console.error('Failed to stop crawl:', error);
      setStoppingProjects(prev => {
        const newSet = new Set(prev);
        newSet.delete(currentProject.id);
        return newSet;
      });
    }
  };

  const handleUpdate = async () => {
    if (!updateInfo?.updateAvailable) return;

    try {
      setIsUpdating(true);
      await DownloadAndInstallUpdate();
      // App will quit and restart after update
    } catch (error) {
      console.error('Failed to update:', error);
      setIsUpdating(false);
      alert(`Failed to install update: ${error}\n\nPlease check the console for details.`);
    }
  };

  const handleUrlClick = async (clickedUrl: string) => {
    if (!currentCrawlId) {
      console.error('No current crawl ID available');
      return;
    }

    try {
      const response = await GetPageLinksForURL(currentCrawlId, clickedUrl);
      setSelectedUrlForPanel(clickedUrl);
      setInlinksData(response.inlinks);
      setOutlinksData(response.outlinks);
      setIsPanelOpen(true);
    } catch (error) {
      console.error('Failed to load links:', error);
    }
  };

  const handleClosePanel = () => {
    setIsPanelOpen(false);
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
          <ServerControl />
          <div className="logo-container">
            <img src={logo} alt="BlueSnake Logo" className="logo-image" />
          </div>
          <h1 className="title">Blue Snake</h1>
          <p className="subtitle">World's #1 AI Native Web Crawler</p>

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
              className="config-button"
              onClick={handleOpenConfigFromHome}
              disabled={!url.trim()}
              title="Configure before crawling"
            >
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
            <button
              className="go-button"
              onClick={handleNewCrawl}
              disabled={!url.trim()}
              title="Start crawl now"
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
                {projects.map((project) => {
                  const activeCrawl = activeCrawls.get(project.id);
                  const isActivelyCrawling = !!activeCrawl;
                  const hasNoCrawls = !isActivelyCrawling && project.latestCrawlId === 0;

                  return (
                    <div
                      key={project.id}
                      className="project-card"
                      onClick={() => handleProjectClick(project)}
                    >
                      <div className="project-header">
                        <div className="project-title-row">
                          <FaviconImage
                            faviconPath={project.faviconPath || ''}
                            alt="Domain favicon"
                            className="project-favicon"
                            placeholderSize={16}
                          />
                          <div className="project-domain">{project.url}</div>
                        </div>
                        <button
                          className="delete-project-button"
                          onClick={(e) => handleDeleteProject(project.id, e)}
                          title="Delete project"
                        >
                          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                            <polyline points="3 6 5 6 21 6"></polyline>
                            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 2 0 0 1 2 2v2"></path>
                            <line x1="10" y1="11" x2="10" y2="17"></line>
                            <line x1="14" y1="11" x2="14" y2="17"></line>
                          </svg>
                        </button>
                      </div>
                      {isActivelyCrawling ? (
                        <>
                          <div className="project-date">Currently crawling...</div>
                          <div className="project-stats">
                            <CircularProgress
                              crawled={activeCrawl.pagesCrawled}
                              total={activeCrawl.totalDiscovered}
                            />
                          </div>
                        </>
                      ) : hasNoCrawls ? (
                        <>
                          <div className="project-date">Not crawled yet</div>
                          <div className="project-stats project-stats-empty">
                            <span className="project-stat-empty">Configure and start crawl</span>
                          </div>
                        </>
                      ) : (
                        <>
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
                        </>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>

        {/* Update Button - bottom right, only shown when update is available */}
        {updateInfo?.updateAvailable && (
          <div className="update-button-container">
            <button
              className="update-button"
              onClick={handleUpdate}
              disabled={isUpdating}
              title={`Update to ${updateInfo.latestVersion}`}
            >
              {isUpdating ? (
                <>
                  <SmallLoadingSpinner />
                  <span>Updating...</span>
                </>
              ) : (
                <>
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2"/>
                  </svg>
                  <span>Update Available ({updateInfo.latestVersion})</span>
                </>
              )}
            </button>
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
    );
  }

  // Crawl screen
  const hasNoCrawls = availableCrawls.length === 0;

  return (
    <div className="app">
      <div className="crawl-screen">
        <div className="header">
          <div className="header-left">
            <button className="icon-button" onClick={handleHome} title="Home">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"></path>
                <polyline points="9 22 9 12 15 12 15 22"></polyline>
              </svg>
            </button>
            <div className="domain-info">
              <div className="domain-info-header">
                <FaviconImage
                  faviconPath={currentProject?.faviconPath || ''}
                  alt="Domain favicon"
                  className="domain-favicon"
                  placeholderSize={20}
                />
                <h2 className="domain-name">{getDomainName()}</h2>
              </div>
              {!hasNoCrawls && availableCrawls.length > 0 && selectedCrawl && (
                <div className="crawl-info-container" ref={crawlDropdownRef}>
                  <button
                    className="crawl-info-trigger"
                    onClick={() => setIsCrawlDropdownOpen(!isCrawlDropdownOpen)}
                    disabled={isCrawling}
                  >
                    <span className="crawl-info-text">
                      Crawled on {formatDateTime(selectedCrawl.crawlDateTime)}
                    </span>
                    <svg className="crawl-dropdown-icon" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="6 9 12 15 18 9"></polyline>
                    </svg>
                  </button>
                  {isCrawlDropdownOpen && !isCrawling && (
                    <div className="crawl-dropdown-menu">
                      {availableCrawls.map((crawl) => (
                        <div
                          key={crawl.id}
                          className={`crawl-dropdown-item ${crawl.id === selectedCrawl.id ? 'selected' : ''}`}
                        >
                          <div
                            className="crawl-dropdown-option"
                            onClick={() => handleCrawlSelect(crawl.id)}
                          >
                            {formatDateTime(crawl.crawlDateTime)}
                          </div>
                          {availableCrawls.length > 1 && (
                            <button
                              className="crawl-item-delete-button"
                              onClick={(e) => {
                                e.stopPropagation();
                                handleDeleteCrawl(crawl.id);
                              }}
                              title="Delete this crawl"
                            >
                              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <polyline points="3 6 5 6 21 6"></polyline>
                                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                                <line x1="10" y1="11" x2="10" y2="17"></line>
                                <line x1="14" y1="11" x2="14" y2="17"></line>
                              </svg>
                            </button>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>

          {!hasNoCrawls && (
            <div className="header-center">
              <div className="header-search-bar">
                <svg className="search-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="11" cy="11" r="8"></circle>
                  <path d="m21 21-4.35-4.35"></path>
                </svg>
                <input
                  ref={searchInputRef}
                  type="text"
                  className="header-search-input"
                  placeholder="Search by URL, title, status, or indexable..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                />
                {searchQuery && (
                  <span className="search-results-count-inline">
                    {filteredResults.length} {filteredResults.length === 1 ? 'result' : 'results'}
                  </span>
                )}
                {searchQuery && (
                  <button
                    className="search-clear-button-inline"
                    onClick={() => setSearchQuery('')}
                    title="Clear search"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <line x1="18" y1="6" x2="6" y2="18"></line>
                      <line x1="6" y1="6" x2="18" y2="18"></line>
                    </svg>
                  </button>
                )}
              </div>
            </div>
          )}

          {!hasNoCrawls && (
            <div className="header-right">
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
              {isCrawling && currentProject && (
                <button
                  className="stop-crawl-button"
                  onClick={handleStopCrawl}
                  disabled={stoppingProjects.has(currentProject.id)}
                  title="Stop crawling"
                >
                  {stoppingProjects.has(currentProject.id) ? 'Stopping...' : 'Stop Crawl'}
                </button>
              )}
              <button className="new-crawl-button" onClick={handleNewCrawl} disabled={isCrawling}>
                New Crawl
              </button>
            </div>
          )}
        </div>

        {hasNoCrawls ? (
          <div className="empty-state-container">
            <div className="empty-state-content">
              <h3 className="empty-state-title">No crawls yet</h3>
              <p className="empty-state-description">Configure your crawl settings and start crawling</p>
              <div className="empty-state-actions">
                <button className="empty-state-button secondary" onClick={handleOpenConfig}>
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
                  Settings
                </button>
                <button className="empty-state-button primary" onClick={handleNewCrawl}>
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="5" y1="12" x2="19" y2="12"></line>
                    <polyline points="12 5 19 12 12 19"></polyline>
                  </svg>
                  Start Crawl
                </button>
              </div>
            </div>
          </div>
        ) : (
          <div className="results-container">
            <div className="results-header">
              <div className="header-cell url-col">URL</div>
              <div className="header-cell status-col">Status</div>
              <div className="header-cell title-col">Title</div>
              <div className="header-cell meta-desc-col">Meta Description</div>
              <div className="header-cell indexable-col">Indexable</div>
            </div>

            <div className="results-body">
              {filteredResults.map((result, index) => {
                const isInProgress = result.status === 0 && result.title === 'In progress...';
                return (
                  <div
                    key={index}
                    className="result-row"
                    onClick={() => !isInProgress && handleUrlClick(result.url)}
                    style={{ cursor: isInProgress ? 'default' : 'pointer' }}
                    title={isInProgress ? '' : 'Click row to view internal links'}
                  >
                    <div className="result-cell url-col">
                      <span
                        className="url-link"
                        style={{ opacity: isInProgress ? 0.6 : 1 }}
                        onClick={(e) => {
                          if (!isInProgress) {
                            e.stopPropagation();
                            handleOpenUrl(result.url);
                          }
                        }}
                        title={isInProgress ? '' : 'Click to open URL in browser'}
                      >
                        {result.url}
                      </span>
                    </div>
                    <div className={`result-cell status-col ${getStatusColor(result.status)}`} style={{ opacity: isInProgress ? 0.6 : 1 }}>
                      {isInProgress ? 'Queued' : (result.error ? 'Error' : result.status)}
                    </div>
                    <div className="result-cell title-col" style={{ opacity: isInProgress ? 0.6 : 1 }}>
                      {result.error ? result.error : result.title || '(no title)'}
                    </div>
                    <div className="result-cell meta-desc-col" style={{ opacity: isInProgress ? 0.6 : 1 }} title={result.metaDescription || ''}>
                      {result.metaDescription || '-'}
                    </div>
                    <div className="result-cell indexable-col" style={{ opacity: isInProgress ? 0.6 : 1 }}>
                      <span className={`indexable-badge ${result.indexable === 'Yes' ? 'indexable-yes' : 'indexable-no'}`}>
                        {result.indexable}
                      </span>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {!hasNoCrawls && (
          <div className="footer">
            <div className="footer-content">
              {isCrawling && currentProject && (
                <div className="status-indicator">
                  <CircularProgress
                    crawled={results.filter(r => !(r.status === 0 && r.title === 'In progress...')).length}
                    total={results.length}
                  />
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
                  <span className="stat-label">{searchQuery ? 'Showing:' : 'Total:'}</span>
                  <span className="stat-value">{filteredResults.length}{searchQuery && ` of ${results.length}`}</span>
                </span>
                <span className="stat-item">
                  <span className="stat-label">Indexable:</span>
                  <span className="stat-value">{filteredResults.filter(r => r.indexable === 'Yes').length}</span>
                </span>
                <span className="stat-item">
                  <span className="stat-label">Non-indexable:</span>
                  <span className="stat-value">{filteredResults.filter(r => r.indexable === 'No').length}</span>
                </span>
              </div>
            </div>
          </div>
        )}

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

        {/* Links Panel */}
        <LinksPanel
          isOpen={isPanelOpen}
          onClose={handleClosePanel}
          selectedUrl={selectedUrlForPanel}
          inlinks={inlinksData}
          outlinks={outlinksData}
          crawlId={currentCrawlId ?? undefined}
        />
      </div>

      {/* Version Warning Modal */}
      {showWarningModal && updateInfo && (
        <div className="modal-overlay version-warning-overlay" onClick={() => setShowWarningModal(false)}>
          <div className="modal version-warning-modal" onClick={(e) => e.stopPropagation()}>
            <button className="modal-close-button" onClick={() => setShowWarningModal(false)} title="Close">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <line x1="18" y1="6" x2="6" y2="18"></line>
                <line x1="6" y1="6" x2="18" y2="18"></line>
              </svg>
            </button>
            <div className="version-modal-icon warning-icon">
              <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
                <line x1="12" y1="9" x2="12" y2="13"></line>
                <line x1="12" y1="17" x2="12.01" y2="17"></line>
              </svg>
            </div>
            <h3>Version Warning</h3>
            <p className="version-info">You are using version {updateInfo.currentVersion}</p>
            <p className="version-reason">{updateInfo.displayReason}</p>
            <div className="modal-actions">
              <button className="modal-button primary" onClick={handleUpdate} disabled={isUpdating}>
                {isUpdating ? 'Updating...' : `Update to ${updateInfo.latestVersion}`}
              </button>
              <button className="modal-button cancel" onClick={() => setShowWarningModal(false)}>
                Continue Anyway
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Version Block Modal */}
      {showBlockModal && updateInfo && (
        <div className="modal-overlay version-block-overlay">
          <div className="modal version-block-modal" onClick={(e) => e.stopPropagation()}>
            <div className="version-modal-icon block-icon">
              <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10"></circle>
                <line x1="4.93" y1="4.93" x2="19.07" y2="19.07"></line>
              </svg>
            </div>
            <h3>Update Required</h3>
            <p className="version-info">You are using version {updateInfo.currentVersion}</p>
            <p className="version-reason">{updateInfo.displayReason}</p>
            <p className="version-instruction">Please update to continue using BlueSnake.</p>
            <div className="modal-actions">
              <button className="modal-button primary" onClick={handleUpdate} disabled={isUpdating}>
                {isUpdating ? 'Updating...' : `Update to ${updateInfo.latestVersion}`}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default App;
