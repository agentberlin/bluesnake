/* ===========================================================================
   Compare — diff this crawl against another run of the SAME site over time
   (compare.Run on the backend). Lives in the crawl workspace rail, so the two
   sides are always the same site: added/removed/changed pages only make sense
   as a before/after of one site, never as two unrelated domains. Cross-site
   benchmarking is a separate concern and lives in Projects.
   =========================================================================== */
import React, { useMemo, useState } from "react";
import { Icon, Btn, Empty, CopyButton } from "../ui";
import { api, urlShort, hostOf } from "../api";

const typeMeta = {
  added: { c: "var(--sev-ok)", icon: "plus", label: "Added" },
  removed: { c: "var(--s-4xx)", icon: "minus", label: "Removed" },
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

  const [baselineId, setBaselineId] = useState(siblings[0] ? siblings[0].id : "");
  const [res, setRes] = useState(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("all");

  const baseline = siblings.find((c) => c.id === baselineId) || null;
  // Order the pair chronologically so "added/removed" always read forward in
  // time regardless of which run you happen to be viewing.
  const [prevC, currC] = baseline && (baseline.started || "") > (crawl.started || "")
    ? [crawl, baseline] : [baseline, crawl];

  async function run() {
    if (!baseline) return;
    setBusy(true);
    setError("");
    try {
      setRes(await api.compareCrawls(prevC.id, currC.id));
    } catch (e) {
      setError(String(e));
      setRes(null);
    } finally {
      setBusy(false);
    }
  }

  const header = (
    <div className="toolbar">
      <Icon name="git-compare" size={17} />
      <span className="title" style={{ fontSize: 13.5 }}>Compare over time</span>
      <div style={{ flex: 1 }} />
    </div>
  );

  if (live) {
    return (
      <div className="main" style={{ minWidth: 0 }}>
        {header}
        <Empty icon="git-compare" title="Compare is ready once this crawl finishes">
          When the crawl completes it becomes a point in {host}'s history you can diff against earlier runs — added, removed and changed pages, plus per-issue deltas.
        </Empty>
      </div>
    );
  }

  if (siblings.length === 0) {
    return (
      <div className="main" style={{ minWidth: 0 }}>
        {header}
        <Empty icon="git-compare" title={`Only one crawl of ${host}`}>
          Compare shows what changed between two runs of the same site — added, removed and changed pages, element-level diffs (titles, descriptions, H1, word count…) and per-issue deltas. Crawl <b className="mono">{host}</b> again and its diff shows up here.
        </Empty>
      </div>
    );
  }

  return (
    <div className="main" style={{ minWidth: 0 }}>
      {header}
      <div className="scroll" style={{ padding: 22 }}>
        <div style={{ maxWidth: 980, margin: "0 auto" }} className="fade">

          {/* baseline picker — the current crawl is one fixed side */}
          <div className="card" style={{ padding: 16, display: "flex", alignItems: "flex-end", gap: 14, marginBottom: 16 }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 6 }}>Compare against</div>
              <select className="input mono" value={baselineId} onChange={(e) => { setBaselineId(e.target.value); setRes(null); }} style={{ fontSize: 12, fontWeight: 500 }}>
                {siblings.map((o) => <option key={o.id} value={o.id}>{(o.started || "").split(" ")[0]} · {(o.total || o.crawled).toLocaleString()} URLs</option>)}
              </select>
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 6 }}>This crawl</div>
              <div className="input mono" style={{ fontSize: 12, fontWeight: 500, display: "flex", alignItems: "center", background: "var(--surface-2)", color: "var(--ink-2)" }}>{(crawl.started || "").split(" ")[0]} · {(crawl.total || crawl.crawled).toLocaleString()} URLs</div>
            </div>
            <Btn icon="git-compare" variant="primary" disabled={busy || !baseline} onClick={run}>{busy ? "Comparing…" : "Compare"}</Btn>
          </div>

          {baseline && (
            <div className="hint" style={{ marginBottom: 14, display: "flex", alignItems: "center", gap: 7 }}>
              <span className="mono">{(prevC.started || "").split(" ")[0]}</span>
              <Icon name="arrow-right" size={13} />
              <span className="mono">{(currC.started || "").split(" ")[0]}</span>
              <span>· diff of {host} between the two runs</span>
            </div>
          )}
          {error && <div style={{ marginBottom: 14, display: "flex", alignItems: "center", gap: 8, color: "var(--s-4xx)", fontSize: 12.5 }}><Icon name="circle-alert" size={15} />{error}</div>}

          {res
            ? <CompareResult res={res} filter={filter} setFilter={setFilter} />
            : !error && (
              <div style={{ marginTop: 30 }}>
                <Empty icon="git-compare" title="Pick a run and hit Compare">Added, removed and changed pages, element-level diffs (titles, descriptions, H1, word count…) and per-issue deltas.</Empty>
              </div>
            )}
        </div>
      </div>
    </div>
  );
}

/* ---- the diff itself (delta cards + issue delta + changed URLs) --------- */
function CompareResult({ res, filter, setFilter }) {
  const trim = (s) => { s = String(s ?? ""); return s.length > 40 ? s.slice(0, 38) + "…" : (s || "—"); };
  const rows = useMemo(() => {
    const out = [];
    (res.NewPages || []).forEach((u) => out.push({ url: u, type: "added", detail: "New page — not present in the earlier crawl" }));
    (res.MissingPages || []).forEach((u) => out.push({ url: u, type: "removed", detail: "No longer found in the later crawl" }));
    const byUrl = {};
    (res.Changes || []).forEach((c) => { (byUrl[c.URL] = byUrl[c.URL] || []).push(c); });
    Object.entries(byUrl).forEach(([u, cs]) => {
      out.push({ url: u, type: "changed", detail: cs.map((c) => `${c.Element}: ${trim(c.Previous)} → ${trim(c.Current)}`).join(" · ") });
    });
    return out;
  }, [res]);

  const counts = {
    added: rows.filter((r) => r.type === "added").length,
    removed: rows.filter((r) => r.type === "removed").length,
    changed: rows.filter((r) => r.type === "changed").length,
  };
  const filtered = filter === "all" ? rows : rows.filter((r) => r.type === filter);

  const issuesDelta = useMemo(() => {
    if (!res.Deltas) return null;
    let appeared = 0, resolved = 0;
    res.Deltas.forEach((d) => {
      appeared += (d.New || []).length + (d.Added || []).length;
      resolved += (d.Removed || []).length + (d.Missing || []).length;
    });
    return { appeared, resolved };
  }, [res]);

  return (
    <>
      {/* delta summary */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(3,1fr)", gap: 12, marginBottom: 14 }}>
        {["added", "removed", "changed"].map((k) => (
          <div key={k} className="card" style={{ padding: 16, cursor: "default", borderColor: filter === k ? typeMeta[k].c : "var(--border)" }} onClick={() => setFilter(filter === k ? "all" : k)}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}><span style={{ width: 22, height: 22, background: `color-mix(in oklab, ${typeMeta[k].c} 16%, transparent)`, color: typeMeta[k].c, display: "flex", alignItems: "center", justifyContent: "center" }}><Icon name={typeMeta[k].icon} size={14} /></span><span style={{ fontSize: 12, fontWeight: 600 }}>{typeMeta[k].label} pages</span></div>
            <div className="mono" style={{ fontSize: 26, fontWeight: 600, marginTop: 8, color: typeMeta[k].c }}>{counts[k]}</div>
          </div>
        ))}
      </div>

      {/* issues delta + totals */}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12, marginBottom: 14 }}>
        <div className="card" style={{ padding: 16 }}>
          <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 12 }}>Issues delta</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
            <DeltaRow label="New issue occurrences" value={`+${issuesDelta ? issuesDelta.appeared : 0}`} color="var(--s-4xx)" />
            <DeltaRow label="Resolved" value={`−${issuesDelta ? issuesDelta.resolved : 0}`} color="var(--sev-ok)" />
            <DeltaRow label="Net change" value={`${issuesDelta ? issuesDelta.appeared - issuesDelta.resolved : 0}`} color={issuesDelta && issuesDelta.appeared <= issuesDelta.resolved ? "var(--sev-ok)" : "var(--s-4xx)"} />
          </div>
        </div>
        <div className="card" style={{ padding: 16 }}>
          <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 12 }}>Pages compared</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
            <DeltaRow label="Earlier crawl" value={(res.PagesPrevious || 0).toLocaleString()} />
            <DeltaRow label="Later crawl" value={(res.PagesCurrent || 0).toLocaleString()} />
          </div>
        </div>
      </div>

      {/* change list */}
      <div className="card" style={{ overflow: "hidden" }}>
        <div style={{ padding: "11px 16px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ fontSize: 12.5, fontWeight: 650 }}>Changed URLs</span>
          <span className="mono" style={{ fontSize: 11, color: "var(--ink-faint)" }}>{filtered.length}</span>
          {filter !== "all" && <Btn size="sm" variant="ghost" icon="x" onClick={() => setFilter("all")}>Clear</Btn>}
        </div>
        {filtered.length === 0 && <div style={{ padding: "22px 16px", fontSize: 12.5, color: "var(--ink-faint)", display: "flex", gap: 8, alignItems: "center" }}><Icon name="circle-check" size={14} style={{ color: "var(--sev-ok)" }} />No differences in this category.</div>}
        {filtered.slice(0, 500).map((c, i) => (
          <div key={i} className="datarow copyhost" style={{ display: "grid", gridTemplateColumns: "110px minmax(0,1fr) minmax(0,1.4fr)", gap: 12, alignItems: "center", padding: "10px 16px", borderBottom: "1px solid var(--border-soft)" }}>
            <span className="badge tint" style={{ "--c": typeMeta[c.type].c }}><Icon name={typeMeta[c.type].icon} size={11} />{typeMeta[c.type].label}</span>
            <span style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
              <span className="mono" title={c.url} style={{ flex: 1, minWidth: 0, fontSize: 11.5, color: "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(c.url)}</span>
              <CopyButton text={c.url} />
            </span>
            <span title={c.detail} style={{ fontSize: 11.5, color: "var(--ink-3)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{c.detail}</span>
          </div>
        ))}
      </div>
    </>
  );
}

function DeltaRow({ label, value, color }) {
  return <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", fontSize: 12.5 }}>
    <span style={{ color: "var(--ink-2)" }}>{label}</span><span className="mono" style={{ fontWeight: 600, color }}>{value}</span>
  </div>;
}
