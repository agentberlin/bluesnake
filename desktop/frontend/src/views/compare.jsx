/* ===========================================================================
   Compare — diff two stored crawls (compare.Run on the backend)
   =========================================================================== */
import React, { useMemo, useState } from "react";
import { Icon, Btn, Empty } from "../ui";
import { api, urlShort } from "../api";

export function CompareView({ crawls }) {
  const usable = crawls.filter((c) => c.status !== "running");
  const [prevId, setPrevId] = useState(usable[1] ? usable[1].id : (usable[0] ? usable[0].id : ""));
  const [currId, setCurrId] = useState(usable[0] ? usable[0].id : "");
  const [res, setRes] = useState(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("all");

  async function run() {
    if (!prevId || !currId) return;
    setBusy(true);
    setError("");
    try {
      setRes(await api.compareCrawls(prevId, currId));
    } catch (e) {
      setError(String(e));
      setRes(null);
    } finally {
      setBusy(false);
    }
  }

  const typeMeta = {
    added: { c: "var(--sev-ok)", icon: "plus", label: "Added" },
    removed: { c: "var(--s-4xx)", icon: "minus", label: "Removed" },
    changed: { c: "var(--sev-warn)", icon: "pencil", label: "Changed" },
  };

  const rows = useMemo(() => {
    if (!res) return [];
    const out = [];
    (res.NewPages || []).forEach((u) => out.push({ url: u, type: "added", detail: "New page — not present in the previous crawl" }));
    (res.MissingPages || []).forEach((u) => out.push({ url: u, type: "removed", detail: "No longer found in the current crawl" }));
    const byUrl = {};
    (res.Changes || []).forEach((c) => {
      (byUrl[c.URL] = byUrl[c.URL] || []).push(c);
    });
    Object.entries(byUrl).forEach(([u, cs]) => {
      out.push({ url: u, type: "changed", detail: cs.map((c) => `${c.Element}: ${trim(c.Previous)} → ${trim(c.Current)}`).join(" · ") });
    });
    return out;
  }, [res]);
  const trim = (s) => { s = String(s ?? ""); return s.length > 40 ? s.slice(0, 38) + "…" : (s || "—"); };

  const counts = {
    added: rows.filter((r) => r.type === "added").length,
    removed: rows.filter((r) => r.type === "removed").length,
    changed: rows.filter((r) => r.type === "changed").length,
  };
  const filtered = filter === "all" ? rows : rows.filter((r) => r.type === filter);

  const issuesDelta = useMemo(() => {
    if (!res || !res.Deltas) return null;
    let appeared = 0, resolved = 0;
    res.Deltas.forEach((d) => {
      appeared += (d.New || []).length + (d.Added || []).length;
      resolved += (d.Removed || []).length + (d.Missing || []).length;
    });
    return { appeared, resolved };
  }, [res]);

  if (usable.length < 2) {
    return (
      <div className="main">
        <div className="toolbar"><Icon name="git-compare" size={17} /><span className="title">Compare crawls</span></div>
        <Empty icon="git-compare" title="Need two crawls to compare">Run the same site twice (e.g. before and after a change) and the diff shows up here — added, removed and changed pages, plus per-issue deltas.</Empty>
      </div>
    );
  }

  return (
    <div className="main">
      <div className="toolbar"><Icon name="git-compare" size={17} /><span className="title">Compare crawls</span><div style={{ flex: 1 }} /></div>
      <div className="scroll" style={{ padding: 22 }}>
        <div style={{ maxWidth: 980, margin: "0 auto" }} className="fade">

          {/* crawl pickers */}
          <div className="card" style={{ padding: 16, display: "flex", alignItems: "center", gap: 14, marginBottom: 16 }}>
            <CrawlPick label="Previous" value={prevId} options={usable} onChange={setPrevId} />
            <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 3, color: "var(--ink-faint)" }}><Icon name="arrow-right" size={20} /></div>
            <CrawlPick label="Current" value={currId} options={usable} onChange={setCurrId} />
            <div style={{ flex: 1 }} />
            <Btn icon="git-compare" variant="primary" disabled={busy || prevId === currId} onClick={run}>{busy ? "Comparing…" : "Compare"}</Btn>
          </div>

          {prevId === currId && <div className="hint" style={{ marginBottom: 14 }}>Pick two different crawls — ideally of the same site, taken at different times.</div>}
          {error && <div style={{ marginBottom: 14, display: "flex", alignItems: "center", gap: 8, color: "var(--s-4xx)", fontSize: 12.5 }}><Icon name="circle-alert" size={15} />{error}</div>}

          {res && <>
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
                  <DeltaRow label="Previous crawl" value={(res.PagesPrevious || 0).toLocaleString()} />
                  <DeltaRow label="Current crawl" value={(res.PagesCurrent || 0).toLocaleString()} />
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
                <div key={i} className="datarow" style={{ display: "grid", gridTemplateColumns: "110px minmax(0,1fr) minmax(0,1.4fr)", gap: 12, alignItems: "center", padding: "10px 16px", borderBottom: "1px solid var(--border-soft)" }}>
                  <span className="badge tint" style={{ "--c": typeMeta[c.type].c }}><Icon name={typeMeta[c.type].icon} size={11} />{typeMeta[c.type].label}</span>
                  <span className="mono" title={c.url} style={{ fontSize: 11.5, color: "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(c.url)}</span>
                  <span title={c.detail} style={{ fontSize: 11.5, color: "var(--ink-3)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{c.detail}</span>
                </div>
              ))}
            </div>
          </>}

          {!res && !error && (
            <div style={{ marginTop: 30 }}>
              <Empty icon="git-compare" title="Pick two crawls and hit Compare">Added, removed and changed pages, element-level diffs (titles, descriptions, H1, word count…) and per-issue deltas.</Empty>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function CrawlPick({ label, value, options, onChange }) {
  return (
    <div style={{ flex: 1, minWidth: 0 }}>
      <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 6 }}>{label}</div>
      <select className="input mono" value={value} onChange={(e) => onChange(e.target.value)} style={{ fontSize: 12, fontWeight: 500 }}>
        {options.map((o) => <option key={o.id} value={o.id}>{urlShort(o.seed)} · {(o.started || "").split(" ")[0]} · {(o.total || o.crawled).toLocaleString()} URLs</option>)}
      </select>
    </div>
  );
}
function DeltaRow({ label, value, color }) {
  return <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", fontSize: 12.5 }}>
    <span style={{ color: "var(--ink-2)" }}>{label}</span><span className="mono" style={{ fontWeight: 600, color }}>{value}</span>
  </div>;
}
