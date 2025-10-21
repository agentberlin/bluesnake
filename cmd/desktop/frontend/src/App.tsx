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
import { StartCrawl, GetProjects, GetCrawls, DeleteCrawlByID, DeleteProjectByID, GetFaviconData, GetActiveCrawls, StopCrawl, GetActiveCrawlStats, GetCrawlStats, CheckForUpdate, DownloadAndInstallUpdate, GetVersion, GetPageLinksForURL, UpdateConfigForDomain, GetConfigForDomain, DetectJSRenderingNeed, SearchCrawlResultsPaginated } from "../wailsjs/go/main/DesktopApp";
import { EventsOn, BrowserOpenURL } from "../wailsjs/runtime/runtime";
import logo from './assets/images/bluesnake-logo.png';
import Config from './Config';
import LinksPanel from './LinksPanel';
import ServerControl from './ServerControl';
import Sidebar from './Sidebar';
import AICrawlers from './AICrawlers';
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

interface ColumnSelectorProps {
  visibleColumns: Record<string, boolean>;
  onColumnToggle: (column: string) => void;
}

function ColumnSelector({ visibleColumns, onColumnToggle }: ColumnSelectorProps) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [menuPosition, setMenuPosition] = useState({ top: 0, right: 0 });

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  useEffect(() => {
    if (isOpen && buttonRef.current) {
      const rect = buttonRef.current.getBoundingClientRect();
      setMenuPosition({
        top: rect.bottom + 4,
        right: window.innerWidth - rect.right
      });
    }
  }, [isOpen]);

  const columns = [
    { key: 'url', label: 'URL' },
    { key: 'status', label: 'Status' },
    { key: 'title', label: 'Title' },
    { key: 'metaDescription', label: 'Meta Description' },
    { key: 'indexable', label: 'Indexable' },
    { key: 'contentType', label: 'Type' }
  ];

  const visibleCount = Object.values(visibleColumns).filter(Boolean).length;

  return (
    <div className="column-selector" ref={dropdownRef}>
      <button
        ref={buttonRef}
        className="column-selector-button"
        onClick={() => setIsOpen(!isOpen)}
        title="Select visible columns"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M3 3h7v7H3z"></path>
          <path d="M14 3h7v7h-7z"></path>
          <path d="M14 14h7v7h-7z"></path>
          <path d="M3 14h7v7H3z"></path>
        </svg>
        <span className="column-selector-text">Columns ({visibleCount})</span>
        <svg className="column-selector-arrow" width="12" height="8" viewBox="0 0 12 8" fill="none">
          <path d="M1 1.5L6 6.5L11 1.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </button>
      {isOpen && (
        <div
          className="column-selector-menu"
          style={{ top: `${menuPosition.top}px`, right: `${menuPosition.right}px` }}
        >
          {columns.map((column) => (
            <label
              key={column.key}
              className="column-selector-item"
              onClick={(e) => e.stopPropagation()}
            >
              <input
                type="checkbox"
                checked={visibleColumns[column.key]}
                onChange={() => onColumnToggle(column.key)}
                className="column-selector-checkbox"
              />
              <span className="column-selector-label">{column.label}</span>
            </label>
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
  contentType?: string;
  error?: string;
}

interface CrawlResultPaginated {
  results: CrawlResult[];
  nextCursor: number;
  hasMore: boolean;
}

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

interface ProjectInfo {
  id: number;
  url: string;
  domain: string;
  faviconPath: string;
  crawlDateTime: number;
  crawlDuration: number;
  pagesCrawled: number;
  totalUrls: number;
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
  totalUrlsCrawled: number;
  totalDiscovered: number;
  discoveredUrls: string[];
  isCrawling: boolean;
}

interface ActiveCrawlStats {
  crawlId: number;
  total: number;
  crawled: number;
  queued: number;
  html: number;
  javascript: number;
  css: number;
  images: number;
  fonts: number;
  unvisited: number;
  others: number;
}

interface ConfigData {
  domain: string;
  jsRenderingEnabled: boolean;
  initialWaitMs: number;
  scrollWaitMs: number;
  finalWaitMs: number;
  parallelism: number;
  userAgent: string;
  includeSubdomains: boolean;
  discoveryMechanisms: string[];
  checkExternalResources: boolean;
  singlePageMode: boolean;
}

type View = 'start' | 'dashboard';
type DashboardSection = 'crawl-results' | 'config' | 'ai-crawlers';

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
  const [dashboardSection, setDashboardSection] = useState<DashboardSection>('crawl-results');
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
  const [contentTypeFilter, setContentTypeFilter] = useState<string>('html');
  const [isCrawlTypeDropdownOpen, setIsCrawlTypeDropdownOpen] = useState(false);
  const crawlTypeDropdownRef = useRef<HTMLDivElement>(null);
  const [activeCrawlStats, setActiveCrawlStats] = useState<ActiveCrawlStats | null>(null);
  const [crawlStats, setCrawlStats] = useState<ActiveCrawlStats | null>(null);

  // Column visibility state
  const [visibleColumns, setVisibleColumns] = useState<Record<string, boolean>>({
    url: true,
    status: true,
    title: true,
    metaDescription: true,
    indexable: true,
    contentType: true
  });

  // Pagination state
  const [cursor, setCursor] = useState<number>(0);
  const [hasMore, setHasMore] = useState<boolean>(false);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const resultsBodyRef = useRef<HTMLDivElement>(null);
  const PAGINATION_LIMIT = 100;

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

    // Always poll every 500ms when on home page to detect new crawls
    // The interval will be cleaned up when we leave the home page
    const interval = setInterval(pollHomeData, 500);
    return () => clearInterval(interval);
  }, [view]);

  // Dashboard polling: Poll for crawl data when on dashboard and crawl is active or stopping
  useEffect(() => {
    if (view !== 'dashboard' || !currentProject) return;

    const pollCrawlData = async () => {
      try {
        // Check if this project has an active crawl
        const crawls = await GetActiveCrawls();
        const activeCrawl = crawls.find((c: CrawlProgress) => c.projectId === currentProject.id);

        if (activeCrawl) {
          // Active crawl - get stats from backend (efficient COUNT queries)
          const stats = await GetActiveCrawlStats(currentProject.id);
          setActiveCrawlStats(stats);
          setIsCrawling(true);
          setCurrentCrawlId(activeCrawl.crawlId);

          // Update current project info to get latest favicon and other metadata
          const projectList = await GetProjects();
          const updatedProject = projectList?.find(p => p.id === currentProject.id);
          if (updatedProject) {
            setCurrentProject(updatedProject);
          }
        } else {
          // No active crawl - just update the isCrawling state
          // The pagination/search effect will handle loading the data
          setIsCrawling(false);
          setActiveCrawlStats(null);

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

  // Helper function to categorize content type
  const categorizeContentType = (contentType: string | undefined): string => {
    if (!contentType) return 'other';

    const ct = contentType.toLowerCase();
    if (ct.includes('text/html') || ct.includes('application/xhtml')) return 'html';
    if (ct.includes('javascript') || ct.includes('application/x-javascript') || ct.includes('text/javascript')) return 'javascript';
    if (ct.includes('text/css')) return 'css';
    if (ct.includes('image/')) return 'image';
    if (ct.includes('font/') || ct.includes('application/font') || ct.includes('woff') || ct.includes('ttf') || ct.includes('eot') || ct.includes('otf')) return 'font';
    return 'other';
  };

  // Helper function to display friendly content type name
  const getContentTypeDisplay = (contentType: string | undefined): string => {
    if (!contentType) return 'Unknown';

    const category = categorizeContentType(contentType);
    const ct = contentType.toLowerCase();

    // Return more specific type for images
    if (category === 'image') {
      if (ct.includes('jpeg') || ct.includes('jpg')) return 'JPEG';
      if (ct.includes('png')) return 'PNG';
      if (ct.includes('gif')) return 'GIF';
      if (ct.includes('webp')) return 'WebP';
      if (ct.includes('svg')) return 'SVG';
      return 'Image';
    }

    // Return capitalized category for others
    return category.charAt(0).toUpperCase() + category.slice(1);
  };

  // Fetch stats for completed crawls (when not actively crawling)
  useEffect(() => {
    // Only fetch stats if we have a crawl ID and are NOT crawling
    if (!currentCrawlId || isCrawling) {
      setCrawlStats(null);
      return;
    }

    // Fetch stats from backend
    GetCrawlStats(currentCrawlId)
      .then((stats: ActiveCrawlStats) => {
        setCrawlStats(stats);
      })
      .catch((error: any) => {
        console.error('Failed to fetch crawl stats:', error);
        setCrawlStats(null);
      });
  }, [currentCrawlId, isCrawling]);

  // Update filtered results when debounced search query, content type filter, or currentCrawlId change
  // Note: This uses debouncedSearchQuery (not searchQuery) to reduce database load
  // The debounce happens 300ms after the user stops typing (see useEffect above)
  useEffect(() => {
    // Only perform search if we have a crawl ID
    if (!currentCrawlId) {
      setFilteredResults([]);
      setCursor(0);
      setHasMore(false);
      return;
    }

    // Reset pagination when filters change
    setCursor(0);
    setHasMore(false);

    // Call backend search with debounced query, content type filter, and pagination
    SearchCrawlResultsPaginated(currentCrawlId, debouncedSearchQuery, contentTypeFilter, PAGINATION_LIMIT, 0)
      .then((paginatedResults: CrawlResultPaginated) => {
        setFilteredResults(paginatedResults.results);
        setCursor(paginatedResults.nextCursor);
        setHasMore(paginatedResults.hasMore);
      })
      .catch((error: any) => {
        console.error('Failed to search crawl results:', error);
        // On error, fall back to empty results
        setFilteredResults([]);
        setCursor(0);
        setHasMore(false);
      });
  }, [debouncedSearchQuery, contentTypeFilter, currentCrawlId]);

  // Poll for URL list updates during active crawling
  // This keeps the first page of results fresh without refetching everything
  useEffect(() => {
    // Only poll if we're actively crawling and have a crawl ID
    if (!isCrawling || !currentCrawlId) return;

    // Stop polling when we have a full page (optimization)
    // If we have PAGINATION_LIMIT results, stop polling to allow infinite scroll to work
    // Pagination will handle loading more pages when user scrolls
    if (filteredResults.length >= PAGINATION_LIMIT) {
      return;
    }

    const pollUrls = async () => {
      try {
        // Fetch first page of results for current filter
        const paginatedResults: CrawlResultPaginated = await SearchCrawlResultsPaginated(
          currentCrawlId,
          debouncedSearchQuery,
          contentTypeFilter,
          PAGINATION_LIMIT,
          0
        );

        // Update the results
        setFilteredResults(paginatedResults.results);
        setCursor(paginatedResults.nextCursor);
        setHasMore(paginatedResults.hasMore);
      } catch (error) {
        console.error('Failed to poll URL list:', error);
      }
    };

    // Initial poll
    pollUrls();

    // Poll every 2 seconds during crawling
    const interval = setInterval(pollUrls, 2000);
    return () => clearInterval(interval);
  }, [isCrawling, currentCrawlId, debouncedSearchQuery, contentTypeFilter, filteredResults.length]);

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

      // Load the latest crawl - just set the ID, pagination will handle loading data
      if (project.latestCrawlId) {
        // Get crawl info only (without results)
        const crawls = await GetCrawls(project.id);
        const latestCrawl = crawls.find(c => c.id === project.latestCrawlId);
        if (latestCrawl) {
          setSelectedCrawl(latestCrawl);
        }
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

  // Handle click outside crawl type dropdown
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (crawlTypeDropdownRef.current && !crawlTypeDropdownRef.current.contains(event.target as Node)) {
        setIsCrawlTypeDropdownOpen(false);
      }
    };

    if (isCrawlTypeDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isCrawlTypeDropdownOpen]);

  const handleNewCrawl = async () => {
    if (!url.trim()) return;

    try {
      // Close the dropdown
      setIsCrawlTypeDropdownOpen(false);

      // First, get existing config to preserve user settings
      try {
        const existingConfig: ConfigData = await GetConfigForDomain(url);

        // Update config with singlePageMode disabled, preserving other settings
        const mechanisms = existingConfig.discoveryMechanisms || ["spider"];
        await UpdateConfigForDomain(
          url,
          existingConfig.jsRenderingEnabled,
          existingConfig.initialWaitMs || 1500,
          existingConfig.scrollWaitMs || 2000,
          existingConfig.finalWaitMs || 1000,
          existingConfig.parallelism,
          existingConfig.userAgent || 'bluesnake/1.0 (+https://snake.blue)',
          existingConfig.includeSubdomains,
          true,  // spiderEnabled - always true
          mechanisms.includes("sitemap"),
          [],    // sitemapURLs
          existingConfig.checkExternalResources,
          false,  // singlePageMode - DISABLE for full crawl
          "respect",  // robotsTxtMode - default
          false,  // followInternalNofollow - default
          false,  // followExternalNofollow - default
          true,   // respectMetaRobotsNoindex - default
          true    // respectNoindex - default
        );
      } catch {
        // If config fetch fails (e.g., project doesn't exist yet), this is a first crawl
        // Detect if JS rendering is needed
        let jsRenderingEnabled = false;
        try {
          jsRenderingEnabled = await DetectJSRenderingNeed(url);
          console.log(`JS rendering detection for ${url}: ${jsRenderingEnabled ? 'ENABLED' : 'DISABLED'}`);
        } catch (detectionError) {
          console.warn('JS rendering detection failed, using default (disabled):', detectionError);
        }

        // Create config with detected or default settings
        await UpdateConfigForDomain(
          url,
          jsRenderingEnabled, // jsRendering - auto-detected or default false
          1500,  // initialWaitMs - default
          2000,  // scrollWaitMs - default
          1000,  // finalWaitMs - default
          5,     // parallelism - default
          'bluesnake/1.0 (+https://snake.blue)', // userAgent
          true,  // includeSubdomains - default
          true,  // spiderEnabled - always true
          false, // sitemapEnabled - default
          [],    // sitemapURLs
          true,  // checkExternalResources - default
          false,  // singlePageMode - DISABLE for full crawl
          "respect",  // robotsTxtMode - default
          false,  // followInternalNofollow - default
          false,  // followExternalNofollow - default
          true,   // respectMetaRobotsNoindex - default
          true    // respectNoindex - default
        ).catch(() => {
          // If this also fails, that's okay - config will be created with defaults
        });
      }

      await StartCrawl(url);

      // Immediately load the project and navigate to crawl view
      await loadCurrentProjectFromUrl(url);

      // Set crawling state to start polling
      setIsCrawling(true);

      // Navigate to dashboard
      setDashboardSection('crawl-results');
      setView('dashboard');
    } catch (error) {
      console.error('Failed to start crawl:', error);
      setIsCrawling(false);
    }
  };

  const handleSinglePageCrawl = async () => {
    if (!url.trim()) return;

    try {
      // Close the dropdown
      setIsCrawlTypeDropdownOpen(false);

      // First, get existing config to preserve user settings
      try {
        const existingConfig: ConfigData = await GetConfigForDomain(url);

        // Update config with singlePageMode enabled, preserving other settings
        const mechanisms = existingConfig.discoveryMechanisms || ["spider"];
        await UpdateConfigForDomain(
          url,
          existingConfig.jsRenderingEnabled,
          existingConfig.initialWaitMs || 1500,
          existingConfig.scrollWaitMs || 2000,
          existingConfig.finalWaitMs || 1000,
          existingConfig.parallelism,
          existingConfig.userAgent || 'bluesnake/1.0 (+https://snake.blue)',
          existingConfig.includeSubdomains,
          true,  // spiderEnabled - always true (won't be used in single page mode)
          mechanisms.includes("sitemap"),
          [],    // sitemapURLs
          existingConfig.checkExternalResources,
          true,   // singlePageMode - ENABLE for single page crawl
          "respect",  // robotsTxtMode - default
          false,  // followInternalNofollow - default
          false,  // followExternalNofollow - default
          true,   // respectMetaRobotsNoindex - default
          true    // respectNoindex - default
        );
      } catch {
        // If config fetch fails (e.g., project doesn't exist yet), this is a first crawl
        // Detect if JS rendering is needed
        let jsRenderingEnabled = false;
        try {
          jsRenderingEnabled = await DetectJSRenderingNeed(url);
          console.log(`JS rendering detection for ${url}: ${jsRenderingEnabled ? 'ENABLED' : 'DISABLED'}`);
        } catch (detectionError) {
          console.warn('JS rendering detection failed, using default (disabled):', detectionError);
        }

        // Create config with detected or default settings
        await UpdateConfigForDomain(
          url,
          jsRenderingEnabled, // jsRendering - auto-detected or default false
          1500,  // initialWaitMs - default
          2000,  // scrollWaitMs - default
          1000,  // finalWaitMs - default
          5,     // parallelism - default
          'bluesnake/1.0 (+https://snake.blue)', // userAgent
          true,  // includeSubdomains - default (doesn't matter in single page mode)
          true,  // spiderEnabled - always true (won't be used in single page mode)
          false, // sitemapEnabled - default (won't be used in single page mode)
          [],    // sitemapURLs
          true,  // checkExternalResources - default
          true,   // singlePageMode - ENABLE for single page crawl
          "respect",  // robotsTxtMode - default
          false,  // followInternalNofollow - default
          false,  // followExternalNofollow - default
          true,   // respectMetaRobotsNoindex - default
          true    // respectNoindex - default
        ).catch(() => {
          // If this also fails, that's okay - config will be created with defaults
        });
      }

      await StartCrawl(url);

      // Immediately load the project and navigate to crawl view
      await loadCurrentProjectFromUrl(url);

      // Set crawling state to start polling
      setIsCrawling(true);

      // Navigate to dashboard
      setDashboardSection('crawl-results');
      setView('dashboard');
    } catch (error) {
      console.error('Failed to start single page crawl:', error);
      setIsCrawling(false);
    }
  };

  const handleOpenConfig = () => {
    setDashboardSection('config');
  };

  const handleOpenConfigFromHome = async () => {
    if (!url.trim()) return;

    // Close the dropdown
    setIsCrawlTypeDropdownOpen(false);

    // Try to load the project if it exists
    await loadCurrentProjectFromUrl(url);

    setDashboardSection('config');
    setView('dashboard');
  };

  const handleCloseConfig = async () => {
    // Switch back to crawl results section
    setDashboardSection('crawl-results');
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

      // Set the selected crawl info (pagination will handle loading results)
      if (crawlIdToLoad) {
        const selectedCrawlInfo = crawls.find(c => c.id === crawlIdToLoad);
        if (selectedCrawlInfo) {
          setSelectedCrawl(selectedCrawlInfo);
        }
      }

      setDashboardSection('crawl-results');
      setView('dashboard');
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

      // Find the selected crawl info from available crawls
      const selectedCrawlInfo = availableCrawls.find(c => c.id === crawlId);
      if (selectedCrawlInfo) {
        setSelectedCrawl(selectedCrawlInfo);
      }

      setIsCrawlDropdownOpen(false);
      // Pagination effect will handle loading the results based on currentCrawlId change
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
          setSelectedCrawl(latestCrawl);
          setCurrentCrawlId(latestCrawl.id);
          // Pagination effect will handle loading the results
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
    if (status === 0) return 'status-server-error'; // Network/timeout errors in red
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

  const loadMoreResults = async () => {
    if (!currentCrawlId || !hasMore || isLoadingMore) return;

    setIsLoadingMore(true);
    try {
      const paginatedResults: CrawlResultPaginated = await SearchCrawlResultsPaginated(
        currentCrawlId,
        debouncedSearchQuery,
        contentTypeFilter,
        PAGINATION_LIMIT,
        cursor
      );

      setFilteredResults(prev => [...prev, ...paginatedResults.results]);
      setCursor(paginatedResults.nextCursor);
      setHasMore(paginatedResults.hasMore);
    } catch (error) {
      console.error('Failed to load more results:', error);
    } finally {
      setIsLoadingMore(false);
    }
  };

  const handleColumnToggle = (column: string) => {
    setVisibleColumns(prev => ({
      ...prev,
      [column]: !prev[column]
    }));
  };

  // Helper function to generate grid template based on visible columns
  const getGridTemplate = () => {
    const columnSizes: Record<string, string> = {
      url: '2fr',
      status: '100px',
      title: '2fr',
      metaDescription: '2fr',
      indexable: '100px',
      contentType: '120px'
    };

    const visibleCols = Object.entries(visibleColumns)
      .filter(([_, isVisible]) => isVisible)
      .map(([col, _]) => columnSizes[col]);

    return visibleCols.join(' ');
  };

  // Infinite scroll effect
  useEffect(() => {
    const resultsBody = resultsBodyRef.current;
    if (!resultsBody) return;

    const handleScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = resultsBody;
      // Trigger load more when user is within 200px of the bottom
      if (scrollHeight - scrollTop - clientHeight < 200) {
        loadMoreResults();
      }
    };

    resultsBody.addEventListener('scroll', handleScroll);
    return () => resultsBody.removeEventListener('scroll', handleScroll);
  }, [hasMore, isLoadingMore, cursor, currentCrawlId, debouncedSearchQuery, contentTypeFilter]);

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
          <p className="subtitle">AI-ready Crawler. For those who never compromise</p>

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
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              spellCheck={false}
            />
            <div className="split-button-container" ref={crawlTypeDropdownRef}>
              <button
                className="go-button"
                onClick={handleNewCrawl}
                disabled={!url.trim()}
                title="Start full website crawl"
              >
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="5" y1="12" x2="19" y2="12"></line>
                  <polyline points="12 5 19 12 12 19"></polyline>
                </svg>
              </button>
              <button
                className="go-button-dropdown"
                onClick={() => setIsCrawlTypeDropdownOpen(!isCrawlTypeDropdownOpen)}
                disabled={!url.trim()}
                title="More crawl options"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="6 9 12 15 18 9"></polyline>
                </svg>
              </button>
              {isCrawlTypeDropdownOpen && (
                <div className="crawl-type-dropdown">
                  <div className="crawl-type-option" onClick={handleNewCrawl}>
                    <div className="crawl-type-option-content">
                      <span className="crawl-type-option-title">Full Website Crawl</span>
                      <span className="crawl-type-option-desc">Discover and crawl pages by following links and sitemaps</span>
                    </div>
                  </div>
                  <div className="crawl-type-option" onClick={handleSinglePageCrawl}>
                    <div className="crawl-type-option-content">
                      <span className="crawl-type-option-title">Single Page Crawl</span>
                      <span className="crawl-type-option-desc">Analyze only this specific URL without following any links</span>
                    </div>
                  </div>
                  <div className="crawl-type-option" onClick={handleOpenConfigFromHome}>
                    <div className="crawl-type-option-content">
                      <span className="crawl-type-option-title">Configure And Crawl</span>
                      <span className="crawl-type-option-desc">Customize settings before starting your crawl</span>
                    </div>
                  </div>
                </div>
              )}
            </div>
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
                              crawled={activeCrawl.totalUrlsCrawled}
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
                              <span className="project-stat-value">{project.totalUrls}</span>
                              <span className="project-stat-label">total URLs</span>
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

  // Dashboard screen
  const hasNoCrawls = availableCrawls.length === 0;

  return (
    <div className="app">
      <div className="dashboard-container">
        <Sidebar
          activeSection={dashboardSection}
          onSectionChange={setDashboardSection}
          onHomeClick={handleHome}
        />
        <div className="dashboard-main">
          {dashboardSection === 'config' ? (
            <Config url={url} onClose={handleCloseConfig} />
          ) : dashboardSection === 'ai-crawlers' ? (
            <AICrawlers url={url} />
          ) : (
            <div className="crawl-screen">
        <div className="header">
          <div className="header-left">
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
              <div className="header-split-button-container" ref={crawlTypeDropdownRef}>
                <button className="new-crawl-button" onClick={handleNewCrawl} disabled={isCrawling} title="Start full website crawl">
                  New Crawl
                </button>
                <button
                  className="new-crawl-button-dropdown"
                  onClick={() => setIsCrawlTypeDropdownOpen(!isCrawlTypeDropdownOpen)}
                  disabled={isCrawling}
                  title="More crawl options"
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <polyline points="6 9 12 15 18 9"></polyline>
                  </svg>
                </button>
                {isCrawlTypeDropdownOpen && (
                  <div className="crawl-type-dropdown">
                    <div className="crawl-type-option" onClick={handleNewCrawl}>
                      <div className="crawl-type-option-content">
                        <span className="crawl-type-option-title">Full Website Crawl</span>
                        <span className="crawl-type-option-desc">Discover and crawl pages by following links and sitemaps</span>
                      </div>
                    </div>
                    <div className="crawl-type-option" onClick={handleSinglePageCrawl}>
                      <div className="crawl-type-option-content">
                        <span className="crawl-type-option-title">Single Page Crawl</span>
                        <span className="crawl-type-option-desc">Analyze only this specific URL without following any links</span>
                      </div>
                    </div>
                  </div>
                )}
              </div>
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
            <div className="content-type-filter">
              <button
                className={`filter-tab ${contentTypeFilter === 'all' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('all')}
              >
                All ({(isCrawling ? activeCrawlStats : crawlStats)?.total || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'html' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('html')}
              >
                HTML ({(isCrawling ? activeCrawlStats : crawlStats)?.html || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'javascript' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('javascript')}
              >
                JavaScript ({(isCrawling ? activeCrawlStats : crawlStats)?.javascript || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'css' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('css')}
              >
                CSS ({(isCrawling ? activeCrawlStats : crawlStats)?.css || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'image' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('image')}
              >
                Images ({(isCrawling ? activeCrawlStats : crawlStats)?.images || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'font' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('font')}
              >
                Fonts ({(isCrawling ? activeCrawlStats : crawlStats)?.fonts || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'unvisited' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('unvisited')}
              >
                Unvisited ({(isCrawling ? activeCrawlStats : crawlStats)?.unvisited || 0})
              </button>
              <button
                className={`filter-tab ${contentTypeFilter === 'other' ? 'active' : ''}`}
                onClick={() => setContentTypeFilter('other')}
              >
                Others ({(isCrawling ? activeCrawlStats : crawlStats)?.others || 0})
              </button>
              <ColumnSelector
                visibleColumns={visibleColumns}
                onColumnToggle={handleColumnToggle}
              />
            </div>
            <div className="results-header" style={{ gridTemplateColumns: getGridTemplate() }}>
              {visibleColumns.url && <div className="header-cell url-col">URL</div>}
              {visibleColumns.status && <div className="header-cell status-col">Status</div>}
              {visibleColumns.title && <div className="header-cell title-col">Title</div>}
              {visibleColumns.metaDescription && <div className="header-cell meta-desc-col">Meta Description</div>}
              {visibleColumns.indexable && <div className="header-cell indexable-col">Indexable</div>}
              {visibleColumns.contentType && <div className="header-cell content-type-col">Type</div>}
            </div>

            <div className="results-body" ref={resultsBodyRef}>
              {filteredResults.map((result, index) => {
                const isInProgress = result.status === 0 && result.title === 'In progress...';
                const isUnvisitedURL = result.status === 0 && result.title === 'Unvisited URL';
                const isClickable = !isInProgress && !isUnvisitedURL;
                return (
                  <div
                    key={index}
                    className="result-row"
                    onClick={() => isClickable && handleUrlClick(result.url)}
                    style={{ cursor: isClickable ? 'pointer' : 'default', gridTemplateColumns: getGridTemplate() }}
                    title={isClickable ? 'Click row to view internal links' : ''}
                  >
                    {visibleColumns.url && (
                      <div className="result-cell url-col">
                        <span
                          className="url-link"
                          style={{ opacity: isClickable ? 1 : 0.6 }}
                          onClick={(e) => {
                            if (isClickable) {
                              e.stopPropagation();
                              handleOpenUrl(result.url);
                            }
                          }}
                          title={isClickable ? 'Click to open URL in browser' : ''}
                        >
                          {result.url}
                        </span>
                      </div>
                    )}
                    {visibleColumns.status && (
                      <div className={`result-cell status-col ${getStatusColor(result.status)}`} style={{ opacity: isClickable ? 1 : 0.6 }}>
                        {isInProgress ? 'Queued' : isUnvisitedURL ? 'Not visited' : (result.error ? 'Error' : result.status)}
                      </div>
                    )}
                    {visibleColumns.title && (
                      <div className="result-cell title-col" style={{ opacity: isClickable ? 1 : 0.6 }}>
                        {result.error ? result.error : result.title || '(no title)'}
                      </div>
                    )}
                    {visibleColumns.metaDescription && (
                      <div className="result-cell meta-desc-col" style={{ opacity: isClickable ? 1 : 0.6 }} title={result.metaDescription || ''}>
                        {result.metaDescription || '-'}
                      </div>
                    )}
                    {visibleColumns.indexable && (
                      <div className="result-cell indexable-col" style={{ opacity: isClickable ? 1 : 0.6 }}>
                        <span className={`indexable-badge ${result.indexable === 'Yes' ? 'indexable-yes' : 'indexable-no'}`}>
                          {result.indexable}
                        </span>
                      </div>
                    )}
                    {visibleColumns.contentType && (
                      <div className="result-cell content-type-col" style={{ opacity: isClickable ? 1 : 0.6 }}>
                        <span className={`content-type-badge content-type-${categorizeContentType(result.contentType)}`}>
                          {getContentTypeDisplay(result.contentType)}
                        </span>
                      </div>
                    )}
                  </div>
                );
              })}
              {isLoadingMore && (
                <div className="loading-more-indicator">
                  <SmallLoadingSpinner />
                  <span>Loading more results...</span>
                </div>
              )}
              {!isLoadingMore && hasMore && filteredResults.length > 0 && (
                <div className="load-more-trigger" style={{ height: '1px' }}></div>
              )}
            </div>
          </div>
        )}

        {!hasNoCrawls && (
          <div className="footer">
            <div className="footer-content">
              {isCrawling && currentProject && (
                <div className="status-indicator">
                  <CircularProgress
                    crawled={activeCrawlStats ? activeCrawlStats.crawled : 0}
                    total={activeCrawlStats ? activeCrawlStats.total : 0}
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
                  <span className="stat-label">Total:</span>
                  <span className="stat-value">{(isCrawling ? activeCrawlStats : crawlStats)?.total || 0}</span>
                </span>
                <span className="stat-item">
                  <span className="stat-label">Crawled:</span>
                  <span className="stat-value">{(isCrawling ? activeCrawlStats : crawlStats)?.crawled || 0}</span>
                </span>
                <span className="stat-item">
                  <span className="stat-label">Unvisited:</span>
                  <span className="stat-value">{(isCrawling ? activeCrawlStats : crawlStats)?.unvisited || 0}</span>
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
          )}
        </div>
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
