/* Thin bridge to the Wails backend. Bound Go methods live on
   window.go.main.App; runtime events on window.runtime. */

function backend() {
  if (!window.go || !window.go.main || !window.go.main.App) {
    throw new Error("backend not available — run inside the Wails app (wails dev)");
  }
  return window.go.main.App;
}

const call = (method, ...args) => backend()[method](...args);

export const api = {
  listCrawls: () => call("ListCrawls"),
  deleteCrawl: (id) => call("DeleteCrawl", id),
  startCrawl: (req) => call("StartCrawl", req),
  resumeCrawl: (id) => call("ResumeCrawl", id),
  pauseCrawl: () => call("PauseCrawl"),
  stopCrawl: () => call("StopCrawl"),
  activeProgress: () => call("ActiveProgress"),

  overview: (id) => call("Overview", id),
  dataset: (id, tab, filterIssue, limit) => call("Dataset", id, tab, filterIssue, limit),
  datasetCounts: (id) => call("DatasetCounts", id),
  issueSummary: (id) => call("IssueSummary", id),
  pageDetail: (id, url) => call("PageDetail", id, url),
  exportDataset: (id, tab, filterIssue, format) => call("ExportDataset", id, tab, filterIssue, format),
  generateSitemap: (id) => call("GenerateSitemap", id),
  reanalyze: (id) => call("Reanalyze", id),

  testRobots: (robotsTxt, token, urls) => call("TestRobots", robotsTxt, token, urls),
  fetchRobots: (site) => call("FetchRobots", site),
  compareCrawls: (prevId, currId) => call("CompareCrawls", prevId, currId),

  listProfiles: () => call("ListProfiles"),
  duplicateProfile: (src, dst) => call("DuplicateProfile", src, dst),
  getProfileConfig: (profile) => call("GetProfileConfig", profile).then((s) => JSON.parse(s)),
  getConfigValues: (profile, keys) => call("GetConfigValues", profile, keys),
  setConfigValues: (profile, vals) => call("SetConfigValues", profile, vals),
  getProfileYAML: (profile) => call("GetProfileYAML", profile),
  saveProfileYAML: (profile, content) => call("SaveProfileYAML", profile, content),
  storageInfo: () => call("GetStorageInfo"),

  getMCPStatus: () => call("GetMCPStatus"),
  setMCPEnabled: (enabled) => call("SetMCPEnabled", enabled),
  setMCPPort: (port) => call("SetMCPPort", port),

  getTunnelStatus: () => call("GetTunnelStatus"),
  setTunnelEnabled: (enabled) => call("SetTunnelEnabled", enabled),

  cliInfo: () => call("CLIInfo"),
  installCLI: () => call("InstallCLI"),
  cliPromptSeen: () => call("CLIPromptSeen"),
  markCliPromptSeen: () => call("MarkCLIPromptSeen"),

  appVersion: () => call("AppVersion"),
  checkForUpdate: () => call("CheckForUpdate"), // cached per session
  refreshUpdate: () => call("RefreshUpdate"),   // forces a fresh network check
  applyUpdate: () => call("ApplyUpdate"),
  skipUpdate: (version) => call("SkipUpdate", version),
  getUpdatePrefs: () => call("GetUpdatePrefs"),
  setUpdateAutoCheck: (enabled) => call("SetUpdateAutoCheck", enabled),
};

/* Opt-in project layer (competitor study). Bound as a SEPARATE Go struct
   (window.go.main.ProjectApp) so this whole block — and the feature — can be
   deleted without touching the core api above. */
function projectBackend() {
  if (!window.go || !window.go.main || !window.go.main.ProjectApp) {
    throw new Error("project backend not available — run inside the Wails app (wails dev)");
  }
  return window.go.main.ProjectApp;
}
const pcall = (method, ...args) => projectBackend()[method](...args);
export const projectApi = {
  list: () => pcall("ListProjects"),
  create: (name, mainDomain) => pcall("CreateProject", name, mainDomain),
  rename: (id, name) => pcall("RenameProject", id, name),
  remove: (id) => pcall("DeleteProject", id),
  addCompetitor: (id, domain) => pcall("AddCompetitor", id, domain),
  removeCompetitor: (id, domain) => pcall("RemoveCompetitor", id, domain),
  sites: (id) => pcall("ProjectSites", id),
  comparison: (id, includeOptional) => pcall("ProjectComparison", id, includeOptional),
  diff: (id, domain) => pcall("ProjectDiff", id, domain),
};

/* Open a URL in the user's default browser (Wails runtime), falling back to a
   normal window.open outside the app. */
export function openURL(url) {
  if (window.runtime && window.runtime.BrowserOpenURL) window.runtime.BrowserOpenURL(url);
  else window.open(url, "_blank");
}

/* Copy text to the clipboard via the Wails runtime, falling back to the web
   Clipboard API in a plain browser. Returns a promise that resolves on success.
   The whole app sets `user-select: none`, so this is how any displayed URL/path
   gets onto the clipboard. */
export function copyToClipboard(text) {
  if (!text) return Promise.resolve(false);
  if (window.runtime && window.runtime.ClipboardSetText) {
    return Promise.resolve(window.runtime.ClipboardSetText(text)).then(() => true);
  }
  if (navigator.clipboard) return navigator.clipboard.writeText(text).then(() => true);
  return Promise.resolve(false);
}

/* runtime events — returns an unsubscribe function for THIS listener only
   (EventsOn returns a cancel fn; EventsOff would drop every listener) */
export function on(event, cb) {
  if (!window.runtime) return () => {};
  const cancel = window.runtime.EventsOn(event, cb);
  return typeof cancel === "function" ? cancel : () => window.runtime.EventsOff(event);
}

export const urlShort = (u) => (u || "").replace(/^https?:\/\/(www\.)?/, "");
export const hostOf = (u) => urlShort(u).replace(/\/.*$/, "");

/* Brand favicons. The backend fetches each logo from Google once and caches it
   to disk; we memoise the result per host for this session so a logo costs at
   most one backend round-trip no matter how many places render it. Resolves to
   a data: URL, or "" meaning "no logo — fall back to the domain initial". */
const _logoCache = new Map(); // host -> dataURL ("")
const _logoInflight = new Map(); // host -> Promise<dataURL>
export function brandLogo(seed) {
  const host = hostOf(seed);
  if (!host) return Promise.resolve("");
  if (_logoCache.has(host)) return Promise.resolve(_logoCache.get(host));
  if (_logoInflight.has(host)) return _logoInflight.get(host);
  const p = Promise.resolve()
    .then(() => call("BrandLogo", seed))
    .then((dataUrl) => { const v = dataUrl || ""; _logoCache.set(host, v); _logoInflight.delete(host); return v; })
    .catch(() => { _logoInflight.delete(host); return ""; });
  _logoInflight.set(host, p);
  return p;
}
