/* ===========================================================================
   Per-URL detail drawer — full record, in/outlinks, issues, headers
   =========================================================================== */
import React, { useEffect, useState } from "react";
import { Icon, IconBtn, StatusBadge, IndexBadge, Empty, SevDot, SEV, PRIO, statusVar, CopyButton } from "../ui";
import { api, urlShort } from "../api";

export function UrlDetail({ crawlId, url, onClose, onFilterByIssue }) {
  const [tab, setTab] = useState("details");
  const [p, setP] = useState(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const h = (e) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, []);

  useEffect(() => {
    setP(null);
    setError("");
    api.pageDetail(crawlId, url).then(setP).catch((e) => setError(String(e)));
  }, [crawlId, url]);

  const issues = (p && p.issues) || [];
  const tabs = [
    ["details", "Details"],
    ["inlinks", `Inlinks ${p ? p.inlinksCount : ""}`],
    ["outlinks", `Outlinks ${p && p.outlinkRefs ? p.outlinkRefs.length : ""}`],
    ["issues", `Issues ${issues.length}`],
    ["headers", "Headers"],
  ];

  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, zIndex: 90, background: "oklch(0.2 0.02 262 / 0.32)", display: "flex", justifyContent: "flex-end", animation: "fadeUp .12s ease" }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 540, maxWidth: "92vw", height: "100%", background: "var(--bg)", borderLeft: "1px solid var(--border)", boxShadow: "var(--shadow-lg)", display: "flex", flexDirection: "column", animation: "slideIn .2s cubic-bezier(.2,.7,.3,1)" }}>
        {/* header */}
        <div style={{ padding: "14px 16px", borderBottom: "1px solid var(--border-soft)" }}>
          <div style={{ display: "flex", alignItems: "flex-start", gap: 10 }}>
            <div style={{ minWidth: 0, flex: 1 }}>
              <div className="mono" style={{ fontSize: 12.5, fontWeight: 600, color: "var(--ink)", wordBreak: "break-all", lineHeight: 1.4 }}>{urlShort(url)}</div>
              {p && (
                <div style={{ display: "flex", gap: 8, marginTop: 9, flexWrap: "wrap" }}>
                  <StatusBadge status={p.statusCode} statusText={p.status} />
                  <IndexBadge value={p.indexable} />
                  {p.indexabilityStatus && <span className="pill" style={{ height: 20, fontSize: 11 }}>{p.indexabilityStatus}</span>}
                </div>
              )}
            </div>
            <CopyButton text={url} title="Copy URL" size={15} style={{ width: 28, height: 28, opacity: 1, color: "var(--ink-2)" }} />
            <IconBtn icon="external-link" title="Open URL in browser" onClick={() => window.runtime && window.runtime.BrowserOpenURL(url)} />
            <IconBtn icon="x" title="Close" onClick={onClose} />
          </div>
        </div>
        {/* tabs */}
        <div style={{ display: "flex", gap: 2, padding: "0 12px", borderBottom: "1px solid var(--border-soft)", flex: "0 0 auto" }}>
          {tabs.map(([id, label]) => (
            <button key={id} onClick={() => setTab(id)} style={{ border: 0, background: "transparent", padding: "11px 11px", fontSize: 12, fontWeight: 600, cursor: "default", color: tab === id ? "var(--ink)" : "var(--ink-faint)", borderBottom: "2px solid " + (tab === id ? "var(--accent)" : "transparent"), marginBottom: -1 }}>{label}</button>
          ))}
        </div>

        <div className="scroll" style={{ padding: 16 }}>
          {error && <Empty icon="circle-alert" title="Couldn't load URL">{error}</Empty>}
          {!p && !error && <div style={{ padding: 30, textAlign: "center", color: "var(--ink-faint)", fontSize: 12 }}>Loading…</div>}
          {p && tab === "details" && <DetailFields p={p} />}
          {p && tab === "inlinks" && <LinkList links={p.inlinkRefs || []} dir="from" empty="No stored internal links point to this URL." n={p.inlinksCount} />}
          {p && tab === "outlinks" && <LinkList links={p.outlinkRefs || []} dir="to" empty="This URL has no stored outlinks." n={(p.outlinkRefs || []).length} />}
          {p && tab === "issues" && (
            issues.length === 0
              ? <Empty icon="circle-check" title="No issues on this URL">Every check passed for this page.</Empty>
              : <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
                {issues.map((it) => (
                  <div key={it.id} className="card" onClick={() => onFilterByIssue({ id: it.id, name: it.name })} style={{ padding: "11px 13px", display: "flex", alignItems: "center", gap: 11, cursor: "default" }}>
                    <SevDot severity={it.severity} />
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 12.5, fontWeight: 600 }}>{it.name}</div>
                      <div className="hint" style={{ marginTop: 1, textTransform: "capitalize" }}>{(SEV[it.severity] || SEV.ok).label} · {PRIO[it.priority] || it.priority} priority</div>
                    </div>
                    <Icon name="chevron-right" size={16} style={{ color: "var(--ink-faint)" }} />
                  </div>
                ))}
              </div>
          )}
          {p && tab === "headers" && (
            Object.keys(p.headers || {}).length === 0
              ? <Empty icon="file-x" title="No headers stored">Header extraction may be off for this crawl (extraction → HTTP headers).</Empty>
              : <div className="card" style={{ padding: 14, fontFamily: "var(--font-mono)", fontSize: 11.5, lineHeight: 1.9 }}>
                <div style={{ display: "flex", gap: 8 }}>
                  <span style={{ color: "var(--ink-faint)", minWidth: 180 }}>HTTP:</span>
                  <span style={{ color: statusVar(p.statusCode) }}>{p.statusCode || "—"} {p.status}</span>
                </div>
                {Object.entries(p.headers).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => (
                  <div key={k} style={{ display: "flex", gap: 8 }}>
                    <span style={{ color: "var(--ink-faint)", minWidth: 180, wordBreak: "break-all" }}>{k.toLowerCase()}:</span>
                    <span style={{ color: "var(--ink-2)", wordBreak: "break-all" }}>{String(v)}</span>
                  </div>
                ))}
              </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Row({ k, v, color, copy }) {
  return <div className={copy ? "copyhost" : undefined} style={{ display: "grid", gridTemplateColumns: "130px 1fr", gap: 10, padding: "7px 0", borderBottom: "1px solid var(--border-soft)", alignItems: "baseline" }}>
    <span style={{ fontSize: 11.5, color: "var(--ink-faint)" }}>{k}</span>
    <span className="mono" style={{ fontSize: 12, color: color || "var(--ink-2)", wordBreak: "break-word", display: "inline-flex", alignItems: "baseline", gap: 6 }}>
      <span style={{ minWidth: 0 }}>{v}</span>
      {copy && <CopyButton text={copy} style={{ alignSelf: "center" }} />}
    </span>
  </div>;
}

function DetailFields({ p }) {
  const html = (p.contentType || "").includes("html") && p.statusCode === 200;
  return (
    <div>
      {html && (
        <div className="card" style={{ padding: 14, marginBottom: 14 }}>
          <FieldText label="Title" text={p.title} len={(p.title || "").length} max={60} />
          <FieldText label="Meta description" text={p.description} len={(p.description || "").length} max={155} />
          <FieldText label="H1" text={p.h1} len={(p.h1 || "").length} max={70} last />
        </div>
      )}
      <div style={{ padding: "0 2px" }}>
        <Row k="Status" v={`${p.statusCode || "—"} ${p.status || p.state}`} color={statusVar(p.statusCode)} />
        <Row k="Indexability" v={`${p.indexable ? "Indexable" : "Non-Indexable"}${p.indexabilityStatus ? " · " + p.indexabilityStatus : ""}`} />
        <Row k="Content type" v={p.contentType || "—"} />
        <Row k="HTTP version" v={p.httpVersion || "—"} />
        <Row k="Crawl depth" v={p.depth} />
        <Row k="Response time" v={p.responseTimeMs ? p.responseTimeMs + " ms" : "—"} color={p.responseTimeMs > 1500 ? "var(--s-4xx)" : null} />
        <Row k="Size" v={p.sizeKB ? p.sizeKB + " KB" : "—"} />
        <Row k="Word count" v={(p.wordCount || 0).toLocaleString()} />
        <Row k="Link score" v={p.linkScore ? p.linkScore.toFixed(0) + " / 100" : "—"} />
        <Row k="Inlinks" v={`${p.inlinksCount}${p.uniqueInlinks ? ` (${p.uniqueInlinks} unique)` : ""}`} />
        <Row k="Unique outlinks" v={p.uniqueOutlinks || "—"} />
        <Row k="Canonical" v={!p.canonical || p.canonical === p.url ? "Self-referencing" : urlShort(p.canonical)} color={p.canonical && p.canonical !== p.url ? "var(--s-3xx)" : null} copy={p.canonical || p.url} />
        {p.redirectUrl && <Row k="Redirects to" v={urlShort(p.redirectUrl)} color="var(--s-3xx)" copy={p.redirectUrl} />}
        {p.redirectType && <Row k="Redirect type" v={p.redirectType} />}
        {p.robotsLine > 0 && <Row k="Blocked by" v={`robots.txt line ${p.robotsLine}`} color="var(--s-4xx)" />}
        {p.fetchError && <Row k="Fetch error" v={p.fetchError} color="var(--s-4xx)" />}
        <Row k="Near-duplicate" v={p.similarity ? `${p.similarity.toFixed(0)}% closest match` : "None"} color={p.similarity > 90 ? "var(--sev-warn)" : null} />
        <Row k="Discovered via" v={p.discoveredFrom ? urlShort(p.discoveredFrom) : "Start URL"} copy={p.discoveredFrom || null} />
      </div>
      <DiscoveryPath path={p.discoveryPath} url={p.url} />
    </div>
  );
}

/* the shortest known click path from the seed to this URL (crawl_paths data) */
function DiscoveryPath({ path, url }) {
  if (!path || path.length < 2) return null;
  return (
    <div className="card" style={{ padding: 14, marginTop: 14 }}>
      <div style={{ fontSize: 11, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".04em", marginBottom: 8 }}>
        Discovery path · {path.length - 1} {path.length === 2 ? "hop" : "hops"}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {path.map((u, i) => (
          <div key={i} className="copyhost" style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)", minWidth: 18, textAlign: "right", alignSelf: "baseline" }}>{i === 0 ? "•" : "↳"}</span>
            <span className="mono" style={{ flex: 1, minWidth: 0, fontSize: 11.5, color: u === url ? "var(--ink)" : "var(--ink-3)", fontWeight: u === url ? 600 : 400, wordBreak: "break-all", paddingLeft: i * 8 }}>{urlShort(u)}</span>
            <CopyButton text={u} />
          </div>
        ))}
      </div>
    </div>
  );
}

function FieldText({ label, text, len, max, last }) {
  const over = len > max, missing = !text;
  return <div style={{ paddingBottom: last ? 0 : 11, marginBottom: last ? 0 : 11, borderBottom: last ? "none" : "1px solid var(--border-soft)" }}>
    <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
      <span style={{ fontSize: 11, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".04em" }}>{label}</span>
      <div style={{ flex: 1 }} />
      {!missing && <span className="mono" style={{ fontSize: 10.5, color: over ? "var(--sev-opp)" : "var(--ink-faint)" }}>{len} / {max} chars</span>}
    </div>
    {missing ? <span style={{ fontSize: 12.5, color: "var(--sev-issue)", fontWeight: 500 }}>— missing —</span>
      : <span style={{ fontSize: 12.5, color: "var(--ink)", lineHeight: 1.45 }}>{text}</span>}
  </div>;
}

function LinkList({ links, dir, empty, n }) {
  if (!links.length) return <Empty icon="unlink" title="Nothing stored">{empty}{n > 0 && <><br /><span className="mono" style={{ fontSize: 11 }}>({n} recorded in the crawl)</span></>}</Empty>;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      {links.slice(0, 60).map((l, i) => (
        <div key={i} className="card copyhost" style={{ padding: "9px 12px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <div className="mono" style={{ flex: 1, minWidth: 0, fontSize: 11.5, color: "var(--ink)", wordBreak: "break-all" }}>{urlShort(l[dir])}</div>
            <CopyButton text={l[dir]} />
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 6, flexWrap: "wrap" }}>
            <span className="badge code">{l.type}</span>
            {l.anchor && <span style={{ fontSize: 11, color: "var(--ink-3)" }}>“{l.anchor}”</span>}
            {l.position && <span style={{ fontSize: 10.5, color: "var(--ink-faint)" }}>{l.position}</span>}
            {l.nofollow && <span className="badge tint" style={{ "--c": "var(--sev-warn)", height: 18 }}>nofollow</span>}
            {l.origin === "rendered" && <span className="badge tint" style={{ "--c": "var(--sev-opp)", height: 18 }}>JS-only</span>}
          </div>
        </div>
      ))}
    </div>
  );
}
