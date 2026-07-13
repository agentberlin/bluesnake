/* ===========================================================================
   Compare — diff this crawl against another run of the SAME site over time.
   Lives in the crawl workspace rail, so the two sides are always the same
   site: added/removed/changed pages only make sense as a before/after of one
   site, never as two unrelated domains (cross-site benchmarking is Projects).

   The diff of the two most recent runs loads automatically. Results come from
   the backend's comparison cache (CompareCrawls is cache-first), so revisiting
   a pair is instant; "Recompute" forces a fresh diff and replaces the cached
   one. Payload fields are the snake_case JSON of desktop ComparePayload.
   =========================================================================== */
import React, { useEffect, useMemo, useState } from "react";
import { Icon, Btn, IconBtn, Empty, CopyButton, Search, SevDot, StatusBar, SEV } from "../ui";
import { api, urlShort, hostOf } from "../api";

const typeMeta = {
  added: { c: "var(--sev-ok)", icon: "plus", label: "Added" },
  removed: { c: "var(--s-4xx)", icon: "minus", label: "Removed" },
  status: { c: "var(--s-5xx)", icon: "activity", label: "Status" },
  indexability: { c: "var(--s-3xx)", icon: "eye-off", label: "Indexability" },
  changed: { c: "var(--sev-warn)", icon: "pencil", label: "Changed" },
};

export function CrawlCompare({ crawl, crawls, live }) {
  const host = hostOf(crawl.seed);
  // Other finished crawls of the *same* site, newest first — the only crawls it
  // makes sense to diff this one against.
  const siblings = useMemo(
    () => crawls
      .filter((c) => c.id !== crawl.id && c.status !== "running" && hostOf(c.seed) === host)
      .sort((a, b) => (b.started || "").localeCompare(a.started || "")),
    [crawls, crawl.id, host],
  );

  // Defaults to the most recent other run, so opening the tab on the latest
  // crawl compares the last two crawls of the site.
  const [baselineId, setBaselineId] = useState(siblings[0] ? siblings[0].id : "");
  const [res, setRes] = useState(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const baseline = siblings.find((c) => c.id === baselineId) || null;
  // keep the default pinned to the most recent sibling if the crawl list
  // arrives (or the chosen baseline is deleted) after mount
  useEffect(() => {
    if (!baseline && siblings.length) setBaselineId(siblings[0].id);
  }, [baseline, siblings]);
  // Order the pair chronologically so "added/removed" always read forward in
  // time regardless of which run you happen to be viewing.
  const [prevC, currC] = baseline && (baseline.started || "") > (crawl.started || "")
    ? [crawl, baseline] : [baseline, crawl];

  async function run(force) {
    if (!baseline || live) return;
    setBusy(true);
    setError("");
    try {
      setRes(await api.compareCrawls(prevC.id, currC.id, force));
    } catch (e) {
      setError(String(e));
      setRes(null);
    } finally {
      setBusy(false);
    }
  }

  // Auto-compare on open and whenever the baseline changes; the cache makes
  // repeat visits instant, and a first-time pair computes once and is stored.
  useEffect(() => {
    setRes(null);
    if (baseline && !live) run(false);
  }, [crawl.id, baselineId, live]); // eslint-disable-line react-hooks/exhaustive-deps

  const header = (
    <div className="toolbar">
      <Icon name="git-compare" size={17} />
      <span className="title" style={{ fontSize: 13.5 }}>Compare over time</span>
      <div style={{ flex: 1 }} />
      {res && (
        <>
          <span style={{ fontSize: 11, color: "var(--ink-faint)", display: "flex", alignItems: "center", gap: 5 }}>
            <Icon name={res.cached ? "database" : "check"} size={12} />
            {res.cached ? `cached · computed ${res.computed_at}` : `computed ${res.computed_at}`}
          </span>
          <Btn size="sm" icon="refresh-cw" disabled={busy} onClick={() => run(true)} title="Discard the cached result and diff the two crawls again">Recompute</Btn>
        </>
      )}
    </div>
  );

  if (live) {
    return (
      <div className="main" style={{ minWidth: 0 }}>
        {header}
        <Empty icon="git-compare" title="Compare is ready once this crawl finishes">
          When the crawl completes it becomes a point in {host}'s history you can diff against earlier runs — added, removed and changed pages, status flips and per-issue deltas.
        </Empty>
      </div>
    );
  }

  if (siblings.length === 0) {
    return (
      <div className="main" style={{ minWidth: 0 }}>
        {header}
        <Empty icon="git-compare" title={`Only one crawl of ${host}`}>
          Compare shows what changed between two runs of the same site — added, removed and changed pages, status-code and indexability flips, element-level diffs (titles, descriptions, H1, word count…) and per-issue deltas. Crawl <b className="mono">{host}</b> again and its diff shows up here.
        </Empty>
      </div>
    );
  }

  return (
    <div className="main" style={{ minWidth: 0 }}>
      {header}
      <div className="scroll" style={{ padding: 22 }}>
        <div style={{ maxWidth: 1080, margin: "0 auto" }} className="fade">

          {/* run picker — the current crawl is one fixed side */}
          <div className="card" style={{ padding: "13px 16px", display: "flex", alignItems: "center", gap: 12, marginBottom: 14 }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 6 }}>Compare against</div>
              <select className="input mono" value={baselineId} onChange={(e) => setBaselineId(e.target.value)} style={{ fontSize: 12, fontWeight: 500 }}>
                {siblings.map((o) => <option key={o.id} value={o.id}>{(o.started || "").split(" ")[0]} · {(o.total || o.crawled).toLocaleString()} URLs</option>)}
              </select>
            </div>
            <Icon name="arrow-right" size={15} style={{ color: "var(--ink-faint)", marginTop: 16 }} />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 6 }}>This crawl</div>
              <div className="input mono" style={{ fontSize: 12, fontWeight: 500, display: "flex", alignItems: "center", background: "var(--surface-2)", color: "var(--ink-2)" }}>{(crawl.started || "").split(" ")[0]} · {(crawl.total || crawl.crawled).toLocaleString()} URLs</div>
            </div>
            {baseline && (
              <div className="hint" style={{ marginTop: 16, whiteSpace: "nowrap" }}>
                <span className="mono">{(prevC.started || "").split(" ")[0]}</span> → <span className="mono">{(currC.started || "").split(" ")[0]}</span>
              </div>
            )}
          </div>

          {error && (
            <div style={{ marginBottom: 14, display: "flex", alignItems: "center", gap: 8, color: "var(--s-4xx)", fontSize: 12.5 }}>
              <Icon name="circle-alert" size={15} />{error}
              <Btn size="sm" onClick={() => run(false)}>Retry</Btn>
            </div>
          )}

          {busy && !res && (
            <div style={{ padding: "60px 0", display: "flex", alignItems: "center", justifyContent: "center", gap: 9, color: "var(--ink-faint)", fontSize: 12.5 }}>
              <Icon name="loader" size={16} style={{ animation: "spin 1s linear infinite" }} />Comparing the two crawls…
            </div>
          )}

          {res && <CompareResult res={res} />}
        </div>
      </div>
    </div>
  );
}

/* ---- the diff itself (also rendered by the Projects diff modal) ---------- */
export function CompareResult({ res }) {
  const [filter, setFilter] = useState("all");
  const [q, setQ] = useState("");

  // one unified, filterable URL-change list built from every diff family
  const rows = useMemo(() => buildRows(res), [res]);

  const counts = useMemo(() => {
    const c = { added: res.new_count || 0, removed: res.missing_count || 0, status: 0, indexability: 0, changed: 0 };
    rows.forEach((r) => { if (r.type === "status" || r.type === "indexability" || r.type === "changed") c[r.type]++; });
    // exact totals from the payload beat counts derived from the capped lists
    if (res.status_flip_count != null) c.status = res.status_flip_count;
    if (res.indexability_flip_count != null) c.indexability = res.indexability_flip_count;
    return c;
  }, [rows, res]);

  const filtered = useMemo(() => {
    let out = filter === "all" ? rows : rows.filter((r) => r.type === filter);
    const needle = q.trim().toLowerCase();
    if (needle) out = out.filter((r) => r.url.toLowerCase().includes(needle) || (r.detail || "").toLowerCase().includes(needle));
    return out;
  }, [rows, filter, q]);

  const pagesDelta = (res.pages_curr || 0) - (res.pages_prev || 0);

  return (
    <>
      {/* headline: how the site moved between the runs */}
      <div style={{ display: "grid", gridTemplateColumns: "1.2fr repeat(3,1fr)", gap: 12, marginBottom: 14 }}>
        <div className="card" style={{ padding: 16 }}>
          <div style={{ fontSize: 11, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em" }}>Pages</div>
          <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginTop: 8 }}>
            <span className="mono" style={{ fontSize: 26, fontWeight: 600 }}>{(res.pages_prev || 0).toLocaleString()}</span>
            <Icon name="arrow-right" size={14} style={{ color: "var(--ink-faint)" }} />
            <span className="mono" style={{ fontSize: 26, fontWeight: 600 }}>{(res.pages_curr || 0).toLocaleString()}</span>
            {pagesDelta !== 0 && (
              <span className="mono" style={{ fontSize: 12.5, fontWeight: 600, color: pagesDelta > 0 ? "var(--sev-ok)" : "var(--s-4xx)" }}>
                {pagesDelta > 0 ? "+" : ""}{pagesDelta.toLocaleString()}
              </span>
            )}
          </div>
        </div>
        {["added", "removed", "changed"].map((k) => (
          <div key={k} className="card" style={{ padding: 16, cursor: "default", borderColor: filter === k ? typeMeta[k].c : "var(--border)" }}
            onClick={() => setFilter(filter === k ? "all" : k)} title="Click to filter the list below">
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ width: 22, height: 22, background: `color-mix(in oklab, ${typeMeta[k].c} 16%, transparent)`, color: typeMeta[k].c, display: "flex", alignItems: "center", justifyContent: "center" }}><Icon name={typeMeta[k].icon} size={14} /></span>
              <span style={{ fontSize: 12, fontWeight: 600 }}>{typeMeta[k].label} pages</span>
            </div>
            <div className="mono" style={{ fontSize: 26, fontWeight: 600, marginTop: 8, color: typeMeta[k].c }}>{counts[k].toLocaleString()}</div>
          </div>
        ))}
      </div>

      {/* site shape: response mix + indexability, before vs after */}
      <ShapeShift res={res} counts={counts} setFilter={setFilter} />

      {/* per-issue movement */}
      <IssueMovement deltas={res.issue_deltas || []} />

      {/* unified change list */}
      <div className="card" style={{ overflow: "hidden" }}>
        <div style={{ padding: "10px 16px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
          <span style={{ fontSize: 12.5, fontWeight: 650 }}>Changed URLs</span>
          <span className="mono" style={{ fontSize: 11, color: "var(--ink-faint)" }}>{filtered.length.toLocaleString()}</span>
          <div style={{ display: "flex", gap: 5, flexWrap: "wrap" }}>
            {["all", "added", "removed", "status", "indexability", "changed"].map((k) => (
              <button key={k} className="pill" onClick={() => setFilter(k)}
                style={{
                  height: 22, fontSize: 11, fontWeight: 600, cursor: "default",
                  color: k === "all" ? undefined : typeMeta[k].c,
                  borderColor: filter === k ? (k === "all" ? "var(--border-strong)" : typeMeta[k].c) : "var(--border)",
                  background: filter === k ? (k === "all" ? "var(--surface-hover)" : `color-mix(in oklab, ${typeMeta[k].c} 10%, transparent)`) : "transparent",
                }}>
                {k === "all" ? "All" : typeMeta[k].label}
              </button>
            ))}
          </div>
          <div style={{ flex: 1 }} />
          <Search value={q} onChange={setQ} placeholder="Filter URLs" width={200} />
        </div>
        {filtered.length === 0 && (
          <div style={{ padding: "22px 16px", fontSize: 12.5, color: "var(--ink-faint)", display: "flex", gap: 8, alignItems: "center" }}>
            <Icon name="circle-check" size={14} style={{ color: "var(--sev-ok)" }} />
            {q ? "Nothing matches the filter." : "No differences in this category."}
          </div>
        )}
        {filtered.slice(0, 500).map((c, i) => (
          <div key={i} className="datarow copyhost" style={{ display: "grid", gridTemplateColumns: "110px minmax(0,1fr) minmax(0,1.4fr)", gap: 12, alignItems: "center", padding: "9px 16px", borderBottom: "1px solid var(--border-soft)" }}>
            <span className="badge tint" style={{ "--c": typeMeta[c.type].c }}><Icon name={typeMeta[c.type].icon} size={11} />{typeMeta[c.type].label}</span>
            <span style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
              <span className="mono" title={c.url} style={{ flex: 1, minWidth: 0, fontSize: 11.5, color: "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(c.url)}</span>
              <CopyButton text={c.url} />
            </span>
            <span title={c.detail} style={{ fontSize: 11.5, color: "var(--ink-3)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{c.detail}</span>
          </div>
        ))}
        {filtered.length > 500 && (
          <div style={{ padding: "10px 16px", fontSize: 11.5, color: "var(--ink-faint)" }}>
            Showing the first 500 of {filtered.length.toLocaleString()} — narrow with the search box, or export from the CLI: <span className="mono">bluesnake compare</span>.
          </div>
        )}
      </div>
    </>
  );
}

/* Flatten the payload's diff families into one list of typed URL rows. */
function buildRows(res) {
  const out = [];
  const pageNote = (b) => [b.status ? `HTTP ${b.status}` : null, b.title || null].filter(Boolean).join(" · ");
  (res.new_pages || []).forEach((b) => out.push({ url: b.url, type: "added", detail: ["New page", pageNote(b)].filter(Boolean).join(" · ") }));
  (res.missing_pages || []).forEach((b) => out.push({ url: b.url, type: "removed", detail: ["No longer found", b.title ? `was “${b.title}”` : null].filter(Boolean).join(" · ") }));
  (res.state_changes || []).forEach((s) => {
    if (s.prev_status !== s.curr_status) {
      out.push({ url: s.url, type: "status", detail: `HTTP ${s.prev_status} → ${s.curr_status}` });
    }
    if (s.prev_indexable !== s.curr_indexable) {
      const label = (ok, why) => ok ? "Indexable" : `Non-indexable${why ? ` (${why})` : ""}`;
      out.push({ url: s.url, type: "indexability", detail: `${label(s.prev_indexable, s.prev_indexability)} → ${label(s.curr_indexable, s.curr_indexability)}` });
    }
  });
  const trim = (v) => { v = String(v ?? ""); return v.length > 40 ? v.slice(0, 38) + "…" : (v || "—"); };
  const byUrl = {};
  (res.element_changes || []).forEach((c) => { (byUrl[c.url] = byUrl[c.url] || []).push(c); });
  Object.entries(byUrl).forEach(([u, cs]) => {
    out.push({ url: u, type: "changed", detail: cs.map((c) => c.element === "content" ? `content: now ${c.current}` : `${c.element}: ${trim(c.previous)} → ${trim(c.current)}`).join(" · ") });
  });
  return out;
}

/* ---- response-mix + indexability shift ---------------------------------- */
function ShapeShift({ res, counts, setFilter }) {
  const idxPct = (n, non) => {
    const total = n + non;
    return total ? Math.round((n / total) * 100) : 0;
  };
  const prevPct = idxPct(res.indexable_prev || 0, res.non_indexable_prev || 0);
  const currPct = idxPct(res.indexable_curr || 0, res.non_indexable_curr || 0);
  const hasMix = Object.values(res.status_mix_prev || {}).some(Boolean) || Object.values(res.status_mix_curr || {}).some(Boolean);
  if (!hasMix && !counts.status && !counts.indexability) return null;
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1.4fr 1fr", gap: 12, marginBottom: 14 }}>
      <div className="card" style={{ padding: 16 }}>
        <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 12 }}>Response codes, before → after</div>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span style={{ fontSize: 11, color: "var(--ink-faint)", width: 46 }}>Earlier</span>
            <div style={{ flex: 1 }}><StatusBar status={res.status_mix_prev || {}} /></div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span style={{ fontSize: 11, color: "var(--ink-faint)", width: 46 }}>Later</span>
            <div style={{ flex: 1 }}><StatusBar status={res.status_mix_curr || {}} /></div>
          </div>
        </div>
        {counts.status > 0 && (
          <div style={{ marginTop: 12, fontSize: 11.5, color: "var(--ink-2)", display: "flex", alignItems: "center", gap: 6 }}>
            <Icon name="activity" size={13} style={{ color: typeMeta.status.c }} />
            <span><b className="mono">{counts.status}</b> page{counts.status === 1 ? "" : "s"} changed status code</span>
            <Btn size="sm" variant="ghost" onClick={() => setFilter("status")}>View</Btn>
          </div>
        )}
      </div>
      <div className="card" style={{ padding: 16 }}>
        <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 12 }}>Indexability</div>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span className="mono" style={{ fontSize: 24, fontWeight: 600 }}>{prevPct}%</span>
          <Icon name="arrow-right" size={13} style={{ color: "var(--ink-faint)" }} />
          <span className="mono" style={{ fontSize: 24, fontWeight: 600, color: currPct >= prevPct ? "var(--sev-ok)" : "var(--s-4xx)" }}>{currPct}%</span>
          <span style={{ fontSize: 11.5, color: "var(--ink-3)" }}>of internal URLs indexable</span>
        </div>
        <div style={{ marginTop: 8, fontSize: 11.5, color: "var(--ink-3)" }}>
          {(res.indexable_prev || 0).toLocaleString()} → {(res.indexable_curr || 0).toLocaleString()} indexable pages
        </div>
        {counts.indexability > 0 && (
          <div style={{ marginTop: 10, fontSize: 11.5, color: "var(--ink-2)", display: "flex", alignItems: "center", gap: 6 }}>
            <Icon name="eye-off" size={13} style={{ color: typeMeta.indexability.c }} />
            <span><b className="mono">{counts.indexability}</b> page{counts.indexability === 1 ? "" : "s"} flipped</span>
            <Btn size="sm" variant="ghost" onClick={() => setFilter("indexability")}>View</Btn>
          </div>
        )}
      </div>
    </div>
  );
}

/* ---- per-issue movement table ------------------------------------------- */
function IssueMovement({ deltas }) {
  const [open, setOpen] = useState(null); // issue id
  const worse = (d) => d.curr_count - d.prev_count;
  const appeared = deltas.reduce((n, d) => n + (d.added_count || 0) + (d.new_count || 0), 0);
  const resolved = deltas.reduce((n, d) => n + (d.removed_count || 0) + (d.missing_count || 0), 0);
  return (
    <div className="card" style={{ overflow: "hidden", marginBottom: 14 }}>
      <div style={{ padding: "11px 16px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ fontSize: 12.5, fontWeight: 650 }}>Issue movement</span>
        {deltas.length > 0 && (
          <>
            <span className="badge tint" style={{ "--c": "var(--s-4xx)" }}>+{appeared.toLocaleString()} appeared</span>
            <span className="badge tint" style={{ "--c": "var(--sev-ok)" }}>−{resolved.toLocaleString()} resolved</span>
          </>
        )}
        <div style={{ flex: 1 }} />
        <span className="hint">occurrences that entered or left each check between the runs</span>
      </div>
      {deltas.length === 0 && (
        <div style={{ padding: "20px 16px", fontSize: 12.5, color: "var(--sev-ok)", display: "flex", alignItems: "center", gap: 8 }}>
          <Icon name="circle-check" size={15} />No issue moved between these two crawls.
        </div>
      )}
      {deltas.map((d) => {
        const net = worse(d);
        const isOpen = open === d.id;
        return (
          <React.Fragment key={d.id}>
            <div className="datarow" onClick={() => setOpen(isOpen ? null : d.id)}
              style={{ display: "grid", gridTemplateColumns: "14px 18px minmax(0,1fr) 150px 90px", gap: 10, alignItems: "center", padding: "9px 16px", borderBottom: "1px solid var(--border-soft)", cursor: "default" }}>
              <Icon name={isOpen ? "chevron-down" : "chevron-right"} size={13} style={{ color: "var(--ink-faint)" }} />
              <SevDot severity={d.severity} />
              <span style={{ fontSize: 12.5, fontWeight: 500, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }} title={(SEV[d.severity] || {}).label ? `${SEV[d.severity].label} · ${d.name}` : d.name}>{d.name}</span>
              <span className="mono" style={{ fontSize: 12, color: "var(--ink-2)", textAlign: "right" }}>
                {(d.prev_count || 0).toLocaleString()} → {(d.curr_count || 0).toLocaleString()}
              </span>
              <span className="mono" style={{ fontSize: 12, fontWeight: 650, textAlign: "right", color: net > 0 ? "var(--s-4xx)" : net < 0 ? "var(--sev-ok)" : "var(--ink-faint)" }}>
                {net > 0 ? `+${net}` : net === 0 ? "±0" : net}
              </span>
            </div>
            {isOpen && <IssueBuckets d={d} />}
          </React.Fragment>
        );
      })}
    </div>
  );
}

/* The four SF buckets for one issue, shown when its row is expanded. */
function IssueBuckets({ d }) {
  const buckets = [
    { key: "added", label: "Started failing", hint: "page in both crawls, entered the issue", urls: d.added || [], total: d.added_count || 0, c: "var(--s-4xx)" },
    { key: "new", label: "New page with this issue", hint: "page added to the site, has the issue", urls: d.new || [], total: d.new_count || 0, c: "var(--s-4xx)" },
    { key: "removed", label: "Fixed", hint: "page in both crawls, left the issue", urls: d.removed || [], total: d.removed_count || 0, c: "var(--sev-ok)" },
    { key: "missing", label: "Page gone", hint: "page no longer on the site", urls: d.missing || [], total: d.missing_count || 0, c: "var(--ink-3)" },
  ].filter((b) => b.total > 0);
  return (
    <div style={{ padding: "6px 16px 14px 58px", borderBottom: "1px solid var(--border-soft)", background: "var(--surface-2)", display: "flex", flexDirection: "column", gap: 10 }}>
      {buckets.map((b) => (
        <div key={b.key}>
          <div style={{ display: "flex", alignItems: "center", gap: 7, margin: "6px 0 4px" }}>
            <span className="badge tint" style={{ "--c": b.c }}>{b.label}</span>
            <span className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)" }}>{b.total.toLocaleString()}</span>
            <span className="hint">{b.hint}</span>
          </div>
          {b.urls.slice(0, 50).map((u) => (
            <div key={u} className="copyhost" style={{ display: "flex", alignItems: "center", gap: 6, padding: "2px 0" }}>
              <span className="mono" title={u} style={{ fontSize: 11, color: "var(--ink-2)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(u)}</span>
              <CopyButton text={u} />
            </div>
          ))}
          {b.total > 50 && <div style={{ fontSize: 10.5, color: "var(--ink-faint)", marginTop: 2 }}>…and {(b.total - 50).toLocaleString()} more</div>}
        </div>
      ))}
    </div>
  );
}
