/* ===========================================================================
   bluesnake — app shell + router (Wails frontend)
   =========================================================================== */
import React, { useEffect, useState, useCallback } from "react";
import ReactDOM from "react-dom/client";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "./styles.css";
import { api, on, urlShort, hostOf } from "./api";
import { Icon, IconBtn, BrandMark } from "./ui";
import { CrawlManager } from "./views/home";
import { Welcome } from "./views/welcome";
import { NewCrawl } from "./views/newcrawl";
import { CrawlProgress } from "./views/progress";
import { ResultsWorkspace } from "./views/results-shell";
import { UrlDetail } from "./views/detail";
import { SettingsView } from "./views/settings";
import { CompareView } from "./views/compare";
import { RobotsTester } from "./views/robots";
import { MCPControls } from "./mcp-controls";

/* Windows caption buttons. The window is frameless on Windows (desktop/main.go),
   so we drive minimise/maximise/close through the Wails runtime ourselves. The
   container is no-drag so clicks aren't swallowed by the draggable title bar. */
function WinControls() {
  const rt = () => window.runtime || {};
  return (
    <div className="win-controls">
      <button title="Minimise" onClick={() => rt().WindowMinimise && rt().WindowMinimise()}>
        <Icon name="minus" size={15} />
      </button>
      <button title="Maximise" onClick={() => rt().WindowToggleMaximise && rt().WindowToggleMaximise()}>
        <Icon name="square" size={12} />
      </button>
      <button className="close" title="Close" onClick={() => rt().Quit && rt().Quit()}>
        <Icon name="x" size={16} />
      </button>
    </div>
  );
}

function App() {
  const [dark, setDark] = useState(() => localStorage.getItem("bluesnake-theme") === "dark");
  const [view, setView] = useState("home");
  const [crawls, setCrawls] = useState([]);
  const [crawlsLoaded, setCrawlsLoaded] = useState(false); // gates the first-run welcome until we know
  const [activeCrawl, setActiveCrawl] = useState(null);
  const [liveCrawlId, setLiveCrawlId] = useState(null);
  const [resultsTab, setResultsTab] = useState("internal");
  const [settingsProfile, setSettingsProfile] = useState("Default audit");
  const [detail, setDetail] = useState(null); // {crawlId, url}
  const [issueFilter, setIssueFilter] = useState(null); // {id, name}
  const [collapsed, setCollapsed] = useState(false);
  const [storage, setStorage] = useState(null);
  const [settingsFocus, setSettingsFocus] = useState(null); // {section} -> open Settings on it
  const [settingsBack, setSettingsBack] = useState(null); // {view,label} -> show a "back" button in Settings
  const [platform, setPlatform] = useState(""); // "windows" | "darwin" | "linux" — drives window-chrome layout

  // The Windows window is frameless (see desktop/main.go), so we draw our own
  // caption buttons; macOS keeps its native traffic lights. Resolve the host OS
  // once from the Wails runtime.
  useEffect(() => {
    if (window.runtime && window.runtime.Environment) {
      window.runtime.Environment().then((e) => setPlatform(e.platform)).catch(() => {});
    }
  }, []);
  const isWindows = platform === "windows";

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
    localStorage.setItem("bluesnake-theme", dark ? "dark" : "light");
  }, [dark]);

  const refresh = useCallback(() => {
    api.listCrawls().then((cs) => { setCrawls(cs || []); setCrawlsLoaded(true); }).catch(() => setCrawlsLoaded(true));
    api.storageInfo().then(setStorage).catch(() => {});
  }, []);

  useEffect(() => {
    refresh();
    // a crawl may already be running (e.g. after a frontend reload)
    api.activeProgress().then((p) => {
      if (p && p.state === "running") { setLiveCrawlId(p.crawlId); setView("progress"); }
    }).catch(() => {});
    const offDone = on("crawl:done", () => refresh());
    // crawls started over MCP (by an LLM) take over the screen like any other
    const offStarted = on("crawl:started", (id) => { setLiveCrawlId(id); setView("progress"); refresh(); });
    return () => { offDone(); offStarted(); };
  }, [refresh]);

  async function startCrawl(req) {
    const id = await api.startCrawl(req);
    setLiveCrawlId(id);
    setView("progress");
    refresh();
  }
  // first-run welcome: a bare domain → a sensible default spider crawl
  function startFromWelcome(rawUrl) {
    let u = (rawUrl || "").trim();
    if (u && !/^https?:\/\//i.test(u)) u = "https://" + u;
    return startCrawl({
      mode: "spider", url: u, listUrls: [], sitemapUrl: "", project: "",
      profile: "Default audit", threads: 5, rate: 2, maxDepth: -1, rendering: "text",
    });
  }
  async function resumeCrawl(c) {
    const id = await api.resumeCrawl(c.id);
    setLiveCrawlId(id);
    setView("progress");
    refresh();
  }
  function openCrawl(c) {
    setActiveCrawl(c);
    setResultsTab("internal");
    setIssueFilter(null);
    setView("results");
  }
  function finishToResults(crawlId) {
    const c = crawls.find((x) => x.id === crawlId);
    setActiveCrawl(c || { id: crawlId, seed: "", crawled: 0 });
    setResultsTab("overview");
    setIssueFilter(null);
    setView("results");
    refresh();
  }
  async function deleteCrawl(c) {
    await api.deleteCrawl(c.id);
    if (activeCrawl && activeCrawl.id === c.id) { setActiveCrawl(null); setView("home"); }
    refresh();
  }
  function openDataset(tabId, filter) {
    setResultsTab(tabId);
    setIssueFilter(filter || null);
    setView("results");
  }

  const nav = [
    { id: "home", label: "Crawls", icon: "layout-grid", count: crawls.length },
    { id: "compare", label: "Compare", icon: "git-compare" },
    { id: "robots", label: "robots.txt Tester", icon: "bot" },
    { id: "settings", label: "Settings & Profiles", icon: "sliders-horizontal" },
  ];

  // Titlebar host follows what's on screen: the live crawl while one is
  // running (so a fresh crawl's domain shows immediately, not the last one
  // you opened), otherwise the crawl whose results you're viewing.
  const liveCrawl = crawls.find((c) => c.id === liveCrawlId);
  const titleHost = view === "progress"
    ? (liveCrawl ? hostOf(liveCrawl.seed) : "crawling…")
    : (activeCrawl ? hostOf(activeCrawl.seed) : "no crawl");

  return (
    <div className="win">
      {/* title bar — macOS hosts the native traffic lights in the left inset;
          Windows is frameless, so the left inset collapses and we draw our own
          caption buttons (WinControls) on the right. */}
      <div
        className={"titlebar" + (isWindows ? " win" : "")}
        onDoubleClick={isWindows ? () => window.runtime && window.runtime.WindowToggleMaximise() : undefined}
      >
        <span className="tb-nodrag">
          <IconBtn icon={collapsed ? "panel-left-open" : "panel-left-close"} title={collapsed ? "Show sidebar" : "Hide sidebar"} onClick={() => setCollapsed((c) => !c)} />
        </span>
        <div className="tb-title">
          <img src="/logo.png" alt="" width={16} height={16} draggable={false} style={{ display: "block", borderRadius: 3 }} />
          bluesnake
          <span className="dot" />
          <span style={{ color: "var(--ink-faint)", fontWeight: 500, whiteSpace: "nowrap" }} className="mono">
            {titleHost}
          </span>
        </div>
        <div className="tb-spacer" />
        <div className="tb-actions">
          <MCPControls onOpenSettings={() => { setSettingsFocus({ section: "mcp" }); setSettingsBack(null); setView("settings"); }} />
          <IconBtn icon={dark ? "sun" : "moon"} title="Toggle theme" onClick={() => setDark((d) => !d)} />
        </div>
        {isWindows && <WinControls />}
      </div>

      {/* body */}
      <div className="body">
        {/* sidebar */}
        <div className={"sidebar" + (collapsed ? " collapsed" : "")}>
          <div className="sb-top">
            {collapsed
              ? <button className="btn-newcrawl" title="New Crawl" onClick={() => setView("new")} style={{ width: 34, height: 34, padding: 0 }}><Icon name="plus" size={16} /></button>
              : <button className="btn-newcrawl" onClick={() => setView("new")}><Icon name="plus" size={15} />New Crawl</button>}
          </div>
          <div className="sb-nav">
            {nav.map((n) => (
              <div key={n.id} className={"sb-item" + (view === n.id ? " active" : "")} onClick={() => { if (n.id === "settings") setSettingsBack(null); setView(n.id); }} title={collapsed ? n.label : null}>
                <Icon name={n.icon} size={15} />
                {!collapsed && <>{n.label}{n.count != null && <span className="count">{n.count}</span>}</>}
              </div>
            ))}
          </div>
          {!collapsed && <div className="sb-sectlabel">Recent crawls</div>}
          {collapsed && <div style={{ height: 1, background: "var(--border-soft)", margin: "8px 12px" }} />}
          <div className="sb-recents">
            {crawls.map((c) => {
              const active = view === "results" && activeCrawl && activeCrawl.id === c.id;
              const dotc = c.status === "completed" ? "var(--sev-ok)" : c.status === "interrupted" ? "var(--sev-warn)" : "var(--accent)";
              const host = hostOf(c.seed);
              if (collapsed) return (
                <button key={c.id} className="sb-monogram" title={host + " · " + c.crawled.toLocaleString() + " URLs"} onClick={() => openCrawl(c)}>
                  <BrandMark seed={c.seed} size={34} dot={dotc} active={active} />
                </button>
              );
              return (
                <div key={c.id} className={"sb-crawl" + (active ? " active" : "")} onClick={() => openCrawl(c)}>
                  <BrandMark seed={c.seed} size={26} dot={dotc} />
                  <div className="meta">
                    <div className="host">{host}</div>
                    <div className="sub">{(c.total || c.crawled).toLocaleString()} URLs{c.started ? " · " + c.started.split(" ")[0] : ""}</div>
                  </div>
                </div>
              );
            })}
          </div>
          <div className="sb-foot" style={collapsed ? { justifyContent: "center" } : null}>
            {!collapsed && <>
              <Icon name="hard-drive" size={14} style={{ color: "var(--ink-faint)" }} />
              <div style={{ fontSize: 10.5, color: "var(--ink-faint)", lineHeight: 1.3 }}>
                <div className="mono">{storage ? storage.dir.replace(/^\/Users\/[^/]+/, "~") : "~/.bluesnake"}</div>
                <div>{storage ? `${storage.sizeMB >= 1024 ? (storage.sizeMB / 1024).toFixed(1) + " GB" : storage.sizeMB + " MB"} · ${storage.crawls} crawls` : ""}</div>
              </div>
              <div style={{ flex: 1 }} />
            </>}
            <IconBtn icon="settings" title="Settings" onClick={() => { setSettingsBack(null); setView("settings"); }} />
          </div>
        </div>

        {/* main routed content */}
        {view === "home" && crawlsLoaded && (
          crawls.length === 0
            ? <Welcome onStart={startFromWelcome} onConfigure={() => setView("new")}
                onMcp={() => { setSettingsFocus({ section: "mcp" }); setSettingsBack({ view: "home", label: "Back" }); setView("settings"); }} />
            : <CrawlManager crawls={crawls} onOpen={openCrawl} onResume={resumeCrawl} onCompare={() => setView("compare")} onNew={() => setView("new")} onDelete={deleteCrawl} storage={storage} />
        )}
        {view === "new" && <NewCrawl onStart={startCrawl} onOpenSettings={(p) => { setSettingsProfile(p); setSettingsBack({ view: "new", label: "New Crawl" }); setView("settings"); }} />}
        {view === "progress" && <CrawlProgress crawlId={liveCrawlId} onOpenResults={finishToResults} />}
        {view === "results" && activeCrawl && (
          <ResultsWorkspace
            crawl={activeCrawl}
            tab={resultsTab}
            setTab={(id) => { setResultsTab(id); setIssueFilter(null); }}
            issueFilter={issueFilter}
            setIssueFilter={setIssueFilter}
            onOpenDetail={(url) => setDetail({ crawlId: activeCrawl.id, url })}
            onFilterByIssue={openDataset}
          />
        )}
        {view === "settings" && <SettingsView profileName={settingsProfile} focus={settingsFocus}
          onBack={settingsBack ? () => setView(settingsBack.view) : null} backLabel={settingsBack ? settingsBack.label : null} />}
        {view === "compare" && <CompareView crawls={crawls} />}
        {view === "robots" && <RobotsTester />}
      </div>

      {/* URL detail drawer */}
      {detail && (
        <UrlDetail crawlId={detail.crawlId} url={detail.url}
          onClose={() => setDetail(null)}
          onFilterByIssue={(f) => { setDetail(null); openDataset("internal", f); }} />
      )}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
