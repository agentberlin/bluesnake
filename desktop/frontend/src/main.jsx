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
import { Icon, IconBtn } from "./ui";
import { CrawlManager } from "./views/home";
import { NewCrawl } from "./views/newcrawl";
import { CrawlProgress } from "./views/progress";
import { ResultsWorkspace } from "./views/results-shell";
import { UrlDetail } from "./views/detail";
import { SettingsView } from "./views/settings";
import { CompareView } from "./views/compare";
import { RobotsTester } from "./views/robots";

function App() {
  const [dark, setDark] = useState(() => localStorage.getItem("bluesnake-theme") === "dark");
  const [view, setView] = useState("home");
  const [crawls, setCrawls] = useState([]);
  const [activeCrawl, setActiveCrawl] = useState(null);
  const [liveCrawlId, setLiveCrawlId] = useState(null);
  const [resultsTab, setResultsTab] = useState("internal");
  const [settingsProfile, setSettingsProfile] = useState("Default audit");
  const [detail, setDetail] = useState(null); // {crawlId, url}
  const [issueFilter, setIssueFilter] = useState(null); // {id, name}
  const [collapsed, setCollapsed] = useState(false);
  const [storage, setStorage] = useState(null);
  const [mcp, setMcp] = useState(null); // MCPStatus
  const [settingsFocus, setSettingsFocus] = useState(null); // {section} -> open Settings on it

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
    localStorage.setItem("bluesnake-theme", dark ? "dark" : "light");
  }, [dark]);

  const refresh = useCallback(() => {
    api.listCrawls().then((cs) => setCrawls(cs || [])).catch(() => {});
    api.storageInfo().then(setStorage).catch(() => {});
  }, []);

  useEffect(() => {
    refresh();
    // a crawl may already be running (e.g. after a frontend reload)
    api.activeProgress().then((p) => {
      if (p && p.state === "running") { setLiveCrawlId(p.crawlId); setView("progress"); }
    }).catch(() => {});
    api.getMCPStatus().then(setMcp).catch(() => {});
    const offDone = on("crawl:done", () => refresh());
    const offMcp = on("mcp:status", setMcp);
    // crawls started over MCP (by an LLM) take over the screen like any other
    const offStarted = on("crawl:started", (id) => { setLiveCrawlId(id); setView("progress"); refresh(); });
    return () => { offDone(); offMcp(); offStarted(); };
  }, [refresh]);

  async function startCrawl(req) {
    const id = await api.startCrawl(req);
    setLiveCrawlId(id);
    setView("progress");
    refresh();
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

  return (
    <div className="win">
      {/* title bar (native traffic lights sit in the left inset) */}
      <div className="titlebar">
        <span className="tb-nodrag">
          <IconBtn icon={collapsed ? "panel-left-open" : "panel-left-close"} title={collapsed ? "Show sidebar" : "Hide sidebar"} onClick={() => setCollapsed((c) => !c)} />
        </span>
        <div className="tb-title">
          <Icon name="bug" size={15} style={{ color: "var(--ink-3)" }} />
          bluesnake
          <span className="dot" />
          <span style={{ color: "var(--ink-faint)", fontWeight: 500 }} className="mono">
            {activeCrawl ? hostOf(activeCrawl.seed) : "no crawl"}
          </span>
        </div>
        <div className="tb-spacer" />
        <div className="tb-actions">
          <button
            className="pill tb-nodrag"
            title={mcp && mcp.running ? `MCP server running — ${mcp.endpoint}` : "MCP server off — click to set up LLM access"}
            onClick={() => { setSettingsFocus({ section: "mcp" }); setView("settings"); }}
            style={{ height: 22, cursor: "pointer", gap: 6, fontSize: 11, color: "var(--ink-2)", background: "transparent" }}
          >
            <Icon name="plug-zap" size={12} />
            MCP
            <span className="statusdot" style={{ background: mcp && mcp.running ? "var(--sev-ok)" : "var(--ink-faint)" }} />
          </button>
          <IconBtn icon={dark ? "sun" : "moon"} title="Toggle theme" onClick={() => setDark((d) => !d)} />
        </div>
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
              <div key={n.id} className={"sb-item" + (view === n.id ? " active" : "")} onClick={() => setView(n.id)} title={collapsed ? n.label : null}>
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
                <button key={c.id} className={"sb-monogram" + (active ? " active" : "")} title={host + " · " + c.crawled.toLocaleString() + " URLs"} onClick={() => openCrawl(c)}>
                  {(host.charAt(0) || "?").toUpperCase()}
                  <span className="statusdot" style={{ background: dotc }} />
                </button>
              );
              return (
                <div key={c.id} className={"sb-crawl" + (active ? " active" : "")} onClick={() => openCrawl(c)}>
                  <span className="statusdot" style={{ background: dotc, marginTop: 5, alignSelf: "flex-start" }} />
                  <div className="meta">
                    <div className="host">{host}</div>
                    <div className="sub">{c.crawled.toLocaleString()} URLs{c.started ? " · " + c.started.split(" ")[0] : ""}</div>
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
            <IconBtn icon="settings" title="Settings" onClick={() => setView("settings")} />
          </div>
        </div>

        {/* main routed content */}
        {view === "home" && <CrawlManager crawls={crawls} onOpen={openCrawl} onResume={resumeCrawl} onCompare={() => setView("compare")} onNew={() => setView("new")} onDelete={deleteCrawl} storage={storage} />}
        {view === "new" && <NewCrawl onStart={startCrawl} onOpenSettings={(p) => { setSettingsProfile(p); setView("settings"); }} />}
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
        {view === "settings" && <SettingsView profileName={settingsProfile} focus={settingsFocus} />}
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
