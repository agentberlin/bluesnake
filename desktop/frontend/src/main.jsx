/* ===========================================================================
   bluesnake — app shell + router (Wails frontend)
   =========================================================================== */
import React, { useEffect, useState, useCallback } from "react";
import ReactDOM from "react-dom/client";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource/jetbrains-mono/600.css";
import "./styles.css";
import { api, on, urlShort, hostOf, openURL } from "./api";
import { Icon, IconBtn, BrandMark, Modal, Btn, CopyButton } from "./ui";
import { CrawlManager } from "./views/home";
import { Welcome } from "./views/welcome";
import { NewCrawl } from "./views/newcrawl";
import { ResultsWorkspace } from "./views/results-shell";
import { UrlDetail } from "./views/detail";
import { SettingsView } from "./views/settings";
import { CompareView } from "./views/compare";
import { ProjectsView } from "./views/projects";
import { RobotsTester } from "./views/robots";
import { MCPControls } from "./mcp-controls";

/* Resolve the host OS synchronously from the WebView user agent. The webview
   engine is OS-bound (WebView2 ⇒ Windows, WKWebView ⇒ macOS, WebKitGTK ⇒ Linux),
   so this is reliable and — unlike Wails' async window.runtime.Environment() —
   available on the very first render. Only macOS needs special chrome: its window
   is borderless (TitleBarHiddenInset) so our custom bar IS the title bar and must
   leave room for the traffic lights. Windows/Linux use the native title bar
   (desktop/main.go), so the custom bar is just a toolbar and needs no platform
   tricks. Environment() still runs as the authoritative refinement (see App). */
function detectPlatform() {
  const ua = (typeof navigator !== "undefined" && navigator.userAgent) || "";
  if (/Windows/i.test(ua)) return "windows";
  if (/Mac OS X|Macintosh/i.test(ua)) return "darwin";
  if (/Linux/i.test(ua)) return "linux";
  return "";
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
  const [platform, setPlatform] = useState(detectPlatform); // "windows" | "darwin" | "linux" — drives window-chrome layout
  const [showCliPrompt, setShowCliPrompt] = useState(false); // first-launch "install the CLI?" prompt (shown once)
  const [update, setUpdate] = useState(null); // UpdateStatus from the launch check
  const [showUpdate, setShowUpdate] = useState(false); // update modal open

  // platform is seeded synchronously from the user agent (detectPlatform), so the
  // macOS title-bar inset is correct on the first paint. Refine it with Wails'
  // authoritative Environment() when it resolves (it wins; an empty result is
  // ignored so a slow/failed call can't clobber the good seed).
  useEffect(() => {
    if (window.runtime && window.runtime.Environment) {
      window.runtime.Environment()
        .then((e) => { if (e && e.platform) setPlatform(e.platform); })
        .catch(() => {});
    }
  }, []);
  // macOS is the only platform whose custom bar doubles as the OS title bar (it's
  // draggable and insets for the traffic lights); Windows/Linux keep the native bar.
  const isMac = platform === "darwin";

  // First launch only: offer to install the CLI when an embedded one exists
  // (macOS app), it isn't already on PATH, and we haven't asked before.
  useEffect(() => {
    let alive = true;
    Promise.all([api.cliInfo(), api.cliPromptSeen()]).then(([info, seen]) => {
      if (alive && info && info.available && !info.installed && !seen) setShowCliPrompt(true);
    }).catch(() => {});
    return () => { alive = false; };
  }, []);
  // Any dismissal (install, "not now", esc, backdrop) marks it seen for good.
  const dismissCliPrompt = useCallback(() => {
    setShowCliPrompt(false);
    api.markCliPromptSeen().catch(() => {});
  }, []);

  // Background update check on launch (respects the auto-check preference; the
  // backend skips dev builds and caches the result for this session).
  useEffect(() => {
    let alive = true;
    api.getUpdatePrefs().then((p) => {
      if (!alive || !p || !p.autoCheck) return;
      api.checkForUpdate().then((st) => { if (alive) setUpdate(st); }).catch(() => {});
    }).catch(() => {});
    return () => { alive = false; };
  }, []);
  // × on the pill: hide it and don't surface this version again.
  const dismissUpdate = useCallback((version) => {
    setUpdate((u) => (u ? { ...u, skipped: true } : u));
    api.skipUpdate(version).catch(() => {});
  }, []);

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
    // A crawl may already be running (e.g. after a frontend reload). Open it the
    // same way any crawl opens — on its Overview, which renders live progress
    // while the crawl is running (see results-shell.jsx).
    api.activeProgress().then((p) => {
      if (p && p.state === "running") openLive(p.crawlId, p.seed);
    }).catch(() => {});
    const offDone = on("crawl:done", (d) => {
      refresh();
      // The session has ended: stop treating it as live (its Overview falls back
      // to the static dashboard) and fold the final status into what we're showing.
      setLiveCrawlId((cur) => (d && d.crawlId === cur ? null : cur));
      setActiveCrawl((cur) => (cur && d && cur.id === d.crawlId
        ? { ...cur, status: d.status, crawled: d.crawled, total: d.total } : cur));
    });
    // crawls started over MCP (by an LLM) take over the screen like any other
    const offStarted = on("crawl:started", (id) => {
      refresh();
      api.activeProgress().then((p) => openLive(id, p && p.seed)).catch(() => openLive(id, ""));
    });
    return () => { offDone(); offStarted(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refresh]);

  // Open a live crawl on its Overview tab; the Overview renders the live progress
  // view while liveCrawlId matches, so it's reachable like any other crawl page.
  function openLive(id, seed) {
    setLiveCrawlId(id);
    setActiveCrawl((cur) => (cur && cur.id === id ? { ...cur, status: "running" }
      : { id, seed: seed || "", status: "running", crawled: 0, total: 0 }));
    setResultsTab("overview");
    setIssueFilter(null);
    setView("results");
  }

  async function startCrawl(req) {
    const id = await api.startCrawl(req);
    openLive(id, req.url || req.sitemapUrl || (req.listUrls && req.listUrls[0]) || "");
    refresh();
  }
  // first-run welcome: a bare domain → a sensible default spider crawl
  function startFromWelcome(rawUrl) {
    let u = (rawUrl || "").trim();
    if (u && !/^https?:\/\//i.test(u)) u = "https://" + u;
    return startCrawl({
      mode: "spider", url: u, listUrls: [], sitemapUrl: "",
      profile: "Default audit", threads: 5, rate: 2, maxDepth: -1, rendering: "text",
    });
  }
  async function resumeCrawl(c) {
    const id = await api.resumeCrawl(c.id);
    setActiveCrawl({ ...c, id, status: "running" });
    openLive(id, c.seed);
    refresh();
  }
  function openCrawl(c) {
    setActiveCrawl(c);
    setResultsTab("overview");
    setIssueFilter(null);
    setView("results");
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
    { id: "projects", label: "Projects", icon: "folder" },
    { id: "compare", label: "Compare", icon: "git-compare" },
    { id: "robots", label: "robots.txt Tester", icon: "bot" },
    { id: "settings", label: "Settings & Profiles", icon: "sliders-horizontal" },
  ];

  // Titlebar host follows what's on screen: the crawl whose results (or live
  // progress) you're viewing, falling back to the running crawl's domain.
  const liveCrawl = crawls.find((c) => c.id === liveCrawlId);
  const titleHost = activeCrawl
    ? (hostOf(activeCrawl.seed) || (liveCrawl ? hostOf(liveCrawl.seed) : "crawling…"))
    : (liveCrawl ? hostOf(liveCrawl.seed) : "no crawl");

  // Only one crawl runs at a time, process-wide (see runner.go / app.go), so the
  // backend rejects a second start while one is live. Mirror that in the UI:
  // while a crawl is live, disable every "start/resume a crawl" affordance and
  // explain why. liveCrawlId tracks an actually-live session and clears on
  // crawl:done (incl. pause/stop), so the gate lifts exactly when the lock does.
  const crawlActive = liveCrawlId != null;
  const liveSeed = liveCrawl ? liveCrawl.seed
    : (activeCrawl && activeCrawl.id === liveCrawlId ? activeCrawl.seed : "");
  const crawlBusyMsg = crawlActive
    ? `A crawl is already running${liveSeed ? " (" + hostOf(liveSeed) + ")" : ""} — pause or stop it first.`
    : null;
  // Jump back to the running crawl (used by the "View running crawl" link on the
  // New Crawl form when a crawl starts while it's open).
  function viewActiveCrawl() {
    if (!liveCrawlId) return;
    openLive(liveCrawlId, liveSeed);
  }

  return (
    <div className="win">
      {/* On macOS this bar IS the window title bar: it hosts the native traffic
          lights in its left inset and shows the bluesnake wordmark. On Windows/
          Linux the OS draws the real title bar above, so this is just a toolbar —
          drop the wordmark (the native bar already names the app) and keep the
          current crawl host + controls. */}
      <div className={"titlebar" + (isMac ? " mac" : "")}>
        <span className="tb-nodrag">
          <IconBtn icon={collapsed ? "panel-left-open" : "panel-left-close"} title={collapsed ? "Show sidebar" : "Hide sidebar"} onClick={() => setCollapsed((c) => !c)} />
        </span>
        <div className="tb-title">
          <img src="/logo.png" alt="" width={16} height={16} draggable={false} style={{ display: "block", borderRadius: 3 }} />
          {isMac && <>bluesnake<span className="dot" /></>}
          <span style={{ color: "var(--ink-faint)", fontWeight: 500, whiteSpace: "nowrap" }} className="mono">
            {titleHost}
          </span>
        </div>
        <div className="tb-spacer" />
        <div className="tb-actions">
          {update && update.available && !update.skipped && (
            <span className="pill tb-nodrag" title={`Update to v${update.latest}`}
              style={{ height: 22, gap: 5, fontSize: 11, color: "var(--accent)", borderColor: "var(--accent)" }}>
              <span onClick={() => setShowUpdate(true)} style={{ display: "inline-flex", alignItems: "center", gap: 5, cursor: "pointer" }}>
                <Icon name="arrow-up-circle" size={12} />Update v{update.latest}
              </span>
              <button className="iconbtn" style={{ width: 15, height: 15 }} title="Dismiss this version" onClick={() => dismissUpdate(update.latest)}>
                <Icon name="x" size={11} />
              </button>
            </span>
          )}
          <MCPControls onOpenSettings={() => { setSettingsFocus({ section: "mcp" }); setSettingsBack(null); setView("settings"); }} />
          <IconBtn icon={dark ? "sun" : "moon"} title="Toggle theme" onClick={() => setDark((d) => !d)} />
        </div>
      </div>

      {/* body */}
      <div className="body">
        {/* sidebar */}
        <div className={"sidebar" + (collapsed ? " collapsed" : "")}>
          <div className="sb-top">
            {collapsed
              ? <button className="btn-newcrawl" title={crawlActive ? crawlBusyMsg : "New Crawl"} disabled={crawlActive} onClick={() => setView("new")} style={{ width: 34, height: 34, padding: 0 }}><Icon name="plus" size={16} /></button>
              : <button className="btn-newcrawl" title={crawlActive ? crawlBusyMsg : null} disabled={crawlActive} onClick={() => setView("new")}><Icon name="plus" size={15} />New Crawl</button>}
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
              const live = c.id === liveCrawlId || c.status === "running";
              const dotc = live ? "var(--accent)" : c.status === "interrupted" ? "var(--sev-warn)" : "var(--sev-ok)";
              const host = hostOf(c.seed);
              if (collapsed) return (
                <button key={c.id} className="sb-monogram" title={host + (live ? " · crawling…" : " · " + c.crawled.toLocaleString() + " URLs")} onClick={() => openCrawl(c)}>
                  <BrandMark seed={c.seed} size={34} dot={dotc} live={live} active={active} />
                </button>
              );
              return (
                <div key={c.id} className={"sb-crawl copyhost" + (active ? " active" : "")} onClick={() => openCrawl(c)}>
                  <BrandMark seed={c.seed} size={26} dot={dotc} live={live} />
                  <div className="meta">
                    <div className="host">{host}</div>
                    <div className="sub" style={live ? { color: "var(--accent)" } : null}>
                      {live ? "crawling…" : `${(c.total || c.crawled).toLocaleString()} URLs${c.started ? " · " + c.started.split(" ")[0] : ""}`}
                    </div>
                  </div>
                  <CopyButton text={c.seed} title="Copy site URL" />
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
            : <CrawlManager crawls={crawls} onOpen={openCrawl} onResume={resumeCrawl} onCompare={() => setView("compare")} onNew={() => setView("new")} onDelete={deleteCrawl} storage={storage} crawlBusyMsg={crawlBusyMsg} />
        )}
        {view === "new" && <NewCrawl onStart={startCrawl} onOpenSettings={(p) => { setSettingsProfile(p); setSettingsBack({ view: "new", label: "New Crawl" }); setView("settings"); }} crawlBusyMsg={crawlBusyMsg} onViewActiveCrawl={viewActiveCrawl} />}
        {view === "results" && activeCrawl && (
          <ResultsWorkspace
            crawl={activeCrawl}
            live={liveCrawlId === activeCrawl.id}
            tab={resultsTab}
            setTab={(id) => { setResultsTab(id); setIssueFilter(null); }}
            issueFilter={issueFilter}
            setIssueFilter={setIssueFilter}
            onOpenDetail={(url) => setDetail({ crawlId: activeCrawl.id, url })}
            onFilterByIssue={openDataset}
            onResume={() => resumeCrawl(activeCrawl)}
            crawlBusyMsg={crawlBusyMsg}
          />
        )}
        {view === "settings" && <SettingsView profileName={settingsProfile} focus={settingsFocus}
          onBack={settingsBack ? () => setView(settingsBack.view) : null} backLabel={settingsBack ? settingsBack.label : null} />}
        {view === "compare" && <CompareView crawls={crawls} />}
        {view === "projects" && <ProjectsView crawlBusyMsg={crawlBusyMsg} onCrawlSite={(domain) => startCrawl({
          mode: "spider", url: "https://" + domain, listUrls: [], sitemapUrl: "",
          profile: "Default audit", threads: 5, rate: 2, maxDepth: -1, rendering: "text",
        })} />}
        {view === "robots" && <RobotsTester />}
      </div>

      {/* URL detail drawer */}
      {detail && (
        <UrlDetail crawlId={detail.crawlId} url={detail.url}
          onClose={() => setDetail(null)}
          onFilterByIssue={(f) => { setDetail(null); openDataset("internal", f); }} />
      )}

      {/* first-launch CLI install prompt */}
      {showCliPrompt && <CliPrompt onClose={dismissCliPrompt} />}

      {/* self-update flow (opened from the title-bar pill) */}
      {showUpdate && update && <UpdatePrompt status={update} onClose={() => setShowUpdate(false)} />}
    </div>
  );
}

/* The self-update flow: confirm → download (with progress) → install & restart.
   On success the app quits and relaunches mid-install, so the promise from
   applyUpdate typically never resolves; we only ever return here on error. */
function UpdatePrompt({ status, onClose }) {
  const [phase, setPhase] = useState("ready"); // ready | working | error
  const [prog, setProg] = useState({ phase: "", done: 0, total: 0 });
  const [err, setErr] = useState("");
  const [crawlRunning, setCrawlRunning] = useState(false);

  useEffect(() => {
    api.activeProgress().then((p) => setCrawlRunning(!!(p && p.state === "running"))).catch(() => {});
    const off = on("update:progress", (e) => { if (e) setProg(e); });
    return () => off();
  }, []);

  async function go() {
    setPhase("working");
    try {
      const st = await api.applyUpdate();
      if (st && st.error) { setErr(st.error); setPhase("error"); }
    } catch (e) { setErr(String(e)); setPhase("error"); }
  }

  if (phase === "error") {
    return <Modal icon="circle-alert" danger title="Update failed" onClose={onClose}
      body={<>{err || "Something went wrong."} You can download the update manually from the release page instead.</>}
      actions={<>
        <Btn onClick={onClose}>Close</Btn>
        <Btn variant="primary" icon="external-link" onClick={() => { openURL(status.url); onClose(); }}>Open release page</Btn>
      </>} />;
  }

  if (phase === "working") {
    const pct = prog.total > 0 ? Math.round((prog.done / prog.total) * 100) : null;
    const label = prog.phase === "applying"
      ? "Installing & restarting…"
      : pct != null ? `Downloading… ${pct}%` : "Downloading…";
    return <Modal icon="download" title={`Updating to v${status.latest}`} onClose={() => {}}
      body={<div>
        <div style={{ marginBottom: 10 }}>{label}</div>
        <div style={{ height: 6, background: "var(--border-soft)", borderRadius: 4, overflow: "hidden" }}>
          <div style={{ height: "100%", width: (pct != null ? pct : 15) + "%", background: "var(--accent)", transition: "width .2s ease" }} />
        </div>
        <div className="hint" style={{ marginTop: 10 }}>bluesnake will restart automatically when the update is ready — don’t quit it.</div>
      </div>}
      actions={null} />;
  }

  return <Modal icon="arrow-up-circle" title={`Update available — v${status.latest}`} onClose={onClose}
    body={<div>
      <div style={{ marginBottom: status.notes ? 10 : 0 }}>
        You’re on <span className="mono">v{status.current}</span>. Update to <span className="mono">v{status.latest}</span>? bluesnake will download, verify, install, and restart itself.
      </div>
      {status.notes && (
        <div style={{ maxHeight: 150, overflowY: "auto", fontSize: 11.5, lineHeight: 1.55, color: "var(--ink-2)", background: "var(--surface-2)", border: "1px solid var(--border-soft)", borderRadius: 8, padding: "9px 11px", whiteSpace: "pre-wrap" }}>
          {status.notes}
        </div>
      )}
      {crawlRunning && (
        <div className="hint" style={{ marginTop: 10, color: "var(--sev-warn)", display: "flex", alignItems: "center", gap: 6 }}>
          <Icon name="triangle-alert" size={13} />A crawl is running — it will be paused and can be resumed after the restart.
        </div>
      )}
    </div>}
    actions={<>
      <Btn onClick={onClose}>Later</Btn>
      <Btn variant="primary" icon="download" onClick={go}>Update now</Btn>
    </>} />;
}

/* First-launch prompt offering to install the command-line tool. Self-contained
   state machine (ask → installing → done/error); every exit calls onClose, which
   records that we've asked so it never shows again. */
function CliPrompt({ onClose }) {
  const [phase, setPhase] = useState("ask"); // ask | installing | done | error
  const [result, setResult] = useState(null);

  async function install() {
    setPhase("installing");
    try {
      const st = await api.installCLI();
      setResult(st);
      setPhase(st && st.error ? "error" : "done");
    } catch (e) {
      setResult({ error: String(e) });
      setPhase("error");
    }
  }

  if (phase === "done") {
    return <Modal icon="circle-check" title="Command-line tool installed" onClose={onClose}
      body={<>You can now run <span className="mono">bluesnake</span> from any terminal{result && result.target ? <> (linked at <span className="mono">{result.target}</span>)</> : null}. Open a new terminal and try <span className="mono">bluesnake version</span>.</>}
      actions={<Btn variant="primary" onClick={onClose}>Done</Btn>} />;
  }
  if (phase === "error") {
    return <Modal icon="circle-alert" danger title="Couldn’t install the command-line tool" onClose={onClose}
      body={<>{(result && result.error) || "Something went wrong."} You can try again anytime from <b>Settings → Command-line tool</b>.</>}
      actions={<Btn variant="primary" onClick={onClose}>Close</Btn>} />;
  }
  const busy = phase === "installing";
  return <Modal icon="terminal" title="Install the command-line tool?" onClose={busy ? () => {} : onClose}
    body={<>bluesnake also works from your terminal — script crawls, run audits in CI, and pipe results into other tools. Install it to add the <span className="mono">bluesnake</span> command to your PATH. You can always do this later from <b>Settings → Command-line tool</b>.</>}
    actions={<>
      <Btn onClick={onClose} disabled={busy}>Not now</Btn>
      <Btn variant="primary" icon="download" onClick={install} disabled={busy}>{busy ? "Installing…" : "Install"}</Btn>
    </>} />;
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
