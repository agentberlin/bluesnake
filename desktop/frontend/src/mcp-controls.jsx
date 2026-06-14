/* ===========================================================================
   bluesnake — titlebar MCP controls

   The titlebar has almost no room, so the MCP control is three inline bits:
   a switch to flip the server on/off, a copy button that grabs the URL you'd
   actually share (the public URL when it's on, otherwise the local one), and a
   gear into the full Settings panel for the deeper knobs (port, snippets).

   Public URL defaults on for a fresh install; after that we honour whatever the
   user last chose — the same rule as the Settings MCP panel, so the two never
   disagree.
   =========================================================================== */
import React, { useEffect, useRef, useState } from "react";
import { api, on } from "./api";
import { Icon, Toggle } from "./ui";

export function MCPControls({ onOpenSettings }) {
  const [st, setSt] = useState(null);          // MCPStatus
  const [tun, setTun] = useState(null);        // TunnelStatus
  const [wantPublic, setWantPublic] = useState(true);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false); // transient ✓ on the copy button
  const seeded = useRef(false);

  // Own the MCP + tunnel status so the controls stay live without the app
  // shell threading it through.
  useEffect(() => {
    api.getMCPStatus().then(setSt).catch(() => {});
    api.getTunnelStatus().then(setTun).catch(() => {});
    const offMcp = on("mcp:status", setSt);
    const offTun = on("tunnel:status", setTun);
    return () => { offMcp(); offTun(); };
  }, []);

  // Seed the public-URL intent once from persisted state: respect a prior
  // explicit opt-out (server on, tunnel off); otherwise default it on.
  useEffect(() => {
    if (seeded.current || !st || !tun) return;
    seeded.current = true;
    if (tun.enabled) setWantPublic(true);
    else if (st.enabled) setWantPublic(false);
  }, [st, tun]);

  async function toggleServer() {
    if (busy || !st) return;
    setBusy(true);
    try {
      const turningOn = !st.enabled;
      const s = await api.setMCPEnabled(turningOn);
      setSt(s);
      // Carry the public URL up/down with the server, honouring the remembered intent.
      if (turningOn && wantPublic && tun && !tun.enabled) setTun(await api.setTunnelEnabled(true));
      else if (!turningOn && tun && tun.enabled) setTun(await api.setTunnelEnabled(false));
    } finally { setBusy(false); }
  }

  async function copyURL() {
    if (!canCopy) return;
    try {
      if (window.runtime && window.runtime.ClipboardSetText) await window.runtime.ClipboardSetText(copyTarget);
      else await navigator.clipboard.writeText(copyTarget);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch { /* clipboard denied — nothing useful to surface from the titlebar */ }
  }

  const running = !!(st && st.running);
  const local = (st && st.endpoint) || "";
  const publicOn = !!(tun && tun.enabled);
  const publicUrl = (tun && tun.mcpUrl) || "";
  const usePublic = publicOn && !!publicUrl;            // public chosen *and* its URL is known
  const copyTarget = usePublic ? publicUrl : local;
  const canCopy = running && !!copyTarget;

  // The plug icon carries server state at a glance; the switch carries on/off.
  const iconColor = !st ? "var(--ink-faint)"
    : st.error ? "var(--s-4xx)"
    : running ? "var(--accent)"
    : "var(--ink-3)";
  const labelTip = !st ? "MCP server"
    : st.error ? `MCP server error — ${st.error}`
    : running ? `MCP server running${publicOn ? " · public URL on" : ""}`
    : st.enabled ? "MCP server starting…"
    : "MCP server off — switch on for LLM access";
  const copyTip = !running ? "Start the MCP server to copy its URL"
    : usePublic ? "Copy public URL"
    : publicOn ? "Copy local URL — public URL still starting"
    : "Copy local URL";

  return (
    <div className="tb-mcp tb-nodrag">
      <span className="tb-mcp-label" title={labelTip}>
        <Icon name="plug-zap" size={12} style={{ color: iconColor }} />
        MCP
      </span>
      <Toggle on={!!(st && st.enabled)} onChange={toggleServer} disabled={busy || !st} />
      <button className="iconbtn tb-mcp-btn" title={copyTip} aria-label={copyTip}
        onClick={copyURL} disabled={!canCopy}>
        <Icon name={copied ? "check" : "copy"} size={14} style={copied ? { color: "var(--sev-ok)" } : undefined} />
      </button>
      <button className="iconbtn tb-mcp-btn" title="MCP settings" aria-label="MCP settings"
        onClick={onOpenSettings}>
        <Icon name="settings" size={14} />
      </button>
    </div>
  );
}
