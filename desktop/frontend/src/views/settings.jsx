/* ===========================================================================
   Settings & Profiles — curated tree bound to the real config schema
   (dotted yaml-tag keys), search, simple/advanced, raw YAML editor.
   =========================================================================== */
import React, { useEffect, useMemo, useState } from "react";
import { Icon, Btn, IconBtn, Search, Toggle, Seg, Empty, Toast, Modal } from "../ui";
import { api } from "../api";

/* schema: every key is a verified dotted yaml path in internal/config */
const tg = (key, label, hint, adv) => ({ key, label, type: "toggle", hint, advanced: adv });
const num = (key, label, hint, unit, adv) => ({ key, label, type: "number", hint, unit, advanced: adv });
const ch = (key, label, options, hint, adv) => ({ key, label, type: "choice", options, hint, advanced: adv });
const txt = (key, label, hint, adv) => ({ key, label, type: "text", hint, advanced: adv });
const lst = (key, label, hint, adv) => ({ key, label, type: "list", hint, advanced: adv });

const SECTIONS = [
  { id: "scope", label: "Crawl Scope", icon: "crosshair", fields: [
    tg("scope.crawl_all_subdomains", "Crawl all subdomains", "Treat blog.site.com, shop.site.com as part of the site."),
    tg("scope.crawl_outside_start_folder", "Crawl outside start folder", "Lift the /blog/ restriction when starting in a subfolder."),
    tg("scope.check_links_outside_start_folder", "Check links outside start folder", "Verify out-of-folder pages work without exploring them."),
    tg("scope.follow_internal_nofollow", "Follow internal nofollow links", null, true),
    tg("scope.follow_external_nofollow", "Follow external nofollow links", null, true),
    tg("scope.crawl_invalid_links", "Crawl invalid links", "Attempt malformed links and report them as errors.", true),
    lst("scope.cdns", "CDN domains", "Extra domains to treat as part of the site (asset CDNs).", true),
    lst("scope.include", "Include patterns (regex)", "Only URLs matching at least one are crawled."),
    lst("scope.exclude", "Exclude patterns (regex)", "URLs matching any are never requested. Exclude beats include."),
  ]},
  { id: "extraction", label: "Extraction", icon: "scan-line", fields: [
    tg("extraction.page_details.titles", "Page titles"),
    tg("extraction.page_details.meta_descriptions", "Meta descriptions"),
    tg("extraction.page_details.meta_keywords", "Meta keywords"),
    tg("extraction.page_details.h1", "H1"),
    tg("extraction.page_details.h2", "H2"),
    tg("extraction.page_details.indexability", "Indexability"),
    tg("extraction.page_details.word_count", "Word count"),
    tg("extraction.page_details.readability", "Readability"),
    tg("extraction.page_details.text_to_code_ratio", "Text-to-code ratio"),
    tg("extraction.page_details.hash", "Page hash (exact-duplicate detection)"),
    tg("extraction.page_details.page_size", "Page size"),
    tg("extraction.page_details.forms", "Forms"),
    tg("extraction.url_details.response_time", "Response time"),
    tg("extraction.url_details.last_modified", "Last-modified"),
    tg("extraction.url_details.http_headers", "HTTP headers"),
    tg("extraction.url_details.cookies", "Cookies", null, true),
    tg("extraction.directives.meta_robots", "Meta robots"),
    tg("extraction.directives.x_robots_tag", "X-Robots-Tag"),
    tg("extraction.structured_data.jsonld", "JSON-LD", "Structured data master toggle."),
    tg("extraction.structured_data.microdata", "Microdata", null, true),
    tg("extraction.structured_data.rdfa", "RDFa", null, true),
    tg("extraction.store_html", "Store raw HTML", "Saves every page's source to disk for later viewing.", true),
  ]},
  { id: "limits", label: "Limits", icon: "gauge", fields: [
    num("limits.max_urls", "Max URLs to crawl", "Hard stop for the whole crawl."),
    num("limits.max_depth", "Max crawl depth", "Clicks from the start URL. −1 = unlimited."),
    num("limits.max_urls_per_depth", "Max URLs per depth level", null, null, true),
    num("limits.max_folder_depth", "Max folder depth", null, null, true),
    num("limits.max_query_strings", "Max query-string parameters", null, null, true),
    num("limits.max_redirects", "Max redirects to follow"),
    num("limits.max_url_length", "Max URL length", null, "chars", true),
    num("limits.max_links_per_page", "Max links per page", null, null, true),
    num("limits.max_page_size_kb", "Max page size", "Bigger downloads are abandoned.", "KB"),
  ]},
  { id: "rendering", label: "Rendering (JavaScript)", icon: "chrome", fields: [
    ch("rendering.mode", "Rendering mode", ["text", "javascript"], "JavaScript mode loads each page in headless Chrome. Requires Chrome installed."),
    num("rendering.ajax_timeout_sec", "AJAX timeout", "How long scripts run before snapshot.", "s"),
    tg("rendering.screenshots", "Capture screenshots", null, true),
    tg("rendering.js_error_reporting", "Report JavaScript console errors", null, true),
    txt("rendering.chrome_path", "Chrome path", "Manual override when Chrome isn't found.", true),
  ]},
  { id: "thresholds", label: "Thresholds", icon: "sliders-horizontal", fields: [
    num("thresholds.title.min_chars", "Page title min length", null, "chars"),
    num("thresholds.title.max_chars", "Page title max length", null, "chars"),
    num("thresholds.description.min_chars", "Meta description min length", null, "chars"),
    num("thresholds.description.max_chars", "Meta description max length", null, "chars"),
    num("thresholds.url_max_chars", "Max URL length flag", null, "chars"),
    num("thresholds.h1_max_chars", "Max H1 length", null, "chars"),
    num("thresholds.h2_max_chars", "Max H2 length", null, "chars", true),
    num("thresholds.image_alt_max_chars", "Max image alt-text length", null, "chars"),
    num("thresholds.image_max_kb", "Max image file size", null, "KB"),
    num("thresholds.low_content_words", "Low-content word count", null, "words"),
    num("thresholds.high_crawl_depth", "High crawl depth", null, "clicks"),
    lst("thresholds.non_descriptive_anchors", "Non-descriptive anchor texts"),
    lst("thresholds.soft_404_patterns", "Soft-404 phrases"),
  ]},
  { id: "robots", label: "robots.txt", icon: "bot", fields: [
    ch("robots.mode", "Mode", ["respect", "ignore", "ignore-report"], "Respect obeys robots.txt like Google does."),
    tg("robots.show_blocked_internal", "Show blocked internal URLs"),
    tg("robots.show_blocked_external", "Show blocked external URLs"),
  ]},
  { id: "rewriting", label: "URL Rewriting", icon: "replace", fields: [
    lst("url_rewriting.remove_params", "Remove query parameters"),
    tg("url_rewriting.lowercase", "Lowercase all URLs", null, true),
  ]},
  { id: "speed", label: "Speed", icon: "zap", fields: [
    num("speed.max_threads", "Max threads", "Parallel downloads."),
    num("speed.max_urls_per_sec", "Max URLs per second", "Politeness throttle. 0 = unlimited.", "URL/s"),
  ]},
  { id: "http", label: "HTTP & Identity", icon: "fingerprint", fields: [
    txt("http.user_agent", "User-agent"),
    txt("http.robots_user_agent", "Robots user-agent token", "Used when matching robots.txt rules."),
    txt("http.proxy", "Proxy", "http://user:pass@host:port", true),
  ]},
  { id: "content", label: "Content Analysis", icon: "text-select", fields: [
    lst("content.area.exclude_elements", "Content area — exclude elements", "Which parts count as 'content' for word count & duplicates."),
    lst("content.area.include_elements", "Content area — include elements"),
    tg("content.near_duplicates.enabled", "Near-duplicate detection"),
    num("content.near_duplicates.threshold", "Similarity threshold", "Re-running analysis is enough — no recrawl needed.", "%"),
    tg("content.near_duplicates.indexable_only", "Only check indexable pages"),
  ]},
  { id: "analysis", label: "Analysis", icon: "git-compare", fields: [
    tg("analysis.auto", "Auto-analyse after crawl"),
    tg("analysis.link_score", "Link score"),
    tg("analysis.redirect_chains", "Redirect chains"),
    tg("analysis.near_duplicates", "Near-duplicates"),
    tg("analysis.pagination", "Pagination"),
    tg("analysis.hreflang", "Hreflang"),
    tg("analysis.canonicals", "Canonicals"),
    tg("analysis.sitemaps", "Sitemaps"),
  ]},
  { id: "storage", label: "Storage", icon: "hard-drive", fields: [
    txt("storage.dir", "Storage folder"),
    num("storage.retention_days", "Crawl retention (days, 0 = forever)", null, "days", true),
  ]},
];

const getPath = (obj, key) => key.split(".").reduce((o, k) => (o == null ? undefined : o[k]), obj);
/* encode a JS value as the YAML literal config.Set expects */
const encodeVal = (f, v) => {
  if (f.type === "toggle") return v ? "true" : "false";
  if (f.type === "number") return String(v);
  return JSON.stringify(v); // strings and arrays — JSON is valid YAML
};

export function SettingsView({ profileName }) {
  const [profiles, setProfiles] = useState(["Default audit"]);
  const [profile, setProfile] = useState(profileName || "Default audit");
  const [cfg, setCfg] = useState(null);
  const [pending, setPending] = useState({}); // key -> new value
  const [active, setActive] = useState("scope");
  const [advanced, setAdvanced] = useState(false);
  const [q, setQ] = useState("");
  const [toast, setToast] = useState(null);
  const [dup, setDup] = useState(false);
  const [dupName, setDupName] = useState("");
  const [yamlMode, setYamlMode] = useState(false);
  const [yamlText, setYamlText] = useState("");
  const fireToast = (msg, icon = "check") => { setToast({ msg, icon }); setTimeout(() => setToast(null), 2600); };

  const reload = (p) => {
    api.getProfileConfig(p).then(setCfg).catch(() => setCfg(null));
    api.getProfileYAML(p).then(setYamlText).catch(() => {});
    setPending({});
  };
  useEffect(() => { api.listProfiles().then((p) => p && p.length && setProfiles(p)).catch(() => {}); }, []);
  useEffect(() => { reload(profile); }, [profile]);

  const fieldsOf = (s) => s.fields || [];
  const allFields = useMemo(() => SECTIONS.flatMap((s) => fieldsOf(s).map((f) => ({ ...f, section: s.label, sectionId: s.id }))), []);
  const searchHits = q ? allFields.filter((f) => f.label.toLowerCase().includes(q.toLowerCase())) : null;
  const sec = SECTIONS.find((s) => s.id === active);
  const valOf = (f) => (f.key in pending ? pending[f.key] : getPath(cfg, f.key));
  const changedCount = Object.keys(pending).length;

  async function save() {
    try {
      const vals = {};
      for (const k of Object.keys(pending)) {
        const f = allFields.find((x) => x.key === k);
        vals[k] = encodeVal(f, pending[k]);
      }
      await api.setConfigValues(profile, vals);
      fireToast("Profile saved — used by the next crawl that picks it", "save");
      reload(profile);
    } catch (e) {
      fireToast("Invalid value: " + e, "circle-alert");
    }
  }
  async function saveYaml() {
    try {
      await api.saveProfileYAML(profile, yamlText);
      fireToast("Profile YAML saved", "save");
      reload(profile);
    } catch (e) {
      fireToast(String(e), "circle-alert");
    }
  }

  return (
    <div className="main" style={{ flexDirection: "row" }}>
      {/* category rail */}
      <div style={{ width: 218, flex: "0 0 218px", borderRight: "1px solid var(--border-soft)", background: "var(--sidebar)", display: "flex", flexDirection: "column", minHeight: 0 }}>
        <div style={{ padding: 11 }}>
          <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 7 }}>Profile</div>
          <select className="input" value={profile} onChange={(e) => setProfile(e.target.value)} style={{ fontWeight: 600, fontSize: 12.5 }}>
            {profiles.map((p) => <option key={p}>{p}</option>)}
          </select>
          <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
            <Btn size="sm" icon="copy" style={{ flex: 1 }} onClick={() => { setDupName(profile + " copy"); setDup(true); }}>Duplicate</Btn>
            <Btn size="sm" icon={yamlMode ? "list" : "file-code"} style={{ flex: 1 }} onClick={() => setYamlMode((v) => !v)}>{yamlMode ? "Tree" : "YAML"}</Btn>
          </div>
        </div>
        {!yamlMode && (
          <div className="sb-recents" style={{ paddingTop: 2 }}>
            {SECTIONS.map((s) => (
              <div key={s.id} className={"sb-item" + (active === s.id ? " active" : "")} onClick={() => { setActive(s.id); setQ(""); }} style={{ height: 30 }}>
                <Icon name={s.icon} size={15} /><span style={{ flex: 1 }}>{s.label}</span>
                {fieldsOf(s).some((f) => f.key in pending) && <span className="statusdot" style={{ background: "var(--accent)" }} />}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* content */}
      <div className="main" style={{ minWidth: 0 }}>
        <div className="toolbar">
          <span className="title" style={{ fontSize: 13.5 }}>Settings</span>
          <span className="sub">{profile}</span>
          <div style={{ flex: 1 }} />
          {!yamlMode && <>
            <Search value={q} onChange={setQ} placeholder="Search all settings…" width={230} />
            <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12, color: "var(--ink-2)" }}>
              <Toggle on={advanced} onChange={setAdvanced} /> Advanced
            </label>
            <Btn icon="save" variant="primary" disabled={!changedCount} onClick={save}>Save profile</Btn>
          </>}
          {yamlMode && <Btn icon="save" variant="primary" onClick={saveYaml}>Save YAML</Btn>}
        </div>

        {!yamlMode && changedCount > 0 && !q && (
          <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 18px", background: "var(--accent-soft)", borderBottom: "1px solid var(--border-soft)", fontSize: 12 }}>
            <Icon name="pencil" size={13} style={{ color: "var(--accent)" }} />
            <span style={{ color: "var(--ink-2)" }}>{changedCount} unsaved change{changedCount > 1 ? "s" : ""}</span>
            <Btn size="sm" variant="ghost" icon="rotate-ccw" onClick={() => setPending({})} style={{ marginLeft: "auto" }}>Discard</Btn>
          </div>
        )}

        {yamlMode ? (
          <div className="scroll" style={{ padding: "16px 20px", display: "flex", flexDirection: "column" }}>
            <div className="hint" style={{ marginBottom: 10 }}>The full profile file — every setting the engine understands, including custom search, custom extraction and link positions. Validated on save.</div>
            <textarea className="input mono" value={yamlText} onChange={(e) => setYamlText(e.target.value)} spellCheck={false}
              style={{ flex: 1, minHeight: 420, padding: 14, lineHeight: 1.65, fontSize: 11.5, resize: "none" }} />
          </div>
        ) : (
          <div className="scroll" style={{ padding: "20px 24px" }}>
            <div style={{ maxWidth: 720 }} className="fade">
              {!cfg && <Empty icon="sliders-horizontal" title="Loading profile…"> </Empty>}
              {cfg && q && searchHits && (
                <>
                  <div style={{ fontSize: 12, color: "var(--ink-faint)", marginBottom: 14 }}>{searchHits.length} settings match “{q}”</div>
                  {searchHits.map((f) => <SettingField key={f.key} f={f} val={valOf(f)} changed={f.key in pending} section={f.section} onChange={(v) => setPending((p) => ({ ...p, [f.key]: v }))} onReset={() => setPending((p) => { const n = { ...p }; delete n[f.key]; return n; })} />)}
                  {searchHits.length === 0 && <Empty icon="search-x" title="No settings found">Nothing matches “{q}”.</Empty>}
                </>
              )}
              {cfg && !q && sec && (
                <>
                  <div style={{ marginBottom: 18 }}>
                    <h2 style={{ margin: 0, fontSize: 16, fontWeight: 650, display: "flex", alignItems: "center", gap: 9 }}><Icon name={sec.icon} size={18} style={{ color: "var(--ink-3)" }} />{sec.label}</h2>
                  </div>
                  {fieldsOf(sec).filter((f) => advanced || !f.advanced).map((f) => (
                    <SettingField key={f.key} f={f} val={valOf(f)} changed={f.key in pending} onChange={(v) => setPending((p) => ({ ...p, [f.key]: v }))} onReset={() => setPending((p) => { const n = { ...p }; delete n[f.key]; return n; })} />
                  ))}
                  {!advanced && fieldsOf(sec).some((f) => f.advanced) && (
                    <button onClick={() => setAdvanced(true)} className="btn ghost" style={{ marginTop: 8, color: "var(--ink-faint)" }}><Icon name="chevron-down" size={14} />Show {fieldsOf(sec).filter((f) => f.advanced).length} advanced settings</button>
                  )}
                </>
              )}
            </div>
          </div>
        )}
      </div>

      {dup && (
        <Modal onClose={() => setDup(false)} icon="copy" title="Duplicate profile"
          body={<input className="input" value={dupName} autoFocus onChange={(e) => setDupName(e.target.value)} placeholder="New profile name" style={{ marginTop: 6 }} />}
          actions={<>
            <Btn onClick={() => setDup(false)}>Cancel</Btn>
            <Btn variant="primary" icon="copy" onClick={async () => {
              try {
                await api.duplicateProfile(profile, dupName);
                const ps = await api.listProfiles();
                setProfiles(ps);
                setProfile(dupName.trim());
                setDup(false);
                fireToast("Profile duplicated", "copy");
              } catch (e) { fireToast(String(e), "circle-alert"); }
            }}>Duplicate</Btn>
          </>} />
      )}
      {toast && <Toast {...toast} />}
    </div>
  );
}

/* ---- individual setting field ----------------------------------------- */
function SettingField({ f, val, changed, onChange, onReset, section }) {
  return (
    <div style={{ display: "flex", gap: 16, padding: "13px 0", borderBottom: "1px solid var(--border-soft)", alignItems: "flex-start" }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontSize: 12.5, fontWeight: 600 }}>{f.label}</span>
          {section && <span style={{ fontSize: 10.5, color: "var(--ink-faint)" }}>· {section}</span>}
          {changed && <span title="Unsaved change" className="statusdot" style={{ background: "var(--accent)" }} />}
        </div>
        {f.hint && <div className="hint" style={{ marginTop: 4 }}>{f.hint}</div>}
        <div className="hint mono" style={{ marginTop: 3, fontSize: 10 }}>{f.key}</div>
      </div>
      <div style={{ flex: "0 0 auto", display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        {changed && <IconBtn icon="rotate-ccw" size={13} title="Revert" onClick={onReset} />}
        <FieldControl f={f} val={val} onChange={onChange} />
      </div>
    </div>
  );
}

function FieldControl({ f, val, onChange }) {
  if (f.type === "toggle") return <Toggle on={!!val} onChange={onChange} />;
  if (f.type === "choice") return f.options.length <= 3
    ? <Seg value={val} onChange={onChange} options={f.options} />
    : <select className="input" value={val} onChange={(e) => onChange(e.target.value)} style={{ width: "auto", minWidth: 160 }}>{f.options.map((o) => <option key={o}>{o}</option>)}</select>;
  if (f.type === "number") {
    const unlimited = val === -1;
    return <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
      <input className="input mono" value={unlimited ? "" : (val ?? "")} placeholder={unlimited ? "Unlimited" : ""}
        onChange={(e) => onChange(e.target.value === "" ? -1 : +e.target.value.replace(/[^\d.]/g, "") || 0)} style={{ width: 92, textAlign: "right" }} />
      {f.unit && <span style={{ fontSize: 11.5, color: "var(--ink-faint)", width: 40 }}>{f.unit}</span>}
    </div>;
  }
  if (f.type === "text") return <input className="input mono" value={val ?? ""} onChange={(e) => onChange(e.target.value)} style={{ width: 260, fontSize: 11.5 }} />;
  if (f.type === "list") return <ListEditor items={Array.isArray(val) ? val : []} onChange={onChange} regex={f.label.toLowerCase().includes("regex") || f.label.toLowerCase().includes("pattern")} />;
  return null;
}

/* ---- chips list editor ------------------------------------------------ */
function ListEditor({ items, onChange, regex }) {
  const [draft, setDraft] = useState("");
  const add = () => { if (draft.trim()) { onChange([...items, draft.trim()]); setDraft(""); } };
  const invalid = (s) => { if (!regex) return false; try { new RegExp(s); return false; } catch { return true; } };
  return (
    <div style={{ width: 300 }}>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: items.length ? 7 : 0 }}>
        {items.map((it, i) => {
          const bad = invalid(it);
          return <span key={i} className="pill" style={{ height: 24, paddingRight: 4, fontFamily: regex ? "var(--font-mono)" : "inherit", fontSize: 11, borderColor: bad ? "var(--s-4xx)" : "var(--border)", color: bad ? "var(--s-4xx)" : "var(--ink-2)" }}>
            {bad && <Icon name="circle-alert" size={11} />}{it}
            <button className="iconbtn" style={{ width: 16, height: 16 }} onClick={() => onChange(items.filter((_, j) => j !== i))}><Icon name="x" size={11} /></button>
          </span>;
        })}
      </div>
      <input className="input" value={draft} placeholder={regex ? "Add pattern…  e.g. /tag/.*" : "Add…"} onChange={(e) => setDraft(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} style={{ fontFamily: regex ? "var(--font-mono)" : "inherit", fontSize: 11.5 }} />
    </div>
  );
}
