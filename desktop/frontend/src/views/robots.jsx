/* ===========================================================================
   robots.txt Tester — a Tools sub-view over the sitecheck engine. The editor
   + verdicts layout is the original tester; matching, file-level audit and
   findings all come from the one backend derivation (ToolsApp.RunTool), so
   this view, the CLI tester and a crawl's site checks can never disagree.
   =========================================================================== */
import React, { useEffect, useState } from "react";
import { Icon, Btn, Field, CopyButton } from "../ui";
import { toolsApi } from "../api";
import { ToolShell, FindingsStrip, ToolError } from "./tools";

const DEFAULT_ROBOTS = `User-agent: *
Disallow: /admin
Disallow: /checkout
Allow: /admin/public
`;

export function RobotsTool({ meta, onBack, prefill }) {
  const [robots, setRobots] = useState(DEFAULT_ROBOTS);
  const [token, setToken] = useState("bluesnake");
  const [urls, setUrls] = useState("/\n/admin\n/admin/public\n/checkout");
  const [site, setSite] = useState(prefill || "https://");
  const [res, setRes] = useState(null); // {report, findings} for the edited body
  const [err, setErr] = useState("");

  // Live re-evaluation while editing: the inline-body path never fetches.
  useEffect(() => {
    const t = setTimeout(() => {
      const list = urls.split("\n").map((s) => s.trim()).filter(Boolean);
      toolsApi.run("robots", "", { robots_txt: robots, urls: list, user_agent: token })
        .then(setRes)
        .catch(() => setRes(null));
    }, 150);
    return () => clearTimeout(t);
  }, [robots, token, urls]);

  // Load live fetches with the crawler's exact semantics (5-hop Google REP)
  // and drops the body into the editor; the debounced run above re-audits it.
  async function loadLive() {
    setErr("");
    try {
      const r = await toolsApi.run("robots", site);
      const rep = r.report;
      if (!rep.found) {
        setErr(rep.fetch_error ? `unreachable: ${rep.fetch_error}` : `robots.txt returned HTTP ${rep.status}`);
        return;
      }
      setRobots(rep.body || "");
    } catch (e) {
      setErr(String(e && e.message ? e.message : e));
    }
  }

  const rep = res && res.report;
  const results = (rep && rep.verdicts) || [];
  const blockedN = results.filter((r) => !r.allowed).length;

  return (
    <ToolShell meta={meta} onBack={onBack}>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16 }}>

        {/* left: robots source */}
        <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <Field label="Robots user-agent token" hint="The name matched against User-agent groups (kept separate from the HTTP user-agent).">
            <input className="input mono" value={token} onChange={(e) => setToken(e.target.value)} style={{ width: 220 }} />
          </Field>
          <div className="field">
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <label style={{ fontSize: 11.5, fontWeight: 600, color: "var(--ink-2)" }}>robots.txt</label>
              <div style={{ flex: 1 }} />
              <input className="input mono" value={site} onChange={(e) => setSite(e.target.value)} placeholder="https://example.com" style={{ width: 200, height: 26, fontSize: 11 }} />
              <Btn size="sm" variant="ghost" icon="download" onClick={loadLive}>Load live</Btn>
            </div>
            <textarea className="input mono" value={robots} onChange={(e) => setRobots(e.target.value)} style={{ height: 300, padding: 12, lineHeight: 1.65, fontSize: 11.5, resize: "vertical" }} spellCheck={false} />
            {err && <ToolError err={err} />}
            {/* file health, from the same audit the crawl stores */}
            {rep && (
              <div style={{ display: "flex", gap: 12, flexWrap: "wrap", fontSize: 11, color: "var(--ink-faint)" }} className="mono">
                <span>{rep.size_bytes} bytes</span>
                <span>{rep.groups} groups · {rep.rules} rules</span>
                <span>{(rep.sitemaps || []).length} sitemap directives</span>
                {(rep.ignored_lines || []).length > 0 && (
                  <span style={{ color: "var(--sev-warn)" }}>{rep.ignored_lines.length} invalid lines ({rep.ignored_lines.map((l) => l.line).join(", ")})</span>
                )}
                {rep.blocks_all && <span style={{ color: "var(--s-4xx)" }}>blocks all crawlers ({rep.blocks_all_rule})</span>}
              </div>
            )}
          </div>
        </div>

        {/* right: test urls + results */}
        <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <Field label="Test URLs" hint="One per line. Paths or full URLs.">
            <textarea className="input mono" value={urls} onChange={(e) => setUrls(e.target.value)} style={{ height: 120, padding: 12, lineHeight: 1.7, fontSize: 11.5, resize: "vertical" }} spellCheck={false} />
          </Field>
          <div className="card" style={{ overflow: "hidden" }}>
            <div style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-soft)", display: "flex", alignItems: "center", gap: 9 }}>
              <span style={{ fontSize: 12.5, fontWeight: 650 }}>Verdicts</span>
              <div style={{ flex: 1 }} />
              <span style={{ fontSize: 11.5, color: "var(--sev-ok)", fontWeight: 600 }}>{results.length - blockedN} allowed</span>
              <span style={{ fontSize: 11.5, color: "var(--s-4xx)", fontWeight: 600 }}>{blockedN} blocked</span>
            </div>
            {results.map((r, i) => (
              <div key={i} className="copyhost" style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-soft)", display: "flex", flexDirection: "column", gap: 4 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
                  <span className="badge tint" style={{ "--c": r.allowed ? "var(--sev-ok)" : "var(--s-4xx)" }}>
                    <Icon name={r.allowed ? "circle-check" : "ban"} size={11} />{r.allowed ? "ALLOWED" : "BLOCKED"}
                  </span>
                  <span className="mono" style={{ flex: 1, minWidth: 0, fontSize: 11.5, color: "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{r.url}</span>
                  <CopyButton text={r.url} title="Copy URL" />
                </div>
                {r.rule
                  ? <div className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)", paddingLeft: 4 }}>matched <span style={{ color: r.allowed ? "var(--sev-ok)" : "var(--s-4xx)" }}>{r.rule}</span> · line {r.line}</div>
                  : <div className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)", paddingLeft: 4 }}>no matching rule — allowed by default</div>}
              </div>
            ))}
          </div>
          {res && <FindingsStrip findings={res.findings} />}
          <div style={{ display: "flex", alignItems: "center", gap: 9, fontSize: 11.5, color: "var(--ink-faint)", padding: "0 2px" }}>
            <Icon name="lightbulb" size={14} />
            Pairs with <b style={{ color: "var(--ink-3)" }}>Settings → robots.txt</b> to control how crawls treat these rules (respect / ignore / ignore-but-report).
          </div>
        </div>

      </div>
    </ToolShell>
  );
}
