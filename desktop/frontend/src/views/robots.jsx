/* ===========================================================================
   robots.txt Tester — real Google-REP matching via the backend engine
   =========================================================================== */
import React, { useEffect, useState } from "react";
import { Icon, Btn, Field, CopyButton } from "../ui";
import { api } from "../api";

const DEFAULT_ROBOTS = `User-agent: *
Disallow: /admin
Disallow: /checkout
Allow: /admin/public
`;

export function RobotsTester() {
  const [robots, setRobots] = useState(DEFAULT_ROBOTS);
  const [token, setToken] = useState("bluesnake");
  const [urls, setUrls] = useState("/\n/admin\n/admin/public\n/checkout");
  const [site, setSite] = useState("https://");
  const [results, setResults] = useState([]);
  const [err, setErr] = useState("");

  useEffect(() => {
    const t = setTimeout(() => {
      const list = urls.split("\n").map((s) => s.trim()).filter(Boolean);
      if (!list.length) { setResults([]); return; }
      api.testRobots(robots, token, list).then((r) => setResults(r || [])).catch(() => setResults([]));
    }, 150);
    return () => clearTimeout(t);
  }, [robots, token, urls]);

  async function loadLive() {
    setErr("");
    try {
      const text = await api.fetchRobots(site);
      setRobots(text);
    } catch (e) {
      setErr(String(e));
    }
  }

  const blockedN = results.filter((r) => !r.allowed).length;

  return (
    <div className="main">
      <div className="toolbar"><Icon name="bot" size={17} /><span className="title">robots.txt Tester</span><span className="sub">test what your crawler can reach — Google REP semantics</span></div>
      <div className="scroll" style={{ padding: 20 }}>
        <div style={{ maxWidth: 1000, margin: "0 auto", display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16 }} className="fade">

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
              {err && <div style={{ color: "var(--s-4xx)", fontSize: 11.5 }}>{err}</div>}
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
            <div style={{ display: "flex", alignItems: "center", gap: 9, fontSize: 11.5, color: "var(--ink-faint)", padding: "0 2px" }}>
              <Icon name="lightbulb" size={14} />
              Pairs with <b style={{ color: "var(--ink-3)" }}>Settings → robots.txt</b> to control how crawls treat these rules (respect / ignore / ignore-but-report).
            </div>
          </div>

        </div>
      </div>
    </div>
  );
}
