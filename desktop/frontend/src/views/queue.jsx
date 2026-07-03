/* ===========================================================================
   Queue — the persistent crawl queue. Every start enqueues a job; the single
   dispatcher runs them up to the parallel limit at a time
   (speed.max_concurrent_crawls, default 1). Rows live-refresh on
   crawl:started / crawl:done (see main.jsx).
   =========================================================================== */
import React from "react";
import { Icon, Btn, IconBtn, Empty } from "../ui";
import { hostOf } from "../api";

const STATUS = {
  queued: { label: "Queued", color: "var(--ink-faint)" },
  running: { label: "Running", color: "var(--accent)" },
  done: { label: "Done", color: "var(--sev-ok)" },
  failed: { label: "Failed", color: "var(--sev-warn)" },
  interrupted: { label: "Interrupted", color: "var(--sev-warn)" },
  canceled: { label: "Canceled", color: "var(--ink-faint)" },
};

export function QueueView({ jobs, liveCrawlIds, onRefresh, onCancel, onClear, onOpenCrawl, onNew }) {
  const list = jobs || [];
  const pending = list.filter((j) => j.status === "queued" || j.status === "running").length;

  return (
    <div className="main">
      <div className="toolbar">
        <Icon name="list-checks" size={17} />
        <span className="title">Queue</span>
        {pending > 0 && <span className="pill mono">{pending} pending</span>}
        <div style={{ flex: 1 }} />
        <Btn icon="rotate-cw" onClick={onRefresh}>Refresh</Btn>
        <Btn variant="primary" icon="plus" onClick={onNew}>New Crawl</Btn>
      </div>

      <div className="scroll" style={{ padding: 24 }}>
        {list.length === 0 ? (
          <Empty icon="list-checks" title="The queue is empty">
            Crawls you start are added here and run through the queue — up to your parallel limit at once. Use “Crawl all” on a project to queue every site at once.
          </Empty>
        ) : (
          <div style={{ maxWidth: 820, margin: "0 auto", display: "flex", flexDirection: "column", gap: 8 }}>
            {list.map((j) => {
              const st = STATUS[j.status] || { label: j.status, color: "var(--ink-faint)" };
              const live = j.crawlId && (liveCrawlIds || []).includes(j.crawlId);
              const terminal = ["done", "failed", "interrupted", "canceled"].includes(j.status);
              return (
                <div key={j.id} className="card" style={{ display: "flex", alignItems: "center", gap: 12, padding: "12px 14px" }}>
                  <span title={st.label} style={{
                    width: 9, height: 9, borderRadius: 9, flex: "0 0 9px", background: st.color,
                    boxShadow: live ? "0 0 0 3px color-mix(in oklab, var(--accent) 25%, transparent)" : "none",
                  }} />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div className="mono" style={{ fontSize: 13, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {hostOf(j.label) || j.label || "crawl"}
                    </div>
                    <div style={{ fontSize: 11, color: "var(--ink-faint)" }}>
                      {st.label}
                      {j.source === "project" ? " · project" : ""}
                      {j.error ? " · " + j.error : ""}
                    </div>
                  </div>
                  {j.crawlId && <Btn size="sm" icon="arrow-right" onClick={() => onOpenCrawl(j.crawlId)}>Open</Btn>}
                  {j.status === "queued" && <IconBtn icon="x" title="Cancel" onClick={() => onCancel(j.id)} />}
                  {j.status === "running" && <IconBtn icon="square" title="Stop" onClick={() => onCancel(j.id)} />}
                  {terminal && <IconBtn icon="trash-2" title="Remove from list" onClick={() => onClear(j.id)} />}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
