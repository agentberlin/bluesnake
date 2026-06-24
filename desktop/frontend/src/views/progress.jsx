/* ===========================================================================
   Live crawl progress — realtime via Wails events
   ("crawl:progress" ~4/s with counters + feed; "crawl:done" once at the end)
   =========================================================================== */
import React, { useEffect, useState } from "react";
import { Icon, Btn, StatusBadge, StatusBar, Ring, CopyButton } from "../ui";
import { api, on, urlShort } from "../api";

export function CrawlProgress({ crawlId, onOpenResults, onResume, headerExtra }) {
  const [snap, setSnap] = useState(null);
  const [done, setDone] = useState(null);
  const [state, setState] = useState("running"); // running | paused | done

  useEffect(() => {
    // rehydrate immediately, then stream
    api.activeProgress().then((p) => p && setSnap(p)).catch(() => {});
    const offProgress = on("crawl:progress", (p) => {
      if (crawlId && p.crawlId !== crawlId) return;
      setSnap(p);
    });
    const offDone = on("crawl:done", (d) => {
      if (crawlId && d.crawlId !== crawlId) return;
      setDone(d);
      setState(d.status === "interrupted" ? "paused" : "done");
    });
    return () => { offProgress(); offDone(); };
  }, [crawlId]);

  async function pause() { await api.pauseCrawl(); }
  async function stop() { await api.stopCrawl(); }
  async function resume() {
    setDone(null);
    setState("running");
    // Delegate to the app shell when embedded so liveCrawlId/activeCrawl stay in
    // sync; otherwise resume directly.
    if (onResume) onResume();
    else await api.resumeCrawl(crawlId);
  }

  const s = snap || { total: 0, discovered: 1, queue: 0, s2xx: 0, s3xx: 0, s4xx: 0, s5xx: 0, blocked: 0, noresp: 0, indexable: 0, rate: 0, elapsedSec: 0, threads: 0, feed: [], seed: "" };
  const status = { "2xx": s.s2xx, "3xx": s.s3xx, "4xx": s.s4xx, "5xx": s.s5xx, blocked: s.blocked, noresp: s.noresp };
  const discovered = Math.max(s.discovered, 1);
  const pct = Math.min(100, Math.round((s.total / discovered) * 100) || 0);
  const fmt = (sec) => `${Math.floor(sec / 60)}m ${String(sec % 60).padStart(2, "0")}s`;
  const host = urlShort(s.seed || "");
  const feed = s.feed || [];

  const stat = (label, value, sub, accent) => (
    <div style={{ flex: 1, padding: "16px 18px", borderRight: "1px solid var(--border-soft)" }}>
      <div style={{ fontSize: 11, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em" }}>{label}</div>
      <div className="mono" style={{ fontSize: 27, fontWeight: 600, marginTop: 5, color: accent || "var(--ink)", letterSpacing: "-.02em", fontVariantNumeric: "tabular-nums" }}>{value}</div>
      {sub && <div className="mono" style={{ fontSize: 11, color: "var(--ink-faint)", marginTop: 2 }}>{sub}</div>}
    </div>
  );

  return (
    <div className="main">
      <div className="toolbar">
        <span className="statusdot" style={{ background: state === "done" ? "var(--sev-ok)" : state === "paused" ? "var(--sev-warn)" : "var(--accent)", boxShadow: state === "running" ? "0 0 0 4px var(--accent-soft)" : "none", animation: state === "running" ? "pulse 1.4s infinite" : "none" }} />
        <span className="title">{state === "done" ? "Crawl complete" : state === "paused" ? "Crawl paused" : "Crawling"}</span>
        <span className="mono sub">{host}</span>
        <div style={{ flex: 1 }} />
        {headerExtra}
        {state === "running" && <><Btn icon="pause" onClick={pause}>Pause</Btn><Btn icon="square" onClick={stop}>Stop</Btn></>}
        {state === "paused" && <><Btn icon="play" variant="primary" onClick={resume}>Resume</Btn><Btn icon="arrow-right" onClick={() => onOpenResults(crawlId)}>View partial results</Btn></>}
        {state === "done" && <Btn icon="arrow-right" variant="primary" onClick={() => onOpenResults(crawlId)}>View results</Btn>}
      </div>

      <div className="scroll" style={{ padding: 22 }}>
        <div style={{ maxWidth: 960, margin: "0 auto" }} className="fade">

          {/* progress bar */}
          <div className="card" style={{ padding: 18, marginBottom: 16 }}>
            <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", marginBottom: 11 }}>
              <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
                <span className="mono" style={{ fontSize: 22, fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{s.total.toLocaleString()}</span>
                <span style={{ fontSize: 12.5, color: "var(--ink-3)" }}>of ~{discovered.toLocaleString()} discovered</span>
              </div>
              <span className="mono" style={{ fontSize: 13, color: "var(--ink-2)", fontWeight: 600 }}>{state === "done" ? "100" : pct}%</span>
            </div>
            <div style={{ height: 9, borderRadius: 6, background: "var(--border-soft)", overflow: "hidden", display: "flex", gap: 1.5 }}>
              {[["2xx", "var(--s-2xx)"], ["3xx", "var(--s-3xx)"], ["4xx", "var(--s-4xx)"], ["5xx", "var(--s-5xx)"], ["blocked", "var(--s-block)"], ["noresp", "var(--ink-faint)"]].map(([k, c]) =>
                status[k] ? <div key={k} style={{ width: (status[k] / discovered * 100) + "%", background: c, transition: "width .2s linear" }} /> : null)}
              <div style={{ flex: 1 }} />
            </div>
          </div>

          {/* stats */}
          <div className="card" style={{ display: "flex", padding: 0, overflow: "hidden", marginBottom: 16 }}>
            {stat("Queue", state !== "running" ? "0" : s.queue.toLocaleString(), state !== "running" ? "drained" : "URLs waiting")}
            {stat("Pages / sec", state !== "running" ? "—" : s.rate.toFixed(1), state !== "running" ? "finished" : "live")}
            {stat("Indexable", s.indexable.toLocaleString(), `${Math.max(0, s.total - s.indexable)} non-indexable`, "var(--sev-ok)")}
            <div style={{ flex: 1, padding: "16px 18px" }}>
              <div style={{ fontSize: 11, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em" }}>Elapsed</div>
              <div className="mono" style={{ fontSize: 27, fontWeight: 600, marginTop: 5, letterSpacing: "-.02em", fontVariantNumeric: "tabular-nums" }}>{fmt(s.elapsedSec)}</div>
              <div className="mono" style={{ fontSize: 11, color: "var(--ink-faint)", marginTop: 2 }}>{s.threads} threads</div>
            </div>
          </div>

          <div style={{ display: "grid", gridTemplateColumns: "1fr 1.1fr", gap: 16 }}>
            {/* status breakdown */}
            <div className="card" style={{ padding: 18 }}>
              <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 14 }}>Response codes</div>
              <StatusBar status={status} height={10} showLabels />
              <div style={{ marginTop: 18, paddingTop: 16, borderTop: "1px solid var(--border-soft)", display: "flex", alignItems: "center", gap: 11 }}>
                <Ring value={s.indexable} total={s.total || 1} size={46} color="var(--sev-ok)" />
                <div>
                  <div style={{ fontSize: 12.5, fontWeight: 600 }}>{s.total ? Math.round(s.indexable / s.total * 100) : 0}% indexable</div>
                  <div className="hint">{s.indexable.toLocaleString()} of {s.total.toLocaleString()} could be indexed by Google</div>
                </div>
              </div>
            </div>

            {/* live feed */}
            <div className="card" style={{ padding: 0, overflow: "hidden", display: "flex", flexDirection: "column", maxHeight: 320 }}>
              <div style={{ padding: "13px 16px 10px", display: "flex", alignItems: "center", gap: 8, borderBottom: "1px solid var(--border-soft)" }}>
                <Icon name="activity" size={14} style={{ color: "var(--ink-3)" }} />
                <span style={{ fontSize: 12.5, fontWeight: 650 }}>Live issues</span>
                <span className="pill mono" style={{ height: 18, fontSize: 10.5, marginLeft: 2 }}>{feed.length}</span>
              </div>
              <div style={{ overflowY: "auto", flex: 1 }}>
                {feed.length === 0 && <div style={{ padding: 24, textAlign: "center", color: "var(--ink-faint)", fontSize: 12 }}>No problems found yet — all clear.</div>}
                {feed.map((f) => (
                  <div key={f.seq} className="fade copyhost" style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 16px", borderBottom: "1px solid var(--border-soft)" }}>
                    <StatusBadge status={f.status} statusText={f.state} />
                    <span className="mono" style={{ fontSize: 11.5, color: "var(--ink-2)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", flex: 1 }}>{urlShort(f.url)}</span>
                    <CopyButton text={f.url} />
                    <span className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)" }}>#{f.seq}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* finish summary */}
          {done && state === "done" && (
            <div className="card fade" style={{ marginTop: 16, padding: 20, display: "flex", alignItems: "center", gap: 18 }}>
              <div style={{ width: 46, height: 46, flex: "0 0 46px", display: "flex", alignItems: "center", justifyContent: "center", background: "color-mix(in oklab, var(--sev-ok) 14%, transparent)", color: "var(--sev-ok)" }}><Icon name="circle-check" size={24} /></div>
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 14.5, fontWeight: 650 }}>Found {(done.total || done.crawled).toLocaleString()} URLs in {fmt(done.durationSec)}</div>
                <div style={{ fontSize: 12.5, color: "var(--ink-2)", marginTop: 3, display: "flex", gap: 16 }}>
                  {done.analyzed ? <span className="muted">analysis complete — issues evaluated, link scores computed</span> : <span className="muted">analysis pending</span>}
                  {done.error && <span style={{ color: "var(--s-4xx)" }}>{done.error}</span>}
                </div>
              </div>
              <Btn icon="arrow-right" variant="primary" onClick={() => onOpenResults(crawlId)}>View results</Btn>
            </div>
          )}

          <div style={{ marginTop: 16, display: "flex", alignItems: "center", justifyContent: "center", gap: 8, fontSize: 11.5, color: "var(--ink-faint)" }}>
            <Icon name="shield-check" size={14} />
            Everything crawled so far is already saved. You can pause, quit, even crash — resume picks up exactly here.
          </div>

        </div>
      </div>
    </div>
  );
}
