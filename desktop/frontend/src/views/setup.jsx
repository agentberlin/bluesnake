/* ===========================================================================
   Crawl Setup — the exact configuration a crawl was frozen with, read-only.

   Every crawl freezes its full effective config into its own database at start
   (store.CreateCrawl), so this is a permanent, per-crawl record — available
   while the crawl is still running as much as after it finishes. It renders
   from the same schema as the editable Settings view (config-schema.js), so the
   two stay visually identical.

   Actions close the loop on a frozen config you can see but not edit:
     • Copy as YAML       — the exact frozen config.
     • Save as profile    — reuse these settings on ANY other site via New Crawl.
     • Re-run             — crawl THIS site again with the identical config.
   =========================================================================== */
import React, { useEffect, useMemo, useState } from "react";
import { Icon, Btn, Search, Toggle, Empty, Modal, Toast, BrandMark } from "../ui";
import { api, urlShort, hostOf, copyToClipboard } from "../api";
import { SECTIONS, ALL_FIELDS, getPath } from "./config-schema";

/* normalized equality for config values (arrays compared by content) */
function sameVal(a, b) {
  if (Array.isArray(a) || Array.isArray(b)) return JSON.stringify(a || []) === JSON.stringify(b || []);
  return a === b;
}

/* human-readable rendering of a single field value (mirrors the field types in
   config-schema.js, but display-only) */
function fmtValue(f, val) {
  if (f.type === "toggle") return val ? "Yes" : "No";
  if (f.type === "number") {
    if (val === -1) return "Unlimited";
    if (f.unit === "URL/s" && val === 0) return "Unlimited";
    return String(val ?? "—") + (f.unit ? " " + f.unit : "");
  }
  if (f.type === "list") return Array.isArray(val) && val.length ? val : "None";
  // text / choice
  return val === "" || val == null ? "—" : String(val);
}

/* the read-only value cell — chips for lists, a Yes/No pill for toggles, mono
   text otherwise */
function ReadOnlyValue({ f, val }) {
  const out = fmtValue(f, val);
  if (f.type === "toggle") {
    const on = !!val;
    return <span className="pill" style={{ height: 22, fontSize: 11, fontWeight: 600, color: on ? "var(--sev-ok)" : "var(--ink-faint)", borderColor: on ? "color-mix(in oklab, var(--sev-ok) 35%, var(--border))" : "var(--border)" }}>
      <span className="statusdot" style={{ background: on ? "var(--sev-ok)" : "var(--ink-faint)" }} />{out}
    </span>;
  }
  if (Array.isArray(out)) {
    const regex = /regex|pattern/i.test(f.label);
    return <span style={{ display: "inline-flex", flexWrap: "wrap", gap: 5, justifyContent: "flex-end" }}>
      {out.map((it, i) => <span key={i} className="pill" style={{ height: 22, fontSize: 11, fontFamily: regex ? "var(--font-mono)" : "inherit", color: "var(--ink-2)" }}>{it}</span>)}
    </span>;
  }
  const muted = out === "—" || out === "None";
  return <span className="mono" style={{ fontSize: 12, fontWeight: 600, color: muted ? "var(--ink-faint)" : "var(--ink)", textAlign: "right" }}>{out}</span>;
}

/* one field row: label + hint + dotted key on the left, frozen value on the
   right, with a subtle accent dot + default hint when it differs from the
   engine default */
function SetupField({ f, val, def, showSection }) {
  const changed = !sameVal(val, def);
  return (
    <div style={{ display: "flex", gap: 16, padding: "12px 0", borderBottom: "1px solid var(--border-soft)", alignItems: "flex-start" }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontSize: 12.5, fontWeight: 600 }}>{f.label}</span>
          {showSection && <span style={{ fontSize: 10.5, color: "var(--ink-faint)" }}>· {f.section}</span>}
          {changed && <span title="Changed from the default" className="statusdot" style={{ background: "var(--accent)" }} />}
        </div>
        {f.hint && <div className="hint" style={{ marginTop: 4 }}>{f.hint}</div>}
        <div className="hint mono" style={{ marginTop: 3, fontSize: 10 }}>{f.key}</div>
      </div>
      <div style={{ flex: "0 0 auto", maxWidth: 300, display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 3 }}>
        <ReadOnlyValue f={f} val={val} />
        {changed && <span className="hint" style={{ fontSize: 10 }}>default {fmtScalar(f, def)}</span>}
      </div>
    </div>
  );
}

/* compact scalar form of a default value for the "default X" hint */
function fmtScalar(f, def) {
  const out = fmtValue(f, def);
  if (Array.isArray(out)) return out.length ? out.join(", ") : "None";
  return out;
}

export function CrawlSetup({ crawl, onRerun, crawlBusyMsg }) {
  const [info, setInfo] = useState(null);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [toast, setToast] = useState(null);
  const fireToast = (msg, icon = "check") => { setToast({ msg, icon }); setTimeout(() => setToast(null), 2800); };

  useEffect(() => {
    setInfo(null);
    setError("");
    api.crawlConfig(crawl.id).then(setInfo).catch((e) => setError(String(e)));
  }, [crawl.id]);

  const cfg = useMemo(() => { try { return info ? JSON.parse(info.configJson) : null; } catch { return null; } }, [info]);
  const def = useMemo(() => { try { return info ? JSON.parse(info.defaultsJson) : null; } catch { return null; } }, [info]);

  const changedFields = useMemo(() => {
    if (!cfg || !def) return [];
    return ALL_FIELDS.filter((f) => !sameVal(getPath(cfg, f.key), getPath(def, f.key)));
  }, [cfg, def]);

  const searchHits = q ? ALL_FIELDS.filter((f) => f.label.toLowerCase().includes(q.toLowerCase())) : null;

  async function copyYaml() {
    if (!info) return;
    const ok = await copyToClipboard(info.yaml);
    fireToast(ok ? "Copied the crawl's config as YAML" : "Copy failed", ok ? "copy" : "circle-alert");
  }
  async function saveProfile() {
    const name = saveName.trim();
    if (!name) return;
    try {
      const saved = await api.saveCrawlConfigAsProfile(crawl.id, name);
      setSaving(false);
      setSaveName("");
      fireToast(`Saved profile “${saved}” — pick it in New Crawl for any site`, "save");
    } catch (e) {
      fireToast(String(e && e.message ? e.message : e), "circle-alert");
    }
  }

  const host = hostOf(crawl.seed || (info && info.seeds && info.seeds[0]) || "");
  const mode = (info && info.mode) || crawl.mode || "spider";
  const seeds = (info && info.seeds) || (crawl.seed ? [crawl.seed] : []);
  const isList = mode === "list";

  return (
    <div className="main">
      <div className="toolbar">
        <Icon name="settings-2" size={16} style={{ color: "var(--ink-3)" }} />
        <span className="title" style={{ fontSize: 13.5 }}>Setup</span>
        <span className="mono sub">{host}</span>
        <div style={{ flex: 1 }} />
        <Btn icon="copy" onClick={copyYaml} disabled={!info}>Copy YAML</Btn>
        <Btn icon="save" onClick={() => { setSaveName(host ? host.replace(/^www\./, "") + " settings" : "New profile"); setSaving(true); }} disabled={!info}>Save as profile</Btn>
        <Btn icon="rotate-cw" variant="primary" onClick={onRerun} title={crawlBusyMsg || "Crawl this site again with the identical configuration"}>Re-run</Btn>
      </div>

      <div className="scroll" style={{ padding: 20 }}>
        <div style={{ maxWidth: 760, margin: "0 auto" }} className="fade">
          {error && <Empty icon="circle-alert" title="Couldn't load the crawl's settings">{error}</Empty>}
          {!info && !error && <div style={{ padding: 40, textAlign: "center", color: "var(--ink-faint)", fontSize: 12.5 }}>
            <Icon name="loader" size={16} style={{ animation: "spin 1s linear infinite" }} /> Loading configuration…
          </div>}

          {cfg && (
            <>
              {/* identity + headline knobs */}
              <div className="card" style={{ padding: 0, overflow: "hidden", marginBottom: 16 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 13, padding: "15px 18px", borderBottom: "1px solid var(--border-soft)" }}>
                  <BrandMark seed={seeds[0] || crawl.seed} size={36} />
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                      <span className="mono" style={{ fontSize: 13, fontWeight: 650, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
                        {isList ? `${seeds.length} URL${seeds.length === 1 ? "" : "s"}` : urlShort(seeds[0] || crawl.seed)}
                      </span>
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 4, fontSize: 10.5, fontWeight: 600, color: "var(--ink-3)", padding: "1px 6px", background: "var(--surface-2)", border: "1px solid var(--border)" }}>
                        <Icon name={isList ? "list" : "radar"} size={11} />{isList ? "List" : "Spider"}
                      </span>
                    </div>
                    <div className="hint" style={{ marginTop: 3 }}>
                      {crawl.started ? crawl.started + " · " : ""}The exact configuration frozen into this crawl — it can't drift.
                    </div>
                  </div>
                </div>

                <div style={{ display: "grid", gridTemplateColumns: "repeat(3,1fr)", gap: 1, background: "var(--border-soft)" }}>
                  <Tile label="Max crawl depth" value={numVal(getPath(cfg, "limits.max_depth"), { unlimited: -1 })} />
                  <Tile label="Threads" value={String(getPath(cfg, "speed.max_threads") ?? "—")} />
                  <Tile label="Rate limit" value={numVal(getPath(cfg, "speed.max_urls_per_sec"), { unlimited: 0, unit: "/s" })} />
                  <Tile label="Rendering" value={getPath(cfg, "rendering.mode") === "javascript" ? "JavaScript" : "Text only"} />
                  <Tile label="robots.txt" value={getPath(cfg, "robots.mode") || "—"} />
                  <Tile label="Max URLs" value={numVal(getPath(cfg, "limits.max_urls"), { unlimited: 0 })} />
                </div>
              </div>

              {isList && seeds.length > 0 && <SeedList seeds={seeds} />}

              {/* what differs from the engine defaults */}
              <div className="card" style={{ overflow: "hidden", marginBottom: 16 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "12px 16px", borderBottom: changedFields.length ? "1px solid var(--border-soft)" : "none" }}>
                  <Icon name="git-compare" size={15} style={{ color: "var(--ink-3)" }} />
                  <span style={{ fontSize: 12.5, fontWeight: 650 }}>Customised for this crawl</span>
                  {changedFields.length > 0 && <span className="pill mono" style={{ height: 18, fontSize: 10.5 }}>{changedFields.length}</span>}
                </div>
                {changedFields.length === 0 ? (
                  <div style={{ padding: "16px", fontSize: 12.5, color: "var(--ink-2)", display: "flex", alignItems: "center", gap: 8 }}>
                    <Icon name="circle-check" size={15} style={{ color: "var(--sev-ok)" }} />
                    Ran with the standard defaults — nothing was overridden.
                  </div>
                ) : (
                  <div style={{ padding: "2px 16px 8px" }}>
                    {changedFields.map((f) => <SetupField key={f.key} f={f} val={getPath(cfg, f.key)} def={getPath(def, f.key)} showSection />)}
                  </div>
                )}
              </div>

              {/* full frozen configuration, same taxonomy as Settings */}
              <div style={{ display: "flex", alignItems: "center", gap: 10, margin: "22px 2px 6px" }}>
                <span style={{ fontSize: 12.5, fontWeight: 650 }}>Full configuration</span>
                <div style={{ flex: 1 }} />
                <Search value={q} onChange={setQ} placeholder="Search settings…" width={200} />
                <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12, color: "var(--ink-2)" }}>
                  <Toggle on={advanced} onChange={setAdvanced} /> Advanced
                </label>
              </div>

              <div className="card" style={{ padding: "4px 16px 10px" }}>
                {searchHits ? (
                  searchHits.length === 0
                    ? <div style={{ padding: 20 }}><Empty icon="search-x" title="No settings match">Nothing matches “{q}”.</Empty></div>
                    : searchHits.map((f) => <SetupField key={f.key} f={f} val={getPath(cfg, f.key)} def={getPath(def, f.key)} showSection />)
                ) : (
                  SECTIONS.map((s) => {
                    const fields = s.fields.filter((f) => advanced || !f.advanced);
                    if (!fields.length) return null;
                    return (
                      <div key={s.id}>
                        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "16px 0 6px" }}>
                          <Icon name={s.icon} size={15} style={{ color: "var(--ink-3)" }} />
                          <span style={{ fontSize: 12.5, fontWeight: 650 }}>{s.label}</span>
                        </div>
                        {fields.map((f) => <SetupField key={f.key} f={f} val={getPath(cfg, f.key)} def={getPath(def, f.key)} />)}
                      </div>
                    );
                  })
                )}
                {!advanced && !searchHits && (
                  <button onClick={() => setAdvanced(true)} className="btn ghost" style={{ marginTop: 10, color: "var(--ink-faint)" }}>
                    <Icon name="chevron-down" size={14} />Show advanced settings
                  </button>
                )}
              </div>

              <div style={{ marginTop: 14, display: "flex", alignItems: "center", gap: 8, fontSize: 11.5, color: "var(--ink-faint)" }}>
                <Icon name="git-compare" size={14} />
                <span>Changed a threshold since? <b style={{ color: "var(--ink)" }}>Re-analyse</b> from any dataset recomputes issues with new thresholds — no recrawl. To change how the site is crawled, <b style={{ color: "var(--ink)" }}>Save as profile</b> and start a new crawl.</span>
              </div>
            </>
          )}
        </div>
      </div>

      {saving && (
        <Modal onClose={() => setSaving(false)} icon="save" title="Save these settings as a profile"
          body={<div style={{ marginTop: 4 }}>
            <div className="hint" style={{ marginBottom: 8 }}>Creates a reusable profile from this crawl's exact configuration. Pick it in New Crawl to run any site with these settings.</div>
            <input className="input" value={saveName} autoFocus onChange={(e) => setSaveName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && saveProfile()} placeholder="Profile name" />
          </div>}
          actions={<>
            <Btn onClick={() => setSaving(false)}>Cancel</Btn>
            <Btn variant="primary" icon="save" disabled={!saveName.trim()} onClick={saveProfile}>Save profile</Btn>
          </>} />
      )}
      {toast && <Toast {...toast} />}
    </div>
  );
}

/* headline stat tile — hairline separators come from the parent grid's gap */
function Tile({ label, value }) {
  return (
    <div style={{ padding: "14px 18px", background: "var(--surface)" }}>
      <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em" }}>{label}</div>
      <div className="mono" style={{ fontSize: 17, fontWeight: 600, marginTop: 5, letterSpacing: "-.01em" }}>{value}</div>
    </div>
  );
}

/* number with a sentinel meaning "unlimited" (−1 for depth, 0 for rate/urls) */
function numVal(v, { unlimited, unit } = {}) {
  if (v == null) return "—";
  if (unlimited != null && v === unlimited) return "Unlimited";
  return String(v) + (unit || "");
}

/* the seed URL list for list-mode crawls (collapsed past a handful) */
function SeedList({ seeds }) {
  const [open, setOpen] = useState(false);
  const shown = open ? seeds : seeds.slice(0, 6);
  return (
    <div className="card" style={{ padding: "12px 16px", marginBottom: 16 }}>
      <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 8 }}>Seed URLs</div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        {shown.map((u, i) => <span key={i} className="mono" style={{ fontSize: 11.5, color: "var(--ink-2)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{urlShort(u)}</span>)}
      </div>
      {seeds.length > 6 && (
        <button className="btn ghost" style={{ marginTop: 8, color: "var(--ink-faint)" }} onClick={() => setOpen((v) => !v)}>
          <Icon name={open ? "chevron-up" : "chevron-down"} size={14} />{open ? "Show fewer" : `Show all ${seeds.length}`}
        </button>
      )}
    </div>
  );
}
