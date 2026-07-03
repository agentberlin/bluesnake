/* ===========================================================================
   Settings — the app settings (internally the default profile: what every
   crawl uses unless it picks a profile) plus named profiles as snapshots of
   them. Curated tree bound to the real config schema (dotted yaml-tag keys),
   search, simple/advanced, raw YAML editor.
   =========================================================================== */
import React, { useEffect, useMemo, useRef, useState } from "react";
import { Icon, Btn, IconBtn, Search, Toggle, Seg, Empty, Toast, Modal } from "../ui";
import { api, on, openURL, DEFAULT_PROFILE, profileLabel } from "../api";
import { SECTIONS, getPath, encodeVal } from "./config-schema";

export function SettingsView({ profileName, focus, onBack, backLabel }) {
  const [profiles, setProfiles] = useState([DEFAULT_PROFILE]);
  const [profile, setProfile] = useState(profileName || DEFAULT_PROFILE);
  const [cfg, setCfg] = useState(null);
  const [pending, setPending] = useState({}); // key -> new value
  const [active, setActive] = useState("scope");
  const [advanced, setAdvanced] = useState(false);
  const [q, setQ] = useState("");
  const [toast, setToast] = useState(null);
  const [dup, setDup] = useState(false); // "save as profile" (app settings) / duplicate (named profile)
  const [dupName, setDupName] = useState("");
  const [del, setDel] = useState(false); // delete confirm for a named profile
  const [yamlMode, setYamlMode] = useState(false);
  const [yamlText, setYamlText] = useState("");
  const [cliAvail, setCliAvail] = useState(false); // CLI install panel only shows when an embedded CLI exists (macOS app)
  const fireToast = (msg, icon = "check") => { setToast({ msg, icon }); setTimeout(() => setToast(null), 2600); };

  const reload = (p) => {
    api.getProfileConfig(p).then(setCfg).catch(() => setCfg(null));
    api.getProfileYAML(p).then(setYamlText).catch(() => {});
    setPending({});
  };
  useEffect(() => { api.listProfiles().then((p) => p && p.length && setProfiles(p)).catch(() => {}); }, []);
  useEffect(() => { api.cliInfo().then((s) => setCliAvail(!!(s && s.available))).catch(() => {}); }, []);
  useEffect(() => { reload(profile); }, [profile]);
  // deep-link from the titlebar MCP pill (and anywhere else)
  useEffect(() => { if (focus && focus.section) { setActive(focus.section); setQ(""); setYamlMode(false); } }, [focus]);

  const fieldsOf = (s) => s.fields || [];
  const allFields = useMemo(() => SECTIONS.flatMap((s) => fieldsOf(s).map((f) => ({ ...f, section: s.label, sectionId: s.id }))), []);
  const searchHits = q ? allFields.filter((f) => f.label.toLowerCase().includes(q.toLowerCase())) : null;
  const sec = SECTIONS.find((s) => s.id === active);
  const valOf = (f) => (f.key in pending ? pending[f.key] : getPath(cfg, f.key));
  const changedCount = Object.keys(pending).length;
  // app-level panels (MCP, CLI, Updates) aren't crawl profiles — they hide the
  // profile chrome (search, advanced, save) and don't load profile config.
  const appSection = active === "mcp" || active === "cli" || active === "updates";

  const isApp = profile === DEFAULT_PROFILE; // presenting the default profile as "App settings"

  async function save() {
    try {
      const vals = {};
      for (const k of Object.keys(pending)) {
        const f = allFields.find((x) => x.key === k);
        vals[k] = encodeVal(f, pending[k]);
      }
      await api.setConfigValues(profile, vals);
      fireToast(isApp ? "Settings saved — the next crawl uses them" : "Profile saved — used by the next crawl that picks it", "save");
      reload(profile);
    } catch (e) {
      fireToast("Invalid value: " + e, "circle-alert");
    }
  }
  async function saveYaml() {
    try {
      await api.saveProfileYAML(profile, yamlText);
      fireToast(isApp ? "Settings YAML saved" : "Profile YAML saved", "save");
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
          <div style={{ fontSize: 10.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 7 }}>Configuration</div>
          <select className="input" value={profile} onChange={(e) => setProfile(e.target.value)} style={{ fontWeight: 600, fontSize: 12.5 }}>
            {profiles.map((p) => <option key={p} value={p}>{profileLabel(p)}</option>)}
          </select>
          <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
            {isApp
              ? <Btn size="sm" icon="bookmark-plus" style={{ flex: 1 }} title="Snapshot the current app settings as a named, reusable profile" onClick={() => { setDupName(""); setDup(true); }}>Save as profile</Btn>
              : <>
                  <Btn size="sm" icon="copy" style={{ flex: 1 }} onClick={() => { setDupName(profile + " copy"); setDup(true); }}>Duplicate</Btn>
                  <IconBtn icon="trash-2" title="Delete this profile" onClick={() => setDel(true)} />
                </>}
            <Btn size="sm" icon={yamlMode ? "list" : "file-code"} style={{ flex: 1 }} onClick={() => setYamlMode((v) => !v)}>{yamlMode ? "Tree" : "YAML"}</Btn>
          </div>
          <div className="hint" style={{ marginTop: 8, lineHeight: 1.45 }}>
            {isApp
              ? "Every crawl uses these settings unless it picks a profile."
              : "A saved snapshot — pick it on the New Crawl form to use it."}
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
            {/* app-level settings — not part of any crawl profile */}
            <div className="sb-sectlabel" style={{ paddingTop: 14 }}>Application</div>
            <div className={"sb-item" + (active === "mcp" ? " active" : "")} onClick={() => { setActive("mcp"); setQ(""); }} style={{ height: 30 }}>
              <Icon name="plug-zap" size={15} /><span style={{ flex: 1 }}>MCP Server</span>
            </div>
            {cliAvail && (
              <div className={"sb-item" + (active === "cli" ? " active" : "")} onClick={() => { setActive("cli"); setQ(""); }} style={{ height: 30 }}>
                <Icon name="terminal" size={15} /><span style={{ flex: 1 }}>Command-line tool</span>
              </div>
            )}
            <div className={"sb-item" + (active === "updates" ? " active" : "")} onClick={() => { setActive("updates"); setQ(""); }} style={{ height: 30 }}>
              <Icon name="refresh-cw" size={15} /><span style={{ flex: 1 }}>Updates</span>
            </div>
          </div>
        )}
      </div>

      {/* content */}
      <div className="main" style={{ minWidth: 0 }}>
        <div className="toolbar">
          {onBack && <Btn size="sm" variant="ghost" icon="arrow-left" onClick={onBack} style={{ marginRight: 2 }}>{backLabel || "Back"}</Btn>}
          <span className="title" style={{ fontSize: 13.5 }}>Settings</span>
          <span className="sub">{appSection && !yamlMode ? "Application" : profileLabel(profile)}</span>
          <div style={{ flex: 1 }} />
          {!yamlMode && !appSection && <>
            <Search value={q} onChange={setQ} placeholder="Search all settings…" width={230} />
            <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12, color: "var(--ink-2)" }}>
              <Toggle on={advanced} onChange={setAdvanced} /> Advanced
            </label>
            <Btn icon="save" variant="primary" disabled={!changedCount} onClick={save}>{isApp ? "Save settings" : "Save profile"}</Btn>
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
            <div className="hint" style={{ marginBottom: 10 }}>The full configuration file — every setting the engine understands, including custom search, custom extraction and link positions. Validated on save.</div>
            <textarea className="input mono" value={yamlText} onChange={(e) => setYamlText(e.target.value)} spellCheck={false}
              style={{ flex: 1, minHeight: 420, padding: 14, lineHeight: 1.65, fontSize: 11.5, resize: "none" }} />
          </div>
        ) : (
          <div className="scroll" style={{ padding: "20px 24px" }}>
            <div style={{ maxWidth: 720 }} className="fade">
              {active === "mcp" && !q && <MCPPanel onToast={fireToast} />}
              {active === "cli" && <CLIPanel onToast={fireToast} />}
              {active === "updates" && <UpdatesPanel onToast={fireToast} />}
              {!appSection && !cfg && <Empty icon="sliders-horizontal" title="Loading profile…"> </Empty>}
              {!appSection && cfg && q && searchHits && (
                <>
                  <div style={{ fontSize: 12, color: "var(--ink-faint)", marginBottom: 14 }}>{searchHits.length} settings match “{q}”</div>
                  {searchHits.map((f) => <SettingField key={f.key} f={f} val={valOf(f)} changed={f.key in pending} section={f.section} onChange={(v) => setPending((p) => ({ ...p, [f.key]: v }))} onReset={() => setPending((p) => { const n = { ...p }; delete n[f.key]; return n; })} />)}
                  {searchHits.length === 0 && <Empty icon="search-x" title="No settings found">Nothing matches “{q}”.</Empty>}
                </>
              )}
              {!appSection && cfg && !q && sec && (
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
        <Modal onClose={() => setDup(false)} icon={isApp ? "bookmark-plus" : "copy"}
          title={isApp ? "Save as profile" : "Duplicate profile"}
          body={<div>
            {isApp && <div className="hint" style={{ marginBottom: 6 }}>Snapshots the current app settings as a named profile you can pick on the New Crawl form. The app settings themselves stay as they are.</div>}
            {changedCount > 0 && <div className="hint" style={{ marginBottom: 6, color: "var(--sev-warn)" }}>You have unsaved changes — they won't be included. Save first to snapshot them.</div>}
            <input className="input" value={dupName} autoFocus onChange={(e) => setDupName(e.target.value)} placeholder="New profile name" style={{ marginTop: 6 }} />
          </div>}
          actions={<>
            <Btn onClick={() => setDup(false)}>Cancel</Btn>
            <Btn variant="primary" icon={isApp ? "bookmark-plus" : "copy"} disabled={!dupName.trim()} onClick={async () => {
              try {
                await api.duplicateProfile(profile, dupName);
                const ps = await api.listProfiles();
                setProfiles(ps);
                setProfile(dupName.trim());
                setDup(false);
                fireToast(isApp ? "Profile saved from the app settings" : "Profile duplicated", "copy");
              } catch (e) { fireToast(String(e), "circle-alert"); }
            }}>{isApp ? "Save profile" : "Duplicate"}</Btn>
          </>} />
      )}
      {del && (
        <Modal onClose={() => setDel(false)} icon="trash-2" danger title="Delete profile?"
          body={<>This deletes the profile <b>{profile}</b>. Crawls that used it keep their own frozen copy of its settings.</>}
          actions={<>
            <Btn onClick={() => setDel(false)}>Cancel</Btn>
            <Btn variant="primary" icon="trash-2" onClick={async () => {
              try {
                await api.deleteProfile(profile);
                const ps = await api.listProfiles();
                setProfiles(ps && ps.length ? ps : [DEFAULT_PROFILE]);
                setProfile(DEFAULT_PROFILE);
                setDel(false);
                fireToast("Profile deleted", "trash-2");
              } catch (e) { fireToast(String(e), "circle-alert"); }
            }}>Delete</Btn>
          </>} />
      )}
      {toast && <Toast {...toast} />}
    </div>
  );
}

/* ---- MCP server panel (app-level, not part of any profile) -------------
   One control surface: Start the server, and — on by default — expose it
   over a public HTTPS URL. Both the local and public URLs are shown to copy.
   The public URL is just the reverse tunnel pointed at the local listener. */
function MCPPanel({ onToast }) {
  const [st, setSt] = useState(null);     // MCPStatus
  const [t, setT] = useState(null);       // TunnelStatus
  const [port, setPort] = useState("");
  const [wantPublic, setWantPublic] = useState(true); // default: also get a public URL
  const [busy, setBusy] = useState(false);
  const seeded = useRef(false);

  useEffect(() => {
    api.getMCPStatus().then((s) => { setSt(s); setPort(String(s.port)); }).catch(() => {});
    api.getTunnelStatus().then(setT).catch(() => {});
    const offMcp = on("mcp:status", (s) => { setSt(s); setPort(String(s.port)); });
    const offTun = on("tunnel:status", setT);
    return () => { offMcp(); offTun(); };
  }, []);

  // Seed the "public URL" intent once from persisted state: respect a prior
  // explicit opt-out (server on, tunnel off); otherwise leave it on by default.
  useEffect(() => {
    if (seeded.current || !st || !t) return;
    seeded.current = true;
    if (t.enabled) setWantPublic(true);
    else if (st.enabled) setWantPublic(false);
  }, [st, t]);

  async function toggleServer() {
    if (busy || !st) return;
    setBusy(true);
    try {
      const turningOn = !st.enabled;
      const s = await api.setMCPEnabled(turningOn);
      setSt(s);
      if (s.error) { onToast(s.error, "circle-alert"); return; }
      onToast(turningOn ? "MCP server started" : "MCP server stopped", "plug-zap");
      if (turningOn && wantPublic && t && !t.enabled) {
        setT(await api.setTunnelEnabled(true));            // honour the default public URL
      } else if (!turningOn && t && t.enabled) {
        setT(await api.setTunnelEnabled(false));           // take the public URL down with the server
      }
    } finally { setBusy(false); }
  }

  async function togglePublic(next) {
    setWantPublic(next);
    if (!st || !st.enabled) return;                        // no server yet — just remember the intent
    setBusy(true);
    try {
      const tt = await api.setTunnelEnabled(next);
      setT(tt);
      if (tt.error) onToast(tt.error, "circle-alert");
      else onToast(next ? "Creating public URL…" : "Public URL disabled", "globe");
    } finally { setBusy(false); }
  }

  async function applyPort() {
    const p = parseInt(port, 10);
    if (!p || p === st.port) { setPort(String(st.port)); return; }
    const s = await api.setMCPPort(p);
    setSt(s);
    setPort(String(s.port));
    if (s.error) onToast(s.error, "circle-alert");
    else onToast("MCP port set to " + s.port, "plug-zap");
  }
  async function copy(text) {
    try {
      if (window.runtime && window.runtime.ClipboardSetText) await window.runtime.ClipboardSetText(text);
      else await navigator.clipboard.writeText(text);
      onToast("Copied to clipboard", "copy");
    } catch { onToast("Copy failed", "circle-alert"); }
  }

  if (!st) return <Empty icon="plug-zap" title="Loading…"> </Empty>;
  const cmdSnippet = `claude mcp add --transport http bluesnake ${st.endpoint}`;
  const jsonSnippet = `{"mcpServers": {"bluesnake": {"type": "http", "url": "${st.endpoint}"}}}`;

  const pub = t || { enabled: false, state: "disabled" };
  const pubOnline = pub.state === "online";
  const pubConnecting = pub.state === "connecting";
  const pubLabel = pubOnline ? "Live"
    : pubConnecting ? "Connecting…"
    : pub.state === "error" ? "Error" : "Starting…";
  const pubColor = pubOnline ? "var(--sev-ok)" : pub.state === "error" ? "var(--s-4xx)" : "var(--ink-faint)";

  return (
    <>
      <div style={{ marginBottom: 18 }}>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 650, display: "flex", alignItems: "center", gap: 9 }}>
          <Icon name="plug-zap" size={18} style={{ color: "var(--ink-3)" }} />MCP Server
        </h2>
        <div className="hint" style={{ marginTop: 6 }}>
          Let LLM agents (Claude Code, Claude Desktop, any MCP client) drive bluesnake: start crawls with any
          configuration, watch progress, and analyse results with read-only SQL. Runs on this machine, with an
          optional public URL for remote clients.
        </div>
      </div>

      {/* server control + the public-URL option, together */}
      <div className="card" style={{ padding: 0, overflow: "hidden", marginBottom: 16 }}>
        <div style={{ display: "flex", gap: 16, padding: "14px 16px", alignItems: "center" }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 650 }}>Start MCP server</div>
            <div className="hint" style={{ marginTop: 4 }}>Stays on across restarts. Crawls an agent starts appear here live, and the pause/stop buttons work on them.</div>
          </div>
          <Toggle on={st.enabled} onChange={toggleServer} disabled={busy} />
        </div>
        <div style={{ display: "flex", gap: 13, padding: "12px 16px", alignItems: "center", borderTop: "1px solid var(--border-soft)", background: "var(--surface-2)" }}>
          <Icon name="globe" size={16} style={{ color: "var(--ink-3)", flex: "0 0 16px" }} />
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 12.5, fontWeight: 600 }}>Also create a public URL</div>
            <div className="hint" style={{ marginTop: 3 }}>On by default — reach this server from a remote MCP client over HTTPS. Turn off to keep it on this machine only.</div>
          </div>
          <Toggle on={wantPublic} onChange={togglePublic} disabled={busy} />
        </div>
      </div>

      {/* live status + URLs to copy */}
      {st.running ? (
        <div style={{ padding: "2px 0 16px", borderBottom: "1px solid var(--border-soft)" }}>
          <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
            <span className="statusdot" style={{ background: "var(--sev-ok)" }} />
            <span style={{ fontSize: 12.5, fontWeight: 600 }}>Running</span>
            {st.error && <span style={{ fontSize: 11.5, color: "var(--s-4xx)" }}><Icon name="circle-alert" size={12} /> {st.error}</span>}
          </div>
          <UrlRow label="Local URL" value={st.endpoint} onCopy={copy} />
          {wantPublic && (
            <>
              <UrlRow label="Public URL" value={pub.mcpUrl || ""} badge={pubLabel} badgeColor={pubColor}
                placeholder={pub.state === "error" ? (pub.error || "Couldn’t create a public URL") : "Creating public URL…"}
                onCopy={copy} />
              <Note>
                This URL reaches only this MCP server — an agent can start crawls and read your crawl data through
                it, and nothing else on your machine. The address is randomized, so only the people you share it
                with can use it. Switch “public URL” off anytime to take it offline.
              </Note>
            </>
          )}
        </div>
      ) : (
        <div style={{ display: "flex", gap: 10, padding: "13px 0", borderBottom: "1px solid var(--border-soft)", alignItems: "center" }}>
          <span className="statusdot" style={{ background: "var(--ink-faint)" }} />
          <span style={{ fontSize: 12.5, fontWeight: 600 }}>Stopped</span>
          {st.error && <span style={{ fontSize: 11.5, color: "var(--s-4xx)" }}><Icon name="circle-alert" size={12} /> {st.error}</span>}
        </div>
      )}

      {/* port */}
      <div style={{ display: "flex", gap: 16, padding: "13px 0", borderBottom: "1px solid var(--border-soft)", alignItems: "center" }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 12.5, fontWeight: 600 }}>Port</div>
          <div className="hint" style={{ marginTop: 4 }}>Applied immediately — reconnect clients after changing it.</div>
        </div>
        <input className="input mono" value={port} onChange={(e) => setPort(e.target.value.replace(/[^\d]/g, ""))}
          onBlur={applyPort} onKeyDown={(e) => e.key === "Enter" && e.target.blur()} style={{ width: 92, textAlign: "right" }} />
      </div>

      {/* connect snippets */}
      <div style={{ padding: "16px 0" }}>
        <div style={{ fontSize: 12.5, fontWeight: 600, marginBottom: 10 }}>Connect a client</div>
        <Snippet label="Claude Code" text={cmdSnippet} onCopy={copy} />
        <Snippet label="Any MCP client (JSON config)" text={jsonSnippet} onCopy={copy} />
        <div className="hint" style={{ marginTop: 8 }}>
          Without the app running, the CLI serves the same endpoint: <span className="mono">bluesnake mcp</span>
        </div>
      </div>
    </>
  );
}

/* ---- command-line tool panel (app-level) ------------------------------
   Installs the CLI embedded in the .app bundle by symlinking it onto PATH —
   the same job the old "Install bluesnake CLI.command" did, now in-app. */
function CLIPanel({ onToast }) {
  const [st, setSt] = useState(null);   // CLIStatus
  const [busy, setBusy] = useState(false);

  useEffect(() => { api.cliInfo().then(setSt).catch(() => {}); }, []);

  async function install() {
    if (busy) return;
    setBusy(true);
    try {
      const next = await api.installCLI();
      setSt(next);
      if (next && next.error) onToast(next.error, "circle-alert");
      else onToast("Command-line tool installed", "terminal");
    } catch (e) {
      onToast(String(e), "circle-alert");
    } finally { setBusy(false); }
  }
  async function copy(text) {
    try {
      if (window.runtime && window.runtime.ClipboardSetText) await window.runtime.ClipboardSetText(text);
      else await navigator.clipboard.writeText(text);
      onToast("Copied to clipboard", "copy");
    } catch { onToast("Copy failed", "circle-alert"); }
  }

  if (!st) return <Empty icon="terminal" title="Loading…"> </Empty>;

  return (
    <>
      <div style={{ marginBottom: 18 }}>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 650, display: "flex", alignItems: "center", gap: 9 }}>
          <Icon name="terminal" size={18} style={{ color: "var(--ink-3)" }} />Command-line tool
        </h2>
        <div className="hint" style={{ marginTop: 6 }}>
          Run bluesnake straight from your terminal — script crawls, wire audits into CI, and pipe results into
          other tools. This adds the <span className="mono">bluesnake</span> command to your PATH; it’s the same
          engine the app uses, and it also serves the MCP endpoint with <span className="mono">bluesnake mcp</span>.
        </div>
      </div>

      {!st.available ? (
        <Note>The command-line tool isn’t bundled with this build, so it can’t be installed from here.</Note>
      ) : (
        <div className="card" style={{ padding: 0, overflow: "hidden", marginBottom: 16 }}>
          <div style={{ display: "flex", gap: 16, padding: "14px 16px", alignItems: "center" }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 13, fontWeight: 650, display: "flex", alignItems: "center", gap: 8 }}>
                <span className="statusdot" style={{ background: st.installed ? "var(--sev-ok)" : "var(--ink-faint)" }} />
                {st.installed ? "Installed" : "Not installed"}
              </div>
              <div className="hint" style={{ marginTop: 4 }}>
                {st.installed
                  ? <>Linked at <span className="mono">{st.target}</span></>
                  : "Symlinks bluesnake into /usr/local/bin, /opt/homebrew/bin, or ~/.local/bin (whichever is writable)."}
              </div>
            </div>
            <Btn icon={st.installed ? "rotate-ccw" : "download"} variant="primary" disabled={busy} onClick={install}>
              {busy ? "Installing…" : st.installed ? "Reinstall" : "Install"}
            </Btn>
          </div>
        </div>
      )}

      {st.available && (
        <div style={{ padding: "4px 0" }}>
          <div style={{ fontSize: 12.5, fontWeight: 600, marginBottom: 10 }}>Try it</div>
          <Snippet label="Check the version" text="bluesnake version" onCopy={copy} />
          <Snippet label="Crawl a site" text="bluesnake crawl https://example.com" onCopy={copy} />
          <div className="hint" style={{ marginTop: 8 }}>Open a new terminal after installing so it picks up the command.</div>
        </div>
      )}
    </>
  );
}

/* ---- updates panel (app-level) ----------------------------------------
   Current version, manual check, auto-check toggle, and the in-place install.
   The proactive surface is the title-bar pill (see main.jsx); this is the
   always-available home and where you land after dismissing the pill. */
function UpdatesPanel({ onToast }) {
  const [st, setSt] = useState(null);       // UpdateStatus
  const [prefs, setPrefs] = useState(null); // UpdatePrefs
  const [busy, setBusy] = useState(false);  // checking
  const [installing, setInstalling] = useState(false);
  const [prog, setProg] = useState(null);   // {phase, done, total}

  useEffect(() => {
    api.getUpdatePrefs().then(setPrefs).catch(() => {});
    api.checkForUpdate().then(setSt).catch(() => {});
    const off = on("update:progress", (e) => { if (e) setProg(e); });
    return () => off();
  }, []);

  async function check() {
    if (busy) return;
    setBusy(true);
    try {
      const r = await api.refreshUpdate();
      setSt(r);
      if (r.error) onToast(r.error, "circle-alert");
      else if (r.isDev) onToast("Development build — updates are disabled", "info");
      else if (r.available) onToast(`Update available — v${r.latest}`, "arrow-up-circle");
      else onToast("You’re up to date", "check");
      api.getUpdatePrefs().then(setPrefs).catch(() => {});
    } finally { setBusy(false); }
  }

  async function install() {
    if (installing) return;
    setInstalling(true);
    setProg({ phase: "downloading", done: 0, total: 0 });
    try {
      const r = await api.applyUpdate();
      if (r && r.error) { onToast(r.error, "circle-alert"); setInstalling(false); }
      // on success the app downloads, installs, and restarts itself
    } catch (e) { onToast(String(e), "circle-alert"); setInstalling(false); }
  }

  async function toggleAuto(v) {
    setPrefs((p) => ({ ...(p || {}), autoCheck: v }));
    await api.setUpdateAutoCheck(v);
  }

  if (!st) return <Empty icon="refresh-cw" title="Loading…"> </Empty>;

  const current = st.current || "—";
  const lastChecked = prefs && prefs.lastCheck ? new Date(prefs.lastCheck).toLocaleString() : null;
  const pct = prog && prog.total > 0 ? Math.round((prog.done / prog.total) * 100) : null;
  const canUpdate = st.available && st.platformSupported;

  return (
    <>
      <div style={{ marginBottom: 18 }}>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 650, display: "flex", alignItems: "center", gap: 9 }}>
          <Icon name="refresh-cw" size={18} style={{ color: "var(--ink-3)" }} />Updates
        </h2>
        <div className="hint" style={{ marginTop: 6 }}>
          bluesnake checks GitHub for new releases and can update itself in place. Downloads are
          checksum-verified before they’re installed.
        </div>
      </div>

      <div className="card" style={{ padding: 0, overflow: "hidden", marginBottom: 16 }}>
        <div style={{ display: "flex", gap: 16, padding: "14px 16px", alignItems: "center" }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 650, display: "flex", alignItems: "center", gap: 8 }}>
              <span className="statusdot" style={{ background: canUpdate ? "var(--accent)" : "var(--sev-ok)" }} />
              Current version <span className="mono" style={{ color: "var(--ink-2)" }}>v{current}</span>
            </div>
            <div className="hint" style={{ marginTop: 4 }}>
              {st.isDev ? "Development build — updates are disabled."
                : st.error ? st.error
                : canUpdate ? <>Version <span className="mono">v{st.latest}</span> is available.</>
                : !st.platformSupported && st.latest ? "In-app updates aren’t available on this platform — download from the release page."
                : "You’re running the latest version."}
            </div>
          </div>
          {canUpdate
            ? <Btn icon="download" variant="primary" disabled={installing} onClick={install}>{installing ? "Updating…" : `Update to v${st.latest}`}</Btn>
            : <Btn icon="refresh-cw" disabled={busy || st.isDev} onClick={check}>{busy ? "Checking…" : "Check for updates"}</Btn>}
        </div>

        {installing && (
          <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border-soft)", background: "var(--surface-2)" }}>
            <div style={{ fontSize: 12, marginBottom: 8 }}>
              {prog && prog.phase === "applying" ? "Installing & restarting…" : pct != null ? `Downloading… ${pct}%` : "Downloading…"}
            </div>
            <div style={{ height: 6, background: "var(--border-soft)", borderRadius: 4, overflow: "hidden" }}>
              <div style={{ height: "100%", width: (pct != null ? pct : 15) + "%", background: "var(--accent)", transition: "width .2s ease" }} />
            </div>
          </div>
        )}
      </div>

      {canUpdate && st.notes && (
        <div style={{ marginBottom: 16 }}>
          <div style={{ fontSize: 12.5, fontWeight: 600, marginBottom: 8 }}>What’s new in v{st.latest}</div>
          <div style={{ maxHeight: 220, overflowY: "auto", fontSize: 11.5, lineHeight: 1.6, color: "var(--ink-2)", background: "var(--sidebar)", border: "1px solid var(--border-soft)", borderRadius: 8, padding: "10px 12px", whiteSpace: "pre-wrap" }}>
            {st.notes}
          </div>
        </div>
      )}

      <div style={{ display: "flex", gap: 16, padding: "13px 0", borderBottom: "1px solid var(--border-soft)", alignItems: "center" }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 12.5, fontWeight: 600 }}>Check automatically on launch</div>
          <div className="hint" style={{ marginTop: 4 }}>Show a notification in the title bar when a new version is available.</div>
        </div>
        <Toggle on={!!(prefs && prefs.autoCheck)} onChange={toggleAuto} />
      </div>

      <div style={{ display: "flex", gap: 12, padding: "13px 0", alignItems: "center" }}>
        <div style={{ flex: 1, minWidth: 0, fontSize: 11.5, color: "var(--ink-faint)" }}>
          {lastChecked ? <>Last checked {lastChecked}</> : "Not checked yet"}
        </div>
        {st.url && <Btn size="sm" variant="ghost" icon="external-link" onClick={() => openURL(st.url)}>Release page</Btn>}
        {canUpdate && <Btn size="sm" variant="ghost" icon="refresh-cw" disabled={busy} onClick={check}>{busy ? "Checking…" : "Re-check"}</Btn>}
      </div>
    </>
  );
}

/* a copyable endpoint with an optional state badge (local + public URLs) */
function UrlRow({ label, value, placeholder, badge, badgeColor, onCopy }) {
  const has = !!value;
  return (
    <div style={{ marginTop: 11 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 4 }}>
        <span style={{ fontSize: 10.5, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em" }}>{label}</span>
        {badge && <span style={{ fontSize: 10.5, fontWeight: 600, color: badgeColor }}>· {badge}</span>}
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <pre className="mono" style={{ flex: 1, margin: 0, padding: "8px 10px", fontSize: 11, lineHeight: 1.5, border: "1px solid var(--border-soft)", borderRadius: 6, background: "var(--sidebar)", overflowX: "auto", whiteSpace: "pre-wrap", wordBreak: "break-all", color: has ? "var(--ink)" : "var(--ink-faint)" }}>{has ? value : (placeholder || "…")}</pre>
        {has && <IconBtn icon="copy" size={14} title={"Copy " + label.toLowerCase()} onClick={() => onCopy(value)} />}
      </div>
    </div>
  );
}

/* a calm informational note (sticky-note feel — not an alarm) */
function Note({ children }) {
  return (
    <div style={{ display: "flex", gap: 9, alignItems: "flex-start", marginTop: 12, padding: "10px 12px", background: "var(--surface-2)", border: "1px solid var(--border-soft)", borderRadius: 8, fontSize: 11.5, color: "var(--ink-2)", lineHeight: 1.55 }}>
      <Icon name="info" size={14} style={{ flex: "0 0 14px", marginTop: 1, color: "var(--ink-faint)" }} />
      <span>{children}</span>
    </div>
  );
}

function Snippet({ label, text, onCopy }) {
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ fontSize: 10.5, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: 4 }}>{label}</div>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <pre className="mono" style={{ flex: 1, margin: 0, padding: "8px 10px", fontSize: 11, lineHeight: 1.5, border: "1px solid var(--border-soft)", borderRadius: 6, background: "var(--sidebar)", overflowX: "auto", whiteSpace: "pre-wrap", wordBreak: "break-all" }}>{text}</pre>
        <IconBtn icon="copy" size={14} title="Copy" onClick={() => onCopy(text)} />
      </div>
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
