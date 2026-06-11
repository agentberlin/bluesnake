/* ===========================================================================
   Crawl Manager (home) — all stored crawls
   =========================================================================== */
import React, { useState } from "react";
import { Icon, Btn, IconBtn, Search, SEV, Modal } from "../ui";
import { urlShort, hostOf } from "../api";

export function CrawlManager({ crawls, onOpen, onResume, onCompare, onNew, onDelete, storage }) {
  const [q, setQ] = useState("");
  const [confirm, setConfirm] = useState(null);
  const resumable = crawls.filter((c) => c.status === "interrupted");
  const filtered = crawls.filter((c) =>
    (c.seed + c.project + c.id).toLowerCase().includes(q.toLowerCase()));

  const statusMeta = {
    completed: { c: "var(--sev-ok)", label: "Completed" },
    interrupted: { c: "var(--sev-warn)", label: "Interrupted" },
    running: { c: "var(--accent)", label: "Running" },
  };
  const sevOf = (c) => ({ issue: c.issues, warning: c.warnings, opportunity: c.opportunities });

  return (
    <div className="main">
      <div className="toolbar">
        <span className="title">Crawls</span>
        <span className="pill mono">{crawls.length}</span>
        <div style={{ flex: 1 }} />
        <Search value={q} onChange={setQ} placeholder="Filter crawls…" width={220} />
        <Btn icon="git-compare" onClick={onCompare}>Compare</Btn>
        <Btn icon="plus" variant="primary" onClick={onNew}>New Crawl</Btn>
      </div>

      <div className="scroll" style={{ padding: 22 }}>
        <div style={{ maxWidth: 1080, margin: "0 auto" }}>

          {resumable.length > 0 && (
            <div className="fade" style={{ marginBottom: 22 }}>
              <div className="sb-sectlabel" style={{ padding: "0 0 8px" }}>Continue where you left off</div>
              {resumable.map((c) => (
                <div key={c.id} className="card" style={{ display: "flex", alignItems: "center", gap: 16, padding: 16, borderColor: "color-mix(in oklab, var(--sev-warn) 35%, var(--border))", background: "color-mix(in oklab, var(--sev-warn) 5%, var(--surface))" }}>
                  <div style={{ width: 38, height: 38, display: "flex", alignItems: "center", justifyContent: "center", background: "color-mix(in oklab, var(--sev-warn) 16%, transparent)", color: "var(--sev-warn)", flex: "0 0 38px" }}>
                    <Icon name="circle-pause" size={20} />
                  </div>
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ fontWeight: 650, fontSize: 13.5 }}>{urlShort(c.seed)}</div>
                    <div className="mono" style={{ fontSize: 11.5, color: "var(--ink-faint)", marginTop: 2 }}>
                      Paused at {c.crawled.toLocaleString()} URLs · {c.started} · nothing was lost
                    </div>
                  </div>
                  <Btn icon="trash-2" onClick={() => setConfirm(c)}>Discard</Btn>
                  <Btn icon="play" variant="primary" onClick={() => onResume(c)}>Resume crawl</Btn>
                </div>
              ))}
            </div>
          )}

          <div className="sb-sectlabel" style={{ padding: "0 0 8px" }}>All crawls</div>
          <div className="card" style={{ overflow: "hidden" }}>
            <div style={rowGrid(null, true)}>
              <div>Status</div><div>Site / Project</div>
              <div style={{ textAlign: "right" }}>URLs</div><div>Findings</div><div></div>
            </div>
            {filtered.length === 0 && (
              <div style={{ padding: "28px 16px", textAlign: "center", color: "var(--ink-faint)", fontSize: 12.5 }}>
                {crawls.length === 0 ? "No crawls yet — start one with New Crawl." : "Nothing matches the filter."}
              </div>
            )}
            {filtered.map((c) => {
              const sm = statusMeta[c.status] || statusMeta.completed;
              const sev = sevOf(c);
              return (
                <div key={c.id} className="crawl-row" style={rowGrid()} onClick={() => c.status === "completed" && onOpen(c)}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span className="statusdot" style={{ background: sm.c, boxShadow: `0 0 0 3px color-mix(in oklab, ${sm.c} 18%, transparent)` }} />
                    <span style={{ fontSize: 12, fontWeight: 600, color: c.status === "interrupted" ? "var(--sev-warn)" : "var(--ink-2)" }}>{sm.label}</span>
                  </div>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                      <span className="mono" style={{ fontSize: 12.5, fontWeight: 600, color: "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(c.seed)}</span>
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 4, fontSize: 10.5, fontWeight: 600, color: "var(--ink-3)", flex: "0 0 auto", padding: "1px 6px", background: "var(--surface-2)", border: "1px solid var(--border)" }}>
                        <Icon name={c.mode === "list" ? "list" : "radar"} size={11} />{c.mode === "list" ? "List" : "Spider"}
                      </span>
                    </div>
                    <div style={{ fontSize: 11, color: "var(--ink-faint)", marginTop: 2, display: "flex", gap: 6, alignItems: "center" }}>
                      <Icon name="folder" size={11} />
                      <span style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{c.project || hostOf(c.seed)}</span>
                      <span style={{ color: "var(--border-strong)" }}>·</span>
                      <span className="mono">{(c.started || "").split(" ")[0]}</span>
                    </div>
                  </div>
                  <div style={{ textAlign: "right" }}>
                    <div className="mono" style={{ fontSize: 12.5, fontWeight: 600 }}>{c.crawled.toLocaleString()}</div>
                  </div>
                  <div style={{ display: "flex", gap: 9 }}>
                    {["issue", "warning", "opportunity"].map((s) => (
                      <span key={s} title={SEV[s].label} style={{ display: "inline-flex", alignItems: "center", gap: 4, fontSize: 11.5, fontWeight: 600, color: SEV[s].c, fontFamily: "var(--font-mono)" }}>
                        <span className="statusdot" style={{ background: SEV[s].c }} />{sev[s] ?? 0}
                      </span>
                    ))}
                  </div>
                  <div style={{ display: "flex", justifyContent: "flex-end", gap: 2 }} onClick={(e) => e.stopPropagation()}>
                    {c.status === "interrupted"
                      ? <Btn size="sm" icon="play" variant="primary" onClick={() => onResume(c)}>Resume</Btn>
                      : <IconBtn icon="arrow-right" title="Open results" onClick={() => onOpen(c)} />}
                    <IconBtn icon="trash-2" title="Delete crawl" onClick={() => setConfirm(c)} />
                  </div>
                </div>
              );
            })}
          </div>

          <div style={{ marginTop: 14, display: "flex", alignItems: "center", gap: 8, fontSize: 11.5, color: "var(--ink-faint)" }}>
            <Icon name="shield-check" size={14} />
            Crawls auto-save continuously to <span className="mono">{storage ? storage.dir.replace(/^\/Users\/[^/]+/, "~") : "~/.bluesnake"}</span> — there is no Save button, and closing the app never loses a crawl.
          </div>
        </div>
      </div>

      {confirm && (
        <Modal onClose={() => setConfirm(null)} title="Delete crawl?" danger
          icon="trash-2"
          body={<>This permanently deletes the crawl of <b className="mono">{urlShort(confirm.seed)}</b> ({confirm.crawled.toLocaleString()} URLs) and all its data. This cannot be undone.</>}
          actions={<>
            <Btn onClick={() => setConfirm(null)}>Cancel</Btn>
            <Btn variant="primary" style={{ background: "var(--s-4xx)" }} onClick={() => { onDelete(confirm); setConfirm(null); }}>Delete permanently</Btn>
          </>} />
      )}
    </div>
  );
}

function rowGrid(color, header) {
  return {
    display: "grid",
    gridTemplateColumns: "112px minmax(0,1fr) 78px 104px 92px",
    gap: 14, alignItems: "center",
    padding: header ? "9px 16px" : "11px 16px",
    borderBottom: "1px solid var(--border-soft)",
    color: header ? "var(--ink-faint)" : (color || "var(--ink-2)"),
    fontWeight: header ? 600 : 400,
    textTransform: header ? "uppercase" : "none",
    letterSpacing: header ? ".05em" : "0",
    fontSize: header ? 10.5 : 12,
    background: header ? "var(--surface-2)" : "transparent",
  };
}
