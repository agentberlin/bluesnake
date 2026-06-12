/* ===========================================================================
   First-run welcome — shown when there are no crawls yet.
   Startup-tool aesthetic: centered hero, launch pill, command-style input,
   text-only differentiators with mono tags. (Ported from the Claude Design
   handoff — chat2 "Bluesnake Empty State".)
   =========================================================================== */
import React, { useState } from "react";
import { Icon } from "../ui";

/* injected from package.json at build time (vite define); fall back for tests */
const APP_VERSION = typeof __APP_VERSION__ !== "undefined" ? __APP_VERSION__ : "0.1.0";

/* brand mark — blue square + snake glyph */
export function BrandMark({ size = 26, icon = 16 }) {
  return (
    <span style={{
      width: size, height: size, flex: `0 0 ${size}px`,
      display: "inline-flex", alignItems: "center", justifyContent: "center",
      background: "var(--primary)", color: "var(--primary-ink)",
    }}>
      <Icon name="worm" size={icon} />
    </span>
  );
}

const WELCOME_POINTS = [
  { tag: "no-jvm", body: "Opens instantly. Crawls half a million pages on a laptop — no Java, no memory tuning." },
  { tag: "plain-files", body: "Every crawl config is a readable file. Edit it anywhere, diff it, share it." },
  { tag: "agent-native", body: "One CLI, every control. Claude Code or Codex can run whole audits for you." },
  { tag: "mcp --public", body: "Built-in MCP server with a public HTTPS URL. No ngrok, no tunnels.", isNew: true },
];

export function Welcome({ onStart, onConfigure, onMcp }) {
  const [url, setUrl] = useState("");
  const start = () => {
    const u = url.trim();
    if (!u) { onConfigure(); return; } // nothing typed — open the full form instead
    onStart(u);
  };

  return (
    <div className="main">
      <div className="scroll welcome">
        <div className="wl-glow" />
        <div className="wl-col fade">

          {/* brand */}
          <div className="wl-brand">
            <BrandMark size={34} icon={20} />
            <span className="snake" aria-hidden="true"><i /><i /><i /><i /><i /><i /></span>
          </div>

          {/* launch pill — jumps to the MCP server settings it announces */}
          <button className="wl-pill" onClick={onMcp}>
            <span className="wl-pill-new">New</span>
            v{APP_VERSION} — built-in MCP server, public HTTPS URLs
            <Icon name="arrow-right" size={12} />
          </button>

          {/* hero */}
          <h1 className="wl-h1">Crawl fast.<br />Fix faster.</h1>
          <p className="wl-sub">
            Bluesnake maps every page, link and redirect on your site and turns
            it into fixes — in minutes, not meetings.
          </p>

          {/* command input */}
          <div className="wl-cmd">
            <span className="wl-cmd-prefix mono">bluesnake crawl</span>
            <input
              className="mono"
              placeholder="yourdomain.com"
              value={url}
              autoFocus
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && start()}
            />
            <button className="wl-go" onClick={start}>
              Crawl <Icon name="arrow-right" size={14} />
            </button>
          </div>
          <div className="wl-hints mono">
            runs locally &nbsp;·&nbsp; respects robots.txt &nbsp;·&nbsp; auto-saves everything
          </div>

          {/* differentiators */}
          <div className="wl-points">
            {WELCOME_POINTS.map((p) => (
              <div key={p.tag} className="wl-point">
                <span className="wl-tag mono">
                  {p.tag}
                  {p.isNew && <em>new</em>}
                </span>
                <span className="wl-body">{p.body}</span>
              </div>
            ))}
          </div>

          {/* fine print */}
          <div className="wl-fine mono">~/.bluesnake — nothing leaves your machine unless you say so</div>

        </div>
      </div>
    </div>
  );
}
