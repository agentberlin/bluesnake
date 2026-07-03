/* ===========================================================================
   New Crawl — URL entry, mode, crawl setup (profile + quick config, politeness)
   =========================================================================== */
import React, { useEffect, useState } from "react";
import { Icon, Btn, Seg, Toggle } from "../ui";
import { api, DEFAULT_PROFILE, profileLabel } from "../api";

/* The crawl setup — the profile picker plus the quick knobs. One object so the
   whole card is reusable: New Crawl renders it inline; the project "Crawl all"
   dialog reuses it, keeping both journeys identical. */
export function defaultCrawlSetup() {
  // ups matches the 5-thread default so the rate cap isn't the bottleneck; 0 = unlimited
  return { profile: DEFAULT_PROFILE, depth: "", threads: 5, ups: 5, rendering: "text" };
}

/* Map the setup card's state to the backend StartRequest knobs. */
export function setupToRequest(s) {
  return {
    profile: s.profile,
    threads: s.threads,
    rate: s.ups,
    maxDepth: s.depth === "" ? -1 : Math.max(0, parseInt(s.depth, 10) || 0),
    rendering: s.rendering,
  };
}

export function NewCrawl({ onStart, onOpenSettings, crawlBusyMsg, onViewActiveCrawl, initialUrl }) {
  const [mode, setMode] = useState("spider");
  const [url, setUrl] = useState(initialUrl || "https://");
  const [listSrc, setListSrc] = useState("paste");
  const [listText, setListText] = useState("");
  const [sitemapUrl, setSitemapUrl] = useState("");
  const [profiles, setProfiles] = useState([DEFAULT_PROFILE]);
  const [setup, setSetup] = useState(defaultCrawlSetup());
  const [err, setErr] = useState("");
  const [starting, setStarting] = useState(false);

  useEffect(() => {
    api.listProfiles().then((p) => { if (p && p.length) setProfiles(p); }).catch(() => {});
  }, []);

  const listCount = listText.trim().split("\n").filter(Boolean).length;
  const valid = mode === "spider"
    ? /^https?:\/\/.+\..+/.test(url.trim())
    : (listSrc === "sitemap" ? /^https?:\/\//.test(sitemapUrl.trim()) : listCount > 0);

  async function start() {
    // Starting while a crawl is running no longer blocks — the job is enqueued
    // and the dispatcher runs it next (crawlBusyMsg only drives the info banner).
    if (!valid || starting) {
      setErr(mode === "spider" ? "Enter a valid URL including http:// or https://" : "Add at least one URL to audit.");
      return;
    }
    setErr("");
    setStarting(true);
    try {
      await onStart({
        mode,
        url: url.trim(),
        listUrls: mode === "list" && listSrc !== "sitemap" ? listText.trim().split("\n").map((s) => s.trim()).filter(Boolean) : [],
        sitemapUrl: mode === "list" && listSrc === "sitemap" ? sitemapUrl.trim() : "",
        ...setupToRequest(setup),
      });
    } catch (e) {
      setErr(String(e && e.message ? e.message : e));
    } finally {
      setStarting(false);
    }
  }

  return (
    <div className="main">
      <div className="toolbar">
        <Icon name="radar" size={17} />
        <span className="title">New Crawl</span>
        <div style={{ flex: 1 }} />
        <Btn icon="sliders-horizontal" onClick={() => onOpenSettings(setup.profile)}>All settings</Btn>
      </div>

      <div className="scroll" style={{ padding: "40px 24px" }}>
        <div style={{ maxWidth: 660, margin: "0 auto" }} className="fade">

          {/* Only one crawl runs at a time — if one starts while this form is open
              (e.g. over MCP), block the start and point back at the live crawl. */}
          {crawlBusyMsg && (
            <div className="card" style={{ marginBottom: 22, padding: "12px 14px", display: "flex", alignItems: "center", gap: 11, borderColor: "color-mix(in oklab, var(--sev-warn) 40%, var(--border))", background: "color-mix(in oklab, var(--sev-warn) 6%, var(--surface))" }}>
              <Icon name="circle-pause" size={17} style={{ color: "var(--sev-warn)", flex: "0 0 17px" }} />
              <span style={{ fontSize: 12.5, color: "var(--ink-2)", flex: 1 }}>{crawlBusyMsg}</span>
              {onViewActiveCrawl && <Btn size="sm" icon="arrow-right" onClick={onViewActiveCrawl}>View running crawl</Btn>}
            </div>
          )}

          {/* mode */}
          <div style={{ display: "flex", justifyContent: "center", marginBottom: 26 }}>
            <Seg value={mode} onChange={setMode} options={[{ value: "spider", label: "Spider — discover by following links" }, { value: "list", label: "List — audit exact URLs" }]} />
          </div>

          {/* URL / list source */}
          {mode === "spider" ? (
            <div>
              <div style={{ position: "relative" }}>
                <Icon name="globe" size={18} style={{ position: "absolute", left: 16, top: 17, color: "var(--ink-faint)" }} />
                <input className="input mono" value={url} autoFocus
                  onChange={(e) => { setUrl(e.target.value); setErr(""); }}
                  onKeyDown={(e) => e.key === "Enter" && start()}
                  placeholder="https://example.com"
                  style={{ height: 52, fontSize: 15, paddingLeft: 46, paddingRight: 130, boxShadow: "var(--shadow-sm)" }} />
                <div style={{ position: "absolute", right: 8, top: 8 }}>
                  <Btn variant="primary" icon="play" onClick={start} disabled={starting} style={{ height: 36, fontSize: 13.5, padding: "0 16px", opacity: valid ? 1 : 0.55 }}>
                    {starting ? "Starting…" : (crawlBusyMsg ? "Add to queue" : "Start crawl")}
                  </Btn>
                </div>
              </div>
              <div className="hint" style={{ marginTop: 9, textAlign: "center" }}>
                The spider downloads this page, follows every link it finds, and keeps going until the whole site is mapped.
              </div>
            </div>
          ) : (
            <div className="card" style={{ padding: 16 }}>
              <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
                <Seg value={listSrc} onChange={setListSrc} options={[{ value: "paste", label: "Paste" }, { value: "sitemap", label: "Sitemap URL" }]} />
                <div style={{ flex: 1 }} />
                {listSrc === "paste" && <span className="pill mono">{listCount} URLs</span>}
                <Btn variant="primary" icon="play" onClick={start} size="sm" disabled={starting}>{starting ? "Starting…" : (crawlBusyMsg ? "Add to queue" : "Start audit")}</Btn>
              </div>
              {listSrc === "paste" && <textarea className="input mono" value={listText} placeholder={"https://example.com/\nhttps://example.com/about"} onChange={(e) => { setListText(e.target.value); setErr(""); }} style={{ height: 150, padding: 12, resize: "vertical", lineHeight: 1.6 }} />}
              {listSrc === "sitemap" && <input className="input mono" placeholder="https://example.com/sitemap.xml" value={sitemapUrl} onChange={(e) => { setSitemapUrl(e.target.value); setErr(""); }} />}
            </div>
          )}

          {err && <div style={{ marginTop: 12, display: "flex", alignItems: "center", gap: 8, color: "var(--s-4xx)", fontSize: 12.5, fontWeight: 500 }}><Icon name="circle-alert" size={15} />{err}</div>}

          {/* quick config — the shared setup card */}
          <CrawlSetupCard profiles={profiles} value={setup} onChange={setSetup} style={{ marginTop: 24 }} />

          <div style={{ marginTop: 18, display: "flex", alignItems: "center", justifyContent: "center", gap: 16, fontSize: 11.5, color: "var(--ink-faint)" }}>
            <span style={{ display: "flex", alignItems: "center", gap: 6 }}><Icon name="bot" size={13} /> Obeys robots.txt</span>
            <span style={{ display: "flex", alignItems: "center", gap: 6 }}><Icon name="git-compare" size={13} /> Auto-analyses on finish</span>
            <span style={{ display: "flex", alignItems: "center", gap: 6 }}><Icon name="save" size={13} /> Auto-saves continuously</span>
          </div>

        </div>
      </div>
    </div>
  );
}

/* The shared crawl-setup card: profile picker + quick knobs + politeness.
   Controlled — the caller owns the setup object (see defaultCrawlSetup). */
export function CrawlSetupCard({ profiles, value, onChange, style, hint }) {
  const set = (patch) => onChange({ ...value, ...patch });
  return (
    <div className="card" style={{ padding: 0, overflow: "hidden", ...style }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "12px 16px", borderBottom: "1px solid var(--border-soft)" }}>
        <Icon name="settings-2" size={15} style={{ color: "var(--ink-3)" }} />
        <span style={{ fontSize: 12.5, fontWeight: 650 }}>Crawl setup</span>
        <span className="hint" style={{ marginLeft: 2 }}>{hint || "frozen into this crawl"}</span>
        <div style={{ flex: 1 }} />
        <select className="input" value={value.profile} onChange={(e) => set({ profile: e.target.value })} style={{ width: "auto", height: 28, fontSize: 12, fontWeight: 600 }}>
          {profiles.map((p) => <option key={p} value={p}>{profileLabel(p)}</option>)}
        </select>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 0 }}>
        <Setup label="Max crawl depth" hint="Clicks from start · blank = unlimited">
          <input className="input mono" value={value.depth} placeholder="∞ unlimited" onChange={(e) => set({ depth: e.target.value.replace(/\D/g, "") })} style={{ height: 28 }} />
        </Setup>
        <Setup label="Rendering">
          <Seg value={value.rendering} onChange={(v) => set({ rendering: v })} options={[{ value: "text", label: "Text only" }, { value: "javascript", label: "JavaScript" }]} />
          {value.rendering === "javascript" && (
            <div style={{ marginTop: 8, display: "flex", gap: 7, alignItems: "flex-start", fontSize: 11, color: "var(--sev-warn)", lineHeight: 1.45 }}>
              <Icon name="triangle-alert" size={13} style={{ marginTop: 1, flex: "0 0 13px" }} />
              <span>JavaScript rendering loads each page in headless Chrome — slower, and Chrome/Chromium must be installed.</span>
            </div>
          )}
        </Setup>
        <Setup label="Threads" hint="Parallel downloads">
          <Stepper value={value.threads} min={1} max={50} onChange={(v) => set({ threads: v })} />
        </Setup>
      </div>
      {/* politeness — surfaced, not buried */}
      <div style={{ padding: "13px 16px", borderTop: "1px solid var(--border-soft)", background: "var(--surface-2)", display: "flex", alignItems: "center", gap: 13 }}>
        <Icon name="heart-handshake" size={17} style={{ color: "var(--ink-3)", flex: "0 0 17px" }} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 12, fontWeight: 600 }}>Be polite — {value.ups === 0 ? "unlimited rate" : value.ups + " URLs / second"}</div>
          <div className="hint">Crawling uses someone else's server. The throttle is courtesy as much as performance.</div>
        </div>
        <input type="range" min={0} max={20} value={value.ups} onChange={(e) => set({ ups: +e.target.value })} style={{ width: 150, accentColor: "var(--accent)" }} />
        <span className="mono" style={{ fontSize: 12, width: 56, textAlign: "right", color: "var(--ink-2)" }}>{value.ups === 0 ? "max" : value.ups + "/s"}</span>
      </div>
    </div>
  );
}

export function Setup({ label, hint, children }) {
  return (
    <div style={{ padding: "13px 16px", borderRight: "1px solid var(--border-soft)", borderBottom: "1px solid var(--border-soft)" }}>
      <div style={{ fontSize: 11.5, fontWeight: 600, color: "var(--ink-2)", marginBottom: 7 }}>{label}</div>
      {children}
      {hint && <div className="hint" style={{ marginTop: 6 }}>{hint}</div>}
    </div>
  );
}

export function Stepper({ value, min = 0, max = 999, onChange, unit }) {
  return (
    <div style={{ display: "inline-flex", alignItems: "center", border: "1px solid var(--border-strong)", overflow: "hidden", height: 28, background: "var(--surface)" }}>
      <button className="iconbtn" style={{ width: 28, height: 26, borderRadius: 0 }} onClick={() => onChange(Math.max(min, value - 1))}><Icon name="minus" size={14} /></button>
      <span className="mono" style={{ width: 40, textAlign: "center", fontSize: 12.5, fontWeight: 600, borderLeft: "1px solid var(--border-soft)", borderRight: "1px solid var(--border-soft)", lineHeight: "26px" }}>{value}{unit || ""}</span>
      <button className="iconbtn" style={{ width: 28, height: 26, borderRadius: 0 }} onClick={() => onChange(Math.min(max, value + 1))}><Icon name="plus" size={14} /></button>
    </div>
  );
}
