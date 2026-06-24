/* ===========================================================================
   Results workspace shell — dataset rail, toolbar, overview, export
   =========================================================================== */
import React, { useEffect, useMemo, useRef, useState } from "react";
import { Icon, Btn, IconBtn, SevDot, SEV, PRIO, Empty, StatusBar, Ring, Modal, Toast, Toggle, Seg } from "../ui";
import { api, urlShort } from "../api";
import { DataTable } from "./results";
import { IssuesBrowser } from "./issues";
import { CrawlProgress } from "./progress";

/* rail metadata for the export tabs (label + icon per backend tab name) */
const DATASETS = [
  { id: "internal", label: "Internal", icon: "file-text" },
  { id: "external", label: "External", icon: "external-link" },
  { id: "response_codes", label: "Response Codes", icon: "activity" },
  { id: "titles", label: "Page Titles", icon: "type" },
  { id: "descriptions", label: "Meta Descriptions", icon: "align-left" },
  { id: "h1", label: "H1", icon: "heading" },
  { id: "canonicals", label: "Canonicals", icon: "link-2" },
  { id: "hreflang", label: "Hreflang", icon: "languages" },
  { id: "images", label: "Images", icon: "image" },
  { id: "security", label: "Security", icon: "shield" },
  { id: "links", label: "Links", icon: "git-branch" },
  { id: "custom", label: "Custom Extraction", icon: "scan-search" },
];

const ROW_LIMIT = 2000;

export function ResultsWorkspace({ crawl, live, tab, setTab, issueFilter, setIssueFilter, onOpenDetail, onFilterByIssue, onResume, crawlBusyMsg }) {
  const [toast, setToast] = useState(null);
  const [exporting, setExporting] = useState(false);
  const [analyseMenu, setAnalyseMenu] = useState(false);
  const [counts, setCounts] = useState({});
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const fireToast = (msg, icon = "check") => { setToast({ msg, icon }); setTimeout(() => setToast(null), 2600); };

  // The Overview tab doubles as the live-crawl page: while this crawl is running
  // it shows realtime progress, with a toggle to peek at the partial dashboard.
  // `ovMode` is "progress" | "static": default to progress when the crawl is
  // live. We adjust it during render (React's "store info from previous renders"
  // pattern) so switching crawls or resuming flips the view with no flash:
  //   • different crawl  → progress if it's live, else the static dashboard
  //   • this crawl becomes live (e.g. resume) → progress
  //   • this crawl finishes mid-view → leave ovMode as-is, so the completion
  //     summary stays until the user toggles to the dashboard or navigates away.
  const [ovMode, setOvMode] = useState(live ? "progress" : "static");
  const prev = useRef({ id: crawl.id, live });
  if (prev.current.id !== crawl.id) {
    prev.current = { id: crawl.id, live };
    setOvMode(live ? "progress" : "static");
  } else if (prev.current.live !== live) {
    if (live) setOvMode("progress");
    prev.current.live = live;
  }
  const showProgress = tab === "overview" && ovMode === "progress";
  const ovToggle = (
    <Seg value={ovMode} onChange={setOvMode}
      options={[{ value: "progress", label: "Progress" }, { value: "static", label: "Overview" }]} />
  );

  useEffect(() => {
    api.datasetCounts(crawl.id).then((c) => setCounts(c || {})).catch(() => {});
  }, [crawl.id]);

  useEffect(() => {
    if (tab === "overview" || tab === "issues") { setData(null); return; }
    let alive = true;
    setLoading(true);
    setError("");
    api.dataset(crawl.id, tab, issueFilter ? issueFilter.id : "", ROW_LIMIT)
      .then((d) => alive && setData(d))
      .catch((e) => alive && setError(String(e)))
      .finally(() => alive && setLoading(false));
    return () => { alive = false; };
  }, [crawl.id, tab, issueFilter]);

  const datasetName = (DATASETS.find((d) => d.id === tab) || {}).label || (tab === "issues" ? "Issues" : "Overview");
  const urlColIdx = data ? data.header.findIndex((h) => h.toLowerCase() === "address" || h.toLowerCase() === "url") : -1;

  async function reanalyse() {
    setAnalyseMenu(false);
    fireToast("Re-running analysis — no recrawl needed", "git-compare");
    try {
      await api.reanalyze(crawl.id);
      api.datasetCounts(crawl.id).then((c) => setCounts(c || {}));
      fireToast("Analysis complete", "check");
    } catch (e) {
      fireToast("Analysis failed: " + e, "circle-alert");
    }
  }
  async function sitemap() {
    try {
      const dir = await api.generateSitemap(crawl.id);
      if (dir) fireToast("Wrote sitemap.xml → " + dir, "map");
    } catch (e) {
      fireToast("Sitemap failed: " + e, "circle-alert");
    }
  }

  return (
    <div className="main" style={{ flexDirection: "row" }}>
      {/* dataset rail */}
      <div style={{ width: 210, flex: "0 0 210px", borderRight: "1px solid var(--border-soft)", background: "var(--sidebar)", display: "flex", flexDirection: "column", minHeight: 0 }}>
        <div style={{ padding: "11px 12px 7px" }}>
          <div className="mono" style={{ fontSize: 11.5, fontWeight: 600, color: "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(crawl.seed)}</div>
          <div style={{ fontSize: 10.5, color: live ? "var(--accent)" : "var(--ink-faint)", marginTop: 2, display: "flex", alignItems: "center", gap: 5 }}>
            <span className="statusdot" style={{ background: live ? "var(--accent)" : crawl.status === "interrupted" ? "var(--sev-warn)" : "var(--sev-ok)", animation: live ? "pulse 1.4s infinite" : undefined }} />
            {live ? "crawling…" : `${(crawl.total || crawl.crawled).toLocaleString()} URLs${crawl.started ? " · " + crawl.started.split(" ")[0] : ""}`}
          </div>
        </div>
        <div className="sb-nav" style={{ paddingTop: 2 }}>
          <RailItem active={tab === "overview"} icon="layout-dashboard" label="Overview" onClick={() => setTab("overview")} />
          <RailItem active={tab === "issues"} icon="octagon-alert" label="Issues" onClick={() => setTab("issues")}
            right={<span style={{ display: "flex", gap: 5 }}>{["issue", "warning", "opportunity"].map((s) => <span key={s} className="statusdot" style={{ background: SEV[s].c }} />)}</span>} />
        </div>
        <div className="sb-sectlabel">Datasets</div>
        <div className="sb-recents" style={{ paddingTop: 0 }}>
          {DATASETS.map((d) => {
            const n = counts[d.id];
            return (
              <div key={d.id} className={"sb-item" + (tab === d.id ? " active" : "")} onClick={() => setTab(d.id)} style={{ height: 28 }}>
                <Icon name={d.icon} size={14} />
                <span style={{ flex: 1, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{d.label}</span>
                <span className="count">{n == null ? "" : n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + "k" : n}</span>
              </div>
            );
          })}
        </div>
      </div>

      {/* content — the Overview tab renders live progress while crawling
          (CrawlProgress brings its own toolbar with the crawl controls);
          everything else uses the standard dataset toolbar. */}
      <div className="main" style={{ minWidth: 0 }}>
        {showProgress ? (
          <CrawlProgress crawlId={crawl.id} headerExtra={ovToggle}
            onResume={onResume} onOpenResults={() => setOvMode("static")} />
        ) : (
          <>
            <div className="toolbar">
              <span className="title" style={{ fontSize: 13.5 }}>{datasetName}</span>
              {data && <span className="pill mono" style={{ height: 20, fontSize: 11 }}>{data.total.toLocaleString()}</span>}
              {tab === "overview" && live && <span style={{ marginLeft: 4 }}>{ovToggle}</span>}
              <div style={{ flex: 1 }} />
              {crawl.status === "interrupted" && onResume && <Btn icon="play" variant="primary" onClick={onResume} disabled={!!crawlBusyMsg} title={crawlBusyMsg}>Resume crawl</Btn>}
              <div style={{ position: "relative" }}>
                <Btn icon="git-compare" onClick={() => setAnalyseMenu((v) => !v)}>Re-analyse</Btn>
                {analyseMenu && <>
                  <div style={{ position: "fixed", inset: 0, zIndex: 30 }} onClick={() => setAnalyseMenu(false)} />
                  <div className="card fade" style={{ position: "absolute", right: 0, top: 36, zIndex: 31, width: 280, padding: 6, boxShadow: "var(--shadow-lg)" }}>
                    <MenuItem icon="git-compare" title="Re-run analysis" sub="Issues, link scores, chains, duplicates, hreflang — no recrawl needed" onClick={reanalyse} />
                  </div>
                </>}
              </div>
              <Btn icon="map" onClick={sitemap}>Sitemap</Btn>
              <Btn icon="download" variant="primary" onClick={() => setExporting(true)}>Export</Btn>
            </div>

            {issueFilter && (
              <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 16px", background: "color-mix(in oklab, var(--sev-issue) 6%, var(--bg))", borderBottom: "1px solid var(--border-soft)" }}>
                <Icon name="filter" size={14} style={{ color: "var(--ink-3)" }} />
                <span style={{ fontSize: 12, color: "var(--ink-2)" }}>Filtered to URLs affected by</span>
                <span className="pill" style={{ height: 22, borderColor: "color-mix(in oklab, var(--sev-issue) 30%, transparent)" }}><SevDot severity="issue" />{issueFilter.name}</span>
                {data && <span className="mono" style={{ fontSize: 11.5, color: "var(--ink-faint)" }}>{data.total.toLocaleString()} affected</span>}
                <IconBtn icon="x" title="Clear filter" onClick={() => setIssueFilter(null)} />
              </div>
            )}

            {tab === "overview" && <ResultsOverview crawl={crawl} setTab={setTab} onFilterByIssue={onFilterByIssue} />}
            {tab === "issues" && <IssuesBrowser crawlId={crawl.id} onFilterByIssue={onFilterByIssue} />}
            {tab !== "overview" && tab !== "issues" && (
              loading ? <Loading />
                : error ? <Empty icon="circle-alert" title="Couldn't load dataset">{error}</Empty>
                  : data && data.total === 0 && tab === "custom" ? <CustomExtractionEmpty />
                    : data ? <DataTable header={data.header} rows={data.rows || []} total={data.total} truncated={data.truncated}
                      onRowClick={urlColIdx >= 0 ? (row) => onOpenDetail(row[urlColIdx]) : null} />
                      : null
            )}
          </>
        )}
      </div>

      {exporting && <ExportPanel crawlId={crawl.id} tab={tab === "overview" || tab === "issues" ? "issues" : tab} datasetName={datasetName} issueFilter={issueFilter}
        onClose={() => setExporting(false)}
        onDone={(path, fmt) => { setExporting(false); if (path) fireToast(`Exported ${datasetName} → ${path}`, "file-check"); }} />}
      {toast && <Toast {...toast} />}
    </div>
  );
}

function Loading() {
  return <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--ink-faint)", gap: 9, fontSize: 12.5 }}>
    <Icon name="loader" size={16} style={{ animation: "spin 1s linear infinite" }} />Loading…
  </div>;
}

export function RailItem({ active, icon, label, onClick, right }) {
  return <div className={"sb-item" + (active ? " active" : "")} onClick={onClick}>
    <Icon name={icon} size={15} /><span style={{ flex: 1 }}>{label}</span>{right}
  </div>;
}
function MenuItem({ icon, title, sub, onClick }) {
  return <div onClick={onClick} style={{ display: "flex", gap: 10, padding: "9px 10px", cursor: "default" }}
    onMouseEnter={(e) => e.currentTarget.style.background = "var(--surface-hover)"} onMouseLeave={(e) => e.currentTarget.style.background = "transparent"}>
    <Icon name={icon} size={15} style={{ color: "var(--ink-3)", marginTop: 1 }} />
    <div><div style={{ fontSize: 12.5, fontWeight: 600 }}>{title}</div>{sub && <div className="hint" style={{ marginTop: 1 }}>{sub}</div>}</div>
  </div>;
}

/* ---- crawl overview dashboard ----------------------------------------- */
function ResultsOverview({ crawl, setTab, onFilterByIssue }) {
  const [o, setO] = useState(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.overview(crawl.id).then(setO).catch((e) => setError(String(e)));
  }, [crawl.id]);
  if (error) return <Empty icon="circle-alert" title="Couldn't load overview">{error}</Empty>;
  if (!o) return <Loading />;

  const status = { "2xx": o.s2xx, "3xx": o.s3xx, "4xx": o.s4xx, "5xx": o.s5xx, blocked: o.blocked, noresp: o.noresp };
  const indexTotal = o.indexable + o.nonIndexable || 1;

  const Metric = ({ label, value, sub, color, onClick }) => (
    <div className="card" style={{ padding: "15px 17px", cursor: "default" }} onClick={onClick}
      onMouseEnter={onClick ? (e) => e.currentTarget.style.borderColor = "var(--border-strong)" : null}
      onMouseLeave={onClick ? (e) => e.currentTarget.style.borderColor = "var(--border)" : null}>
      <div style={{ fontSize: 11, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em" }}>{label}</div>
      <div className="mono" style={{ fontSize: 25, fontWeight: 600, marginTop: 6, color: color || "var(--ink)", letterSpacing: "-.02em" }}>{value}</div>
      {sub && <div style={{ fontSize: 11.5, color: "var(--ink-3)", marginTop: 2 }}>{sub}</div>}
    </div>
  );

  return (
    <div className="scroll" style={{ padding: 20 }}>
      <div style={{ maxWidth: 1080, margin: "0 auto" }} className="fade">
        <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 12, marginBottom: 14 }}>
          <Metric label="URLs found" value={o.total.toLocaleString()} sub={`${o.crawled.toLocaleString()} crawled · ${o.blocked.toLocaleString()} blocked by robots`} onClick={() => setTab("internal")} />
          <Metric label="Indexable" value={o.indexable.toLocaleString()} sub={`${Math.round(o.indexable / indexTotal * 100)}% of internal pages`} color="var(--sev-ok)" />
          <Metric label="Issues found" value={o.issues} sub="high-priority problems" color="var(--sev-issue)" onClick={() => setTab("issues")} />
          <Metric label="Avg link score" value={o.avgLinkScore ? o.avgLinkScore.toFixed(0) : "—"} sub="0–100, PageRank-like" />
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "1.4fr 1fr", gap: 14, marginBottom: 14 }}>
          <div className="card" style={{ padding: 18 }}>
            <div style={{ display: "flex", alignItems: "center", marginBottom: 14 }}><span style={{ fontSize: 12.5, fontWeight: 650 }}>Response codes</span><div style={{ flex: 1 }} /><Btn size="sm" variant="ghost" icon="arrow-right" onClick={() => setTab("response_codes")}>View</Btn></div>
            <StatusBar status={status} height={12} showLabels />
          </div>
          <div className="card" style={{ padding: 18, display: "flex", alignItems: "center", gap: 18 }}>
            <Ring value={o.indexable} total={indexTotal} size={84} stroke={9} color="var(--sev-ok)" />
            <div>
              <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 8 }}>Indexability</div>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <span style={{ fontSize: 12, display: "flex", alignItems: "center", gap: 7 }}><span className="statusdot" style={{ background: "var(--sev-ok)" }} />Indexable <b className="mono" style={{ marginLeft: "auto", paddingLeft: 14 }}>{o.indexable.toLocaleString()}</b></span>
                <span style={{ fontSize: 12, display: "flex", alignItems: "center", gap: 7 }}><span className="statusdot" style={{ background: "var(--ink-faint)" }} />Non-indexable <b className="mono" style={{ marginLeft: "auto", paddingLeft: 14 }}>{o.nonIndexable.toLocaleString()}</b></span>
              </div>
            </div>
          </div>
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 12, marginBottom: 14 }}>
          {[["issue", o.issues, "Issues"], ["warning", o.warnings, "Warnings"], ["opportunity", o.opportunities, "Opportunities"]].map(([s, n, label]) => (
            <div key={s} className="card" style={{ padding: 16, cursor: "default" }} onClick={() => setTab("issues")}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}><Icon name={SEV[s].icon} size={16} style={{ color: SEV[s].c }} /><span style={{ fontSize: 12, fontWeight: 600 }}>{label}</span></div>
              <div className="mono" style={{ fontSize: 26, fontWeight: 600, marginTop: 8, color: SEV[s].c }}>{n}</div>
            </div>
          ))}
        </div>

        <div className="card" style={{ overflow: "hidden" }}>
          <div style={{ padding: "13px 16px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "center" }}>
            <span style={{ fontSize: 12.5, fontWeight: 650 }}>Top issues to fix</span><div style={{ flex: 1 }} /><Btn size="sm" variant="ghost" icon="arrow-right" onClick={() => setTab("issues")}>All issues</Btn>
          </div>
          {(o.topIssues || []).map((it) => (
            <div key={it.id} className="datarow" onClick={() => onFilterByIssue("internal", { id: it.id, name: it.name })}
              style={{ display: "grid", gridTemplateColumns: "20px minmax(0,1fr) 120px 64px 44px", gap: 10, alignItems: "center", padding: "10px 16px", borderBottom: "1px solid var(--border-soft)", cursor: "default" }}>
              <SevDot severity={it.severity} />
              <div style={{ minWidth: 0 }}><span style={{ fontSize: 12.5, fontWeight: 500, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", display: "block" }} title={it.name}>{it.name}</span></div>
              <span style={{ fontSize: 11, color: "var(--ink-faint)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{prettyCat(it.category)}</span>
              <span style={{ fontSize: 11, color: "var(--ink-3)" }}>{PRIO[it.priority] || it.priority}</span>
              <span className="mono" style={{ fontSize: 12.5, fontWeight: 600, textAlign: "right", color: it.severity === "issue" ? "var(--sev-issue)" : "var(--ink-2)" }}>{it.count}</span>
            </div>
          ))}
          {(!o.topIssues || o.topIssues.length === 0) && (
            <div style={{ padding: "20px 16px", fontSize: 12.5, color: "var(--sev-ok)", display: "flex", alignItems: "center", gap: 8 }}>
              <Icon name="circle-check" size={15} />No issues found — every check passed.
            </div>
          )}
        </div>

        <div style={{ marginTop: 14, padding: 14, display: "flex", alignItems: "center", gap: 12, fontSize: 12, color: "var(--ink-3)", background: "var(--surface-2)", border: "1px solid var(--border-soft)" }}>
          <Icon name="git-compare" size={16} />
          <span>Crawl analysis ran automatically on finish. Changed a threshold? <b style={{ color: "var(--ink)" }}>Re-analyse</b> recomputes scores and issues without re-downloading anything.</span>
        </div>
      </div>
    </div>
  );
}

export function prettyCat(tab) {
  return (tab || "").split("_").map((w) => w === "h1" ? "H1" : w === "url" ? "URL" : w.charAt(0).toUpperCase() + w.slice(1)).join(" ");
}

/* ---- custom extraction empty ------------------------------------------ */
function CustomExtractionEmpty() {
  return <Empty icon="scan-search" title="No custom extraction configured">
    Define XPath, CSS-selector or regex extractors in Settings → Custom Extraction (config: custom_extraction). Each becomes a column here with a per-URL value.
  </Empty>;
}

/* ---- export panel ----------------------------------------------------- */
function ExportPanel({ crawlId, tab, datasetName, issueFilter, onClose, onDone }) {
  const [fmt, setFmt] = useState("csv");
  const [filterIssue, setFilterIssue] = useState(!!issueFilter);
  const [busy, setBusy] = useState(false);
  const formats = ["csv", "json", "jsonl", "xlsx"];
  async function doExport() {
    setBusy(true);
    try {
      const path = await api.exportDataset(crawlId, tab, filterIssue && issueFilter ? issueFilter.id : "", fmt);
      onDone(path, fmt);
    } catch (e) {
      onDone("", fmt);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal onClose={onClose} icon="download" title={`Export ${datasetName}`}
      body={
        <div style={{ marginTop: 4 }}>
          <div style={{ fontSize: 11.5, fontWeight: 600, color: "var(--ink-2)", marginBottom: 8 }}>Format</div>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 7 }}>
            {formats.map((f) => (
              <button key={f} onClick={() => setFmt(f)} className="card" style={{ padding: "11px 6px", textAlign: "center", fontSize: 12, fontWeight: 600, cursor: "default", textTransform: "uppercase", borderColor: fmt === f ? "var(--accent)" : "var(--border)", background: fmt === f ? "var(--accent-soft)" : "var(--surface)", color: fmt === f ? "var(--ink)" : "var(--ink-2)" }}>{f}</button>
            ))}
          </div>
          {issueFilter && (
            <label style={{ display: "flex", alignItems: "center", gap: 9, marginTop: 14, fontSize: 12, color: "var(--ink-2)" }}>
              <Toggle on={filterIssue} onChange={setFilterIssue} /> Only rows affected by “{issueFilter.name}”
            </label>
          )}
          <div style={{ marginTop: 14, display: "flex", alignItems: "center", gap: 8, fontSize: 11.5, color: "var(--ink-faint)" }}>
            <Icon name="folder" size={14} /><span>You'll pick the destination in the next step.</span>
          </div>
        </div>
      }
      actions={<><Btn onClick={onClose}>Cancel</Btn><Btn variant="primary" icon="download" disabled={busy} onClick={doExport}>{busy ? "Exporting…" : "Export"}</Btn></>} />
  );
}
