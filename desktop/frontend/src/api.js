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
};

/* runtime events — returns an unsubscribe function for THIS listener only
   (EventsOn returns a cancel fn; EventsOff would drop every listener) */
export function on(event, cb) {
  if (!window.runtime) return () => {};
  const cancel = window.runtime.EventsOn(event, cb);
  return typeof cancel === "function" ? cancel : () => window.runtime.EventsOff(event);
}

export const urlShort = (u) => (u || "").replace(/^https?:\/\/(www\.)?/, "");
export const hostOf = (u) => urlShort(u).replace(/\/.*$/, "");
