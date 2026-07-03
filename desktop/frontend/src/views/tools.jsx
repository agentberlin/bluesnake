/* ===========================================================================
   Tools — standalone site testers over the sitecheck engine (ToolsApp).
   Hub page (one card per registry entry) + per-tool sub-views. Every run is
   throwaway: computed live against the target, never persisted. The same
   checks run automatically during full-domain crawls (site_checks config)
   and surface in the issues view — the findings strip here renders the very
   same catalogue findings, visibly the same engine.
   =========================================================================== */
import React, { useEffect, useRef, useState } from "react";
import { Icon, IconBtn, Btn, Field, Toggle, SevTag, Empty, statusVar } from "../ui";
import { toolsApi, urlShort } from "../api";
import { RobotsTool } from "./robots";

/* Presentation for each registry tool (the registry itself carries name,
   summary and args; icons and long titles are a UI concern). */
const TOOL_META = {
  robots: { icon: "bot", title: "robots.txt Tester" },
  sitemap: { icon: "map", title: "Sitemap Tester" },
  aibots: { icon: "sparkles", title: "AI Bot Access" },
  render: { icon: "monitor-play", title: "JS Render Diff" },
  llms: { icon: "file-text", title: "llms.txt Tester" },
  structured: { icon: "braces", title: "Structured Data" },
  serp: { icon: "text-search", title: "SERP Snippet Preview" },
};

export function ToolsView({ initial, onConsumedInitial }) {
  const [tools, setTools] = useState([]);
  const [active, setActive] = useState(initial ? initial.tool : null);
  const [prefill, setPrefill] = useState(initial ? initial.target || "" : "");

  useEffect(() => {
    toolsApi.list().then((t) => setTools(t || [])).catch(() => {});
  }, []);
  // A later deep-link (issue row → tool) re-targets an already-open hub.
  useEffect(() => {
    if (!initial) return;
    setActive(initial.tool);
    setPrefill(initial.target || "");
    if (onConsumedInitial) onConsumedInitial();
  }, [initial]);

  function open(name) { setPrefill(""); setActive(name); }
  const back = () => setActive(null);
  const meta = active ? { ...(TOOL_META[active] || { icon: "wrench", title: active }), tool: tools.find((t) => t.name === active) } : null;

  if (!active) {
    return (
      <div className="main">
        <div className="toolbar"><Icon name="wrench" size={17} /><span className="title">Tools</span><span className="sub">standalone site testers — no crawl required, nothing saved</span></div>
        <div className="scroll" style={{ padding: 24 }}>
          <div style={{ maxWidth: 900, margin: "0 auto" }} className="fade">
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(270px, 1fr))", gap: 14 }}>
              {tools.map((t) => {
                const m = TOOL_META[t.name] || { icon: "wrench", title: t.name };
                return (
                  <div key={t.name} className="card" onClick={() => open(t.name)}
                    style={{ padding: 16, cursor: "pointer", display: "flex", flexDirection: "column", gap: 9 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
                      <Icon name={m.icon} size={17} style={{ color: "var(--accent)" }} />
                      <span style={{ fontSize: 13, fontWeight: 650 }}>{m.title}</span>
                    </div>
                    <div style={{ fontSize: 11.5, color: "var(--ink-3)", lineHeight: 1.55 }}>{t.summary}</div>
                  </div>
                );
              })}
            </div>
            <div style={{ marginTop: 18, display: "flex", alignItems: "center", gap: 8, fontSize: 11.5, color: "var(--ink-faint)" }}>
              <Icon name="radar" size={14} />
              Full-domain crawls run these checks automatically (Settings → site_checks) — findings land in the crawl's Issues view.
            </div>
          </div>
        </div>
      </div>
    );
  }

  const common = { onBack: back, meta, prefill };
  switch (active) {
    case "robots": return <RobotsTool {...common} />;
    case "sitemap": return <SitemapTool {...common} />;
    case "aibots": return <AIBotsTool {...common} />;
    case "render": return <RenderTool {...common} />;
    case "llms": return <LlmsTool {...common} />;
    case "structured": return <StructuredTool {...common} />;
    case "serp": return <SerpTool {...common} />;
    default: return null;
  }
}

/* ---- shared plumbing ---------------------------------------------------- */

/* One backend round-trip: run(name, target, args) → {report, findings}. */
export function useToolRun() {
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState(null);
  const [err, setErr] = useState("");
  const seq = useRef(0);
  async function run(name, target, args) {
    const mine = ++seq.current;
    setBusy(true); setErr("");
    try {
      const r = await toolsApi.run(name, target, args);
      if (mine === seq.current) setRes(r);
    } catch (e) {
      if (mine === seq.current) { setErr(String(e && e.message ? e.message : e)); setRes(null); }
    } finally {
      if (mine === seq.current) setBusy(false);
    }
  }
  return { busy, res, err, run };
}

export function ToolShell({ meta, onBack, children }) {
  return (
    <div className="main">
      <div className="toolbar">
        <IconBtn icon="arrow-left" title="All tools" onClick={onBack} />
        <Icon name={meta.icon} size={17} />
        <span className="title">{meta.title}</span>
        {meta.tool && <span className="sub">{meta.tool.summary}</span>}
      </div>
      <div className="scroll" style={{ padding: 20 }}>
        <div style={{ maxWidth: 880, margin: "0 auto", display: "flex", flexDirection: "column", gap: 14 }} className="fade">
          {children}
        </div>
      </div>
    </div>
  );
}

/* Target input + Run button — the entry row every fetching tool shares. */
export function TargetRow({ value, onChange, onRun, busy, placeholder, children }) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <input className="input mono" value={value} autoFocus placeholder={placeholder || "https://example.com"}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && !busy && onRun()}
        style={{ flex: 1, height: 36 }} />
      {children}
      <Btn variant="primary" icon={busy ? "loader-circle" : "play"} onClick={onRun} disabled={busy} style={{ height: 36 }}>
        {busy ? "Testing…" : "Test"}
      </Btn>
    </div>
  );
}

/* The findings strip: the same catalogue findings a crawl would store, with
   the same severity chips the issues view uses. */
export function FindingsStrip({ findings }) {
  if (!findings) return null;
  if (!findings.length) {
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12, color: "var(--sev-ok)", fontWeight: 600 }}>
        <Icon name="circle-check" size={15} />No findings
      </div>
    );
  }
  return (
    <div className="card" style={{ overflow: "hidden" }}>
      <div style={{ padding: "9px 14px", borderBottom: "1px solid var(--border-soft)", fontSize: 12, fontWeight: 650 }}>
        Findings <span style={{ color: "var(--ink-faint)", fontWeight: 500 }}>— what a crawl of this site would report</span>
      </div>
      {findings.map((f, i) => (
        <div key={i} style={{ padding: "9px 14px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "baseline", gap: 10 }}>
          <SevTag severity={f.severity} />
          <span style={{ fontSize: 12, fontWeight: 600 }}>{f.name}</span>
          {f.detail && <span className="mono" style={{ fontSize: 11, color: "var(--ink-3)", minWidth: 0, overflowWrap: "anywhere" }}>{f.detail}</span>}
        </div>
      ))}
    </div>
  );
}

export function ToolError({ err }) {
  if (!err) return null;
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--s-4xx)", fontSize: 12.5 }}>
      <Icon name="circle-alert" size={15} />{err}
    </div>
  );
}

const StatusPill = ({ status }) => (
  <span className="pill mono" style={{ color: statusVar(status) }}>{status || "—"}</span>
);

/* ---- sitemap ------------------------------------------------------------ */

function SitemapTool({ meta, onBack, prefill }) {
  const [target, setTarget] = useState(prefill || "");
  const { busy, res, err, run } = useToolRun();
  const rep = res && res.report;
  return (
    <ToolShell meta={meta} onBack={onBack}>
      <TargetRow value={target} onChange={setTarget} onRun={() => run("sitemap", target)} busy={busy}
        placeholder="https://example.com — or a sitemap.xml URL" />
      <ToolError err={err} />
      {rep && rep.missing && (
        <Empty icon="map-pin-off" title="No sitemap found">
          Nothing declared in robots.txt and nothing at /sitemap.xml or /sitemap_index.xml.
        </Empty>
      )}
      {rep && (rep.files || []).map((f) => (
        <div key={f.url} className="card" style={{ padding: "11px 14px", display: "flex", flexDirection: "column", gap: 7 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
            <StatusPill status={f.status} />
            <span className="mono" style={{ fontSize: 12, fontWeight: 600, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>{f.url}</span>
            {f.kind && <span className="pill">{f.kind}</span>}
            <span className="pill">via {f.source}</span>
          </div>
          <div style={{ display: "flex", gap: 14, fontSize: 11.5, color: "var(--ink-3)", flexWrap: "wrap" }}>
            {f.fetch_error ? <span style={{ color: "var(--s-4xx)" }}>{f.fetch_error}</span> : <>
              <span>{(f.entries || 0).toLocaleString()} entries</span>
              <span>{((f.size_bytes || 0) / 1024).toFixed(1)} KB{f.gzip ? " (gzipped)" : ""}</span>
              {f.xml_error && <span style={{ color: "var(--s-4xx)" }}>{f.xml_error}</span>}
              {f.duplicate_entries > 0 && <span>{f.duplicate_entries} duplicates</span>}
              {f.cross_host_urls > 0 && <span>{f.cross_host_urls} cross-host</span>}
              {f.invalid_lastmod > 0 && <span>{f.invalid_lastmod} invalid lastmod</span>}
              {(f.children || []).length > 0 && <span>{f.children.length} child sitemaps</span>}
            </>}
          </div>
          {(f.entry_checks || []).length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
              {f.entry_checks.map((ec, i) => (
                <div key={i} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 11 }} className="mono">
                  <StatusPill status={ec.status} />
                  <span style={{ color: "var(--ink-3)" }}>{ec.url}</span>
                  {ec.error && <span style={{ color: "var(--s-4xx)" }}>{ec.error}</span>}
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
      {rep && (rep.skipped || []).length > 0 && (
        <div className="hint">{rep.skipped.length} child sitemaps beyond the per-run expansion bound were not fetched.</div>
      )}
      {res && <FindingsStrip findings={res.findings} />}
    </ToolShell>
  );
}

/* ---- AI bots ------------------------------------------------------------ */

const PURPOSE = { training: "Training", search: "Search", user_action: "On demand" };

function AIBotsTool({ meta, onBack, prefill }) {
  const [target, setTarget] = useState(prefill || "");
  const [live, setLive] = useState(true);
  const { busy, res, err, run } = useToolRun();
  const rep = res && res.report;
  const bots = (rep && rep.bots) || [];
  const allowedN = bots.filter((b) => b.robots_allowed && !b.blocked_live).length;
  return (
    <ToolShell meta={meta} onBack={onBack}>
      <TargetRow value={target} onChange={setTarget} onRun={() => run("aibots", target, { live })} busy={busy}>
        <label style={{ display: "flex", alignItems: "center", gap: 7, fontSize: 11.5, color: "var(--ink-2)", whiteSpace: "nowrap" }}>
          <Toggle on={live} onChange={setLive} />Live probes
        </label>
      </TargetRow>
      <ToolError err={err} />
      {rep && (
        <>
          <div style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 12.5 }}>
            <span style={{ fontWeight: 650 }}>{allowedN} of {bots.length} AI crawlers can access {urlShort(rep.site)}</span>
            <div style={{ flex: 1 }} />
            {rep.live && <span className="hint">control fetch: HTTP {rep.control_status || "—"}{rep.control_error ? ` (${rep.control_error})` : ""}</span>}
          </div>
          <div className="card" style={{ overflow: "hidden" }}>
            {bots.map((b) => (
              <div key={b.name} style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "center", gap: 10 }}>
                <div style={{ width: 210, minWidth: 0 }}>
                  <div style={{ fontSize: 12.5, fontWeight: 600 }}>{b.name}</div>
                  <div style={{ fontSize: 10.5, color: "var(--ink-faint)" }}>{b.operator} · {PURPOSE[b.purpose] || b.purpose}</div>
                </div>
                <span className="badge tint" style={{ "--c": b.robots_allowed ? "var(--sev-ok)" : "var(--s-4xx)" }} title={b.robots_rule ? `line ${b.robots_line}: ${b.robots_rule}` : ""}>
                  <Icon name={b.robots_allowed ? "circle-check" : "ban"} size={11} />robots {b.robots_allowed ? "allowed" : "blocked"}
                </span>
                {!b.user_agent
                  ? <span className="pill" title="A robots.txt control token — no crawler sends this User-Agent">control token</span>
                  : b.probed && (b.live_error
                    ? <span className="badge tint" style={{ "--c": "var(--s-4xx)" }}>{b.live_error}</span>
                    : <span className="badge tint" style={{ "--c": b.blocked_live ? "var(--s-4xx)" : "var(--sev-ok)" }}>
                        <Icon name={b.blocked_live ? "shield-x" : "circle-check"} size={11} />live {b.live_status}
                      </span>)}
                <div style={{ flex: 1 }} />
                {!b.respects_robots && <span className="hint" title="Operator documents this fetcher as not honouring robots.txt — only an edge block is effective">ignores robots.txt</span>}
              </div>
            ))}
          </div>
          <div style={{ display: "flex", gap: 8, alignItems: "flex-start", fontSize: 11, color: "var(--ink-faint)", lineHeight: 1.5 }}>
            <Icon name="info" size={13} style={{ marginTop: 1, flex: "0 0 13px" }} />{rep.caveat}
          </div>
          <FindingsStrip findings={res.findings} />
        </>
      )}
    </ToolShell>
  );
}

/* ---- JS render diff ----------------------------------------------------- */

function RenderTool({ meta, onBack, prefill }) {
  const [target, setTarget] = useState(prefill || "");
  const { busy, res, err, run } = useToolRun();
  const rep = res && res.report;
  const row = (label, changed, raw, rendered) => (
    <div style={{ display: "grid", gridTemplateColumns: "130px 1fr 1fr", gap: 10, padding: "8px 14px", borderBottom: "1px solid var(--border-soft)", background: changed ? "color-mix(in oklab, var(--sev-warn) 7%, transparent)" : "transparent" }}>
      <span style={{ fontSize: 11.5, fontWeight: 600, color: changed ? "var(--sev-warn)" : "var(--ink-2)" }}>{label}</span>
      <span className="mono" style={{ fontSize: 11.5, minWidth: 0, overflowWrap: "anywhere" }}>{raw || <i style={{ color: "var(--ink-faint)" }}>none</i>}</span>
      <span className="mono" style={{ fontSize: 11.5, minWidth: 0, overflowWrap: "anywhere" }}>{rendered || <i style={{ color: "var(--ink-faint)" }}>none</i>}</span>
    </div>
  );
  return (
    <ToolShell meta={meta} onBack={onBack}>
      <TargetRow value={target} onChange={setTarget} onRun={() => run("render", target)} busy={busy}
        placeholder="https://example.com/page — rendering takes a few seconds" />
      <ToolError err={err} />
      {rep && rep.fetch_error && <ToolError err={rep.fetch_error} />}
      {rep && !rep.rendered && rep.render_error && <ToolError err={rep.render_error} />}
      {rep && rep.rendered && (
        <>
          <div className="card" style={{ overflow: "hidden" }}>
            <div style={{ display: "grid", gridTemplateColumns: "130px 1fr 1fr", gap: 10, padding: "9px 14px", borderBottom: "1px solid var(--border-soft)", fontSize: 11, fontWeight: 650, color: "var(--ink-3)" }}>
              <span /><span>Raw HTML (what text crawlers see)</span><span>Rendered (after JavaScript)</span>
            </div>
            {row("Words", rep.raw_word_count * 2 < rep.rendered_word_count, String(rep.raw_word_count), String(rep.rendered_word_count))}
            {row("Title", rep.title_changed, rep.raw_title, rep.rendered_title)}
            {row("Canonical", rep.canonical_changed, rep.raw_canonical, rep.rendered_canonical)}
            {row("Description", rep.description_changed, rep.description_changed ? "differs" : "same", rep.description_changed ? "differs" : "same")}
            {row("H1", rep.h1_changed, rep.h1_changed ? "differs" : "same", rep.h1_changed ? "differs" : "same")}
          </div>
          {rep.rendered_only_links > 0 && (
            <div className="card" style={{ padding: "11px 14px" }}>
              <div style={{ fontSize: 12, fontWeight: 650, marginBottom: 6 }}>{rep.rendered_only_links} links only exist after JavaScript runs</div>
              {(rep.rendered_only_link_examples || []).map((l, i) => <div key={i} className="mono" style={{ fontSize: 11, color: "var(--ink-3)" }}>{l}</div>)}
            </div>
          )}
          {(rep.console_errors || []).length > 0 && (
            <div className="card" style={{ padding: "11px 14px" }}>
              <div style={{ fontSize: 12, fontWeight: 650, marginBottom: 6, color: "var(--s-4xx)" }}>Console errors</div>
              {rep.console_errors.map((e, i) => <div key={i} className="mono" style={{ fontSize: 11, color: "var(--ink-3)", overflowWrap: "anywhere" }}>{e}</div>)}
            </div>
          )}
          <FindingsStrip findings={res.findings} />
        </>
      )}
    </ToolShell>
  );
}

/* ---- llms.txt ----------------------------------------------------------- */

function LlmsTool({ meta, onBack, prefill }) {
  const [target, setTarget] = useState(prefill || "");
  const { busy, res, err, run } = useToolRun();
  const rep = res && res.report;
  return (
    <ToolShell meta={meta} onBack={onBack}>
      <TargetRow value={target} onChange={setTarget} onRun={() => run("llms", target)} busy={busy} />
      <ToolError err={err} />
      {rep && (rep.files || []).map((f) => (
        <div key={f.url} className="card" style={{ padding: "11px 14px", display: "flex", alignItems: "center", gap: 10 }}>
          <StatusPill status={f.status} />
          <span className="mono" style={{ fontSize: 12, fontWeight: 600, flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f.url}</span>
          {f.found
            ? <span style={{ fontSize: 11.5, color: "var(--ink-3)" }}>{f.title ? `“${f.title}”` : "no H1 title"}</span>
            : <span style={{ fontSize: 11.5, color: "var(--ink-faint)" }}>not found</span>}
        </div>
      ))}
      {rep && (rep.links || []).length > 0 && (
        <div className="card" style={{ padding: "11px 14px" }}>
          <div style={{ fontSize: 12, fontWeight: 650, marginBottom: 6 }}>{rep.links.length} curated links</div>
          {rep.links.map((l, i) => (
            <div key={i} style={{ display: "flex", gap: 8, fontSize: 11.5, padding: "3px 0" }}>
              {l.section && <span className="pill">{l.section}</span>}
              <span className="mono" style={{ color: "var(--ink-3)", minWidth: 0, overflowWrap: "anywhere" }}>{l.url}</span>
            </div>
          ))}
        </div>
      )}
      {res && <FindingsStrip findings={res.findings} />}
    </ToolShell>
  );
}

/* ---- structured data ---------------------------------------------------- */

function StructuredTool({ meta, onBack, prefill }) {
  const [target, setTarget] = useState(prefill || "");
  const { busy, res, err, run } = useToolRun();
  const rep = res && res.report;
  const list = (label, items, color) => (items || []).length > 0 && (
    <div className="card" style={{ padding: "11px 14px" }}>
      <div style={{ fontSize: 12, fontWeight: 650, marginBottom: 6, color }}>{label}</div>
      {items.map((m, i) => <div key={i} className="mono" style={{ fontSize: 11.5, color: "var(--ink-3)", padding: "2px 0", overflowWrap: "anywhere" }}>{m}</div>)}
    </div>
  );
  return (
    <ToolShell meta={meta} onBack={onBack}>
      <TargetRow value={target} onChange={setTarget} onRun={() => run("structured", target)} busy={busy}
        placeholder="https://example.com/product-page" />
      <ToolError err={err} />
      {rep && rep.fetch_error && <ToolError err={rep.fetch_error} />}
      {rep && !rep.fetch_error && (
        <>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
            <StatusPill status={rep.fetch_status} />
            {(rep.formats || []).map((f) => <span key={f} className="pill">{f}</span>)}
            {(rep.types || []).map((t) => <span key={t} className="badge tint" style={{ "--c": "var(--accent)" }}>{t}</span>)}
            {!(rep.formats || []).length && <span style={{ fontSize: 12, color: "var(--ink-3)" }}>no structured data found</span>}
          </div>
          {list("Rich-result errors — required properties missing", rep.errors, "var(--sev-issue)")}
          {list("Warnings — recommended properties missing", rep.warnings, "var(--sev-warn)")}
          {list("Parse errors", rep.parse_errors, "var(--s-4xx)")}
          {list("Invalid JSON-LD recovered", rep.recovered, "var(--sev-warn)")}
          <FindingsStrip findings={res.findings} />
        </>
      )}
    </ToolShell>
  );
}

/* ---- SERP preview ------------------------------------------------------- */

function SerpTool({ meta, onBack, prefill }) {
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [target, setTarget] = useState(prefill || "");
  const { busy, res, err, run } = useToolRun();
  const timer = useRef(null);

  // Live editing: measuring is near-instant, so re-run debounced on keystrokes.
  useEffect(() => {
    if (!title && !desc) return undefined;
    clearTimeout(timer.current);
    timer.current = setTimeout(() => run("serp", "", { title, description: desc }), 200);
    return () => clearTimeout(timer.current);
  }, [title, desc]);

  async function fetchPage() {
    if (!target) return;
    await run("serp", target);
  }
  // After a URL fetch, adopt the page's actual texts into the editors so the
  // user continues editing from reality.
  useEffect(() => {
    const rep = res && res.report;
    if (rep && rep.fetched) {
      if (rep.title && rep.title.text && !title) setTitle(rep.title.text);
      if (rep.description && rep.description.text && !desc) setDesc(rep.description.text);
    }
  }, [res]);

  const rep = res && res.report;
  const bar = (f) => f && f.text && (
    <div style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 10.5, color: "var(--ink-faint)" }} className="mono">
      <div style={{ flex: 1, height: 4, background: "var(--surface-2)", position: "relative", overflow: "hidden" }}>
        <div style={{ position: "absolute", inset: 0, width: `${Math.min(100, (f.pixels / f.max_px) * 100)}%`, background: f.pixels > f.max_px ? "var(--sev-issue)" : f.pixels < f.min_px ? "var(--sev-warn)" : "var(--sev-ok)" }} />
      </div>
      <span>{f.pixels}px / {f.max_px}px · {f.chars} chars</span>
    </div>
  );
  return (
    <ToolShell meta={meta} onBack={onBack}>
      <TargetRow value={target} onChange={setTarget} onRun={fetchPage} busy={busy}
        placeholder="optional — fetch a live page's actual title & description" />
      <ToolError err={err} />
      {rep && rep.fetch_error && <ToolError err={rep.fetch_error} />}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 14 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <Field label="Title" hint="Google truncates at the pixel limit, not a character count.">
            <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Page title" />
          </Field>
          {rep && bar(rep.title)}
          <Field label="Meta description">
            <textarea className="input" value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Meta description" style={{ height: 84, padding: 10, resize: "vertical", lineHeight: 1.5 }} />
          </Field>
          {rep && bar(rep.description)}
        </div>
        {/* the snippet mock — Google desktop result styling, truncation applied */}
        <div className="card" style={{ padding: "16px 18px", alignSelf: "start" }}>
          <div style={{ fontSize: 11, color: "var(--ink-3)", marginBottom: 3 }} className="mono">{urlShort(target) || "example.com"}</div>
          <div style={{ fontSize: 17, lineHeight: 1.3, color: "var(--serp-link)", marginBottom: 4, fontFamily: "arial, sans-serif" }}>
            {(rep && rep.title && (rep.title.truncated || rep.title.text)) || <span style={{ color: "var(--ink-faint)" }}>Your title appears here</span>}
          </div>
          <div style={{ fontSize: 13, lineHeight: 1.45, color: "var(--ink-2)", fontFamily: "arial, sans-serif" }}>
            {(rep && rep.description && (rep.description.truncated || rep.description.text)) || <span style={{ color: "var(--ink-faint)" }}>Your meta description appears here.</span>}
          </div>
        </div>
      </div>
      {res && <FindingsStrip findings={res.findings} />}
    </ToolShell>
  );
}
