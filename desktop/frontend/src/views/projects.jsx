/* ===========================================================================
   Projects — opt-in competitor study: a main domain + competitor sites.
   A site's crawl history is resolved live from the registry; nothing here is
   stored against a crawl. This whole view is removable with the feature.
   =========================================================================== */
import React, { useEffect, useMemo, useState } from "react";
import { Icon, Btn, IconBtn, BrandMark, Empty, Modal, Seg, Toggle } from "../ui";
import { projectApi, hostOf } from "../api";

const fmtDate = (v) => {
  if (!v) return "—";
  const d = typeof v === "number" ? new Date(v * 1000) : new Date(v);
  return isNaN(d) ? "—" : d.toISOString().slice(0, 10);
};
const ageDays = (unix) => (unix ? Math.floor((Date.now() - unix * 1000) / 86400000) : null);

export function ProjectsView({ onCrawlSite }) {
  const [projects, setProjects] = useState([]);
  const [loaded, setLoaded] = useState(false);
  const [selId, setSelId] = useState(null);
  const [creating, setCreating] = useState(false);

  const load = () => projectApi.list().then((ps) => { setProjects(ps || []); setLoaded(true); }).catch(() => setLoaded(true));
  useEffect(() => { load(); }, []);

  const sel = projects.find((p) => p.id === selId) || null;

  if (sel) {
    return <ProjectDetail project={sel} onBack={() => { setSelId(null); load(); }}
      onCrawlSite={onCrawlSite}
      onDeleted={() => { setSelId(null); load(); }}
      onRenamed={load} />;
  }

  return (
    <div className="main">
      <div className="toolbar">
        <Icon name="folder" size={17} />
        <span className="title">Projects</span>
        <span className="pill mono">{projects.length}</span>
        <div style={{ flex: 1 }} />
        <Btn icon="plus" variant="primary" onClick={() => setCreating(true)}>New Project</Btn>
      </div>
      <div className="scroll" style={{ padding: 22 }}>
        <div style={{ maxWidth: 1080, margin: "0 auto" }}>
          {loaded && projects.length === 0 && (
            <Empty icon="folder" title="No projects yet" action={<Btn icon="plus" variant="primary" onClick={() => setCreating(true)}>New Project</Btn>}>
              A project groups a main domain with its competitors so you can benchmark them and watch how each site changes over time. Crawls you already have are picked up automatically.
            </Empty>
          )}
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))", gap: 14 }}>
            {projects.map((p) => (
              <div key={p.id} className="card" style={{ padding: 16, cursor: "pointer", display: "flex", gap: 12, alignItems: "center" }} onClick={() => setSelId(p.id)}>
                <BrandMark seed={"https://" + p.main_domain} size={34} />
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontWeight: 650, fontSize: 13.5, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{p.name}</div>
                  <div className="mono" style={{ fontSize: 11.5, color: "var(--ink-faint)", marginTop: 2 }}>{p.main_domain}</div>
                </div>
                <div style={{ flex: 1 }} />
                <Icon name="chevron-right" size={16} style={{ color: "var(--ink-faint)" }} />
              </div>
            ))}
          </div>
        </div>
      </div>
      {creating && <CreateProjectModal onClose={() => setCreating(false)} onCreated={(p) => { setCreating(false); load(); setSelId(p.id); }} />}
    </div>
  );
}

function CreateProjectModal({ onClose, onCreated }) {
  const [domain, setDomain] = useState("");
  const [name, setName] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  async function go() {
    if (!domain.trim()) return;
    setBusy(true); setErr("");
    try { onCreated(await projectApi.create(name.trim(), domain.trim())); }
    catch (e) { setErr(String(e)); setBusy(false); }
  }
  return <Modal icon="folder-plus" title="New project" onClose={onClose}
    body={<div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div>
        <label style={{ fontSize: 12, fontWeight: 600 }}>Main domain</label>
        <input className="input" autoFocus value={domain} placeholder="example.com"
          onChange={(e) => setDomain(e.target.value)} onKeyDown={(e) => e.key === "Enter" && go()}
          style={{ width: "100%", marginTop: 4 }} />
        <div className="hint">Your site. Competitors are added next. Stored as the exact host — example.com, www.example.com and a.example.com are different sites.</div>
      </div>
      <div>
        <label style={{ fontSize: 12, fontWeight: 600 }}>Name <span style={{ color: "var(--ink-faint)", fontWeight: 400 }}>(optional)</span></label>
        <input className="input" value={name} placeholder={domain ? domain + "'s Project" : "<domain>'s Project"}
          onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && go()}
          style={{ width: "100%", marginTop: 4 }} />
      </div>
      {err && <div style={{ color: "var(--s-4xx)", fontSize: 12 }}>{err}</div>}
    </div>}
    actions={<>
      <Btn onClick={onClose} disabled={busy}>Cancel</Btn>
      <Btn variant="primary" icon="check" onClick={go} disabled={busy || !domain.trim()}>Create</Btn>
    </>} />;
}

function ProjectDetail({ project, onBack, onCrawlSite, onDeleted, onRenamed }) {
  const [tab, setTab] = useState("overview");
  const [confirmDel, setConfirmDel] = useState(false);
  const [renaming, setRenaming] = useState(false);

  return (
    <div className="main">
      <div className="toolbar">
        <IconBtn icon="arrow-left" title="All projects" onClick={onBack} />
        <BrandMark seed={"https://" + project.main_domain} size={24} />
        <span className="title" style={{ marginLeft: 2 }}>{project.name}</span>
        <div style={{ flex: 1 }} />
        <Seg value={tab} onChange={setTab} options={[{ value: "overview", label: "Overview" }, { value: "comparison", label: "Comparison" }]} />
        <IconBtn icon="pencil" title="Rename" onClick={() => setRenaming(true)} />
        <IconBtn icon="trash-2" title="Delete project" onClick={() => setConfirmDel(true)} />
      </div>
      {tab === "overview"
        ? <Overview project={project} onCrawlSite={onCrawlSite} />
        : <Comparison project={project} />}
      {confirmDel && <Modal icon="trash-2" danger title="Delete project?" onClose={() => setConfirmDel(false)}
        body={<>This removes the project <b>{project.name}</b> and its competitor list. Your crawls are not deleted.</>}
        actions={<>
          <Btn onClick={() => setConfirmDel(false)}>Cancel</Btn>
          <Btn variant="primary" icon="trash-2" onClick={async () => { await projectApi.remove(project.id); onDeleted(); }}>Delete</Btn>
        </>} />}
      {renaming && <RenameModal project={project} onClose={() => setRenaming(false)} onDone={() => { setRenaming(false); onRenamed(); }} />}
    </div>
  );
}

function RenameModal({ project, onClose, onDone }) {
  const [name, setName] = useState(project.name);
  return <Modal icon="pencil" title="Rename project" onClose={onClose}
    body={<input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)}
      onKeyDown={(e) => e.key === "Enter" && name.trim() && projectApi.rename(project.id, name.trim()).then(onDone)}
      style={{ width: "100%" }} />}
    actions={<>
      <Btn onClick={onClose}>Cancel</Btn>
      <Btn variant="primary" icon="check" disabled={!name.trim()} onClick={() => projectApi.rename(project.id, name.trim()).then(onDone)}>Save</Btn>
    </>} />;
}

const reasonLabel = {
  "list crawl": "list audit",
  "path-scoped seed": "path crawl",
  "running": "running",
  "unfinished": "unfinished",
  "scope-narrowed": "section-scoped",
};

function Overview({ project, onCrawlSite }) {
  const [sites, setSites] = useState([]);
  const [busy, setBusy] = useState(true);
  const [adding, setAdding] = useState("");

  const load = () => { setBusy(true); projectApi.sites(project.id).then((s) => { setSites(s || []); setBusy(false); }).catch(() => setBusy(false)); };
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [project.id]);

  async function add() {
    const d = adding.trim();
    if (!d) return;
    await projectApi.addCompetitor(project.id, d);
    setAdding("");
    load();
  }

  return (
    <div className="scroll" style={{ padding: 22 }}>
      <div style={{ maxWidth: 1000, margin: "0 auto" }}>
        <div className="sb-sectlabel" style={{ padding: "0 0 8px" }}>Sites</div>
        <div className="card" style={{ overflow: "hidden" }}>
          {busy && <div style={{ padding: 22, textAlign: "center", color: "var(--ink-faint)", fontSize: 12.5 }}>Loading…</div>}
          {!busy && sites.map((s) => {
            const comparable = s.crawls.filter((c) => c.comparable);
            const latest = comparable[0];
            const others = s.crawls.filter((c) => !c.comparable);
            return (
              <div key={s.domain} style={{ display: "flex", alignItems: "center", gap: 13, padding: "13px 16px", borderTop: "1px solid var(--border-soft)" }}>
                <BrandMark seed={"https://" + s.domain} size={30} />
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span className="mono" style={{ fontWeight: 600, fontSize: 12.5 }}>{s.domain}</span>
                    {s.role === "main" && <span className="pill" style={{ fontSize: 10, height: 18, color: "var(--accent)", borderColor: "var(--accent)" }}>main</span>}
                  </div>
                  <div style={{ fontSize: 11.5, color: "var(--ink-faint)", marginTop: 3, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
                    {latest
                      ? <span style={{ color: "var(--sev-ok)" }}><Icon name="circle-check" size={11} style={{ verticalAlign: "-1px" }} /> latest crawl {fmtDate(latest.started)}</span>
                      : <span><Icon name="circle-dashed" size={11} style={{ verticalAlign: "-1px" }} /> no comparable crawl yet</span>}
                    {others.map((c) => (
                      <span key={c.id} title={c.seed} style={{ color: "var(--ink-3)", textDecoration: "none" }}>
                        · <span style={{ opacity: 0.7 }}>{reasonLabel[c.reason] || c.reason}</span>
                      </span>
                    ))}
                  </div>
                </div>
                {onCrawlSite && <Btn size="sm" icon="radar" onClick={() => onCrawlSite(s.domain)}>Crawl</Btn>}
                {s.role !== "main" && <IconBtn icon="x" title="Remove competitor" onClick={async () => { await projectApi.removeCompetitor(project.id, s.domain); load(); }} />}
              </div>
            );
          })}
          <div style={{ display: "flex", gap: 8, padding: "13px 16px", borderTop: "1px solid var(--border-soft)" }}>
            <input className="input" value={adding} placeholder="add competitor — e.g. rival.com"
              onChange={(e) => setAdding(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()}
              style={{ flex: 1 }} />
            <Btn icon="plus" onClick={add} disabled={!adding.trim()}>Add competitor</Btn>
          </div>
        </div>

        <div className="hint" style={{ marginTop: 14, display: "flex", gap: 7, alignItems: "flex-start" }}>
          <Icon name="info" size={13} style={{ marginTop: 1, flex: "0 0 13px" }} />
          <span>Each site is crawled with the default config — change a site's settings from its own crawl, not here. Scheduled re-crawls are coming later.</span>
        </div>
      </div>
    </div>
  );
}

function badge(label, tone) {
  const c = tone === "warn" ? "var(--sev-warn)" : tone === "accent" ? "var(--accent)" : "var(--ink-3)";
  return <span key={label} className="pill" style={{ height: 18, fontSize: 10, color: c, borderColor: "color-mix(in oklab, " + c + " 45%, var(--border))" }}>{label}</span>;
}

function configBadges(r) {
  const out = [];
  out.push(badge(r.rendering === "javascript" ? "JS render" : "text", r.rendering === "javascript" ? "accent" : null));
  if (r.max_depth != null && r.max_depth !== -1) out.push(badge("depth " + r.max_depth, "warn"));
  if (r.max_urls && r.max_urls < 5000000) out.push(badge("url cap", "warn"));
  if (r.robots_mode && r.robots_mode !== "respect") out.push(badge("robots:" + r.robots_mode, "warn"));
  if (r.scoped) out.push(badge("excludes", null));
  return out;
}

function Comparison({ project }) {
  const [card, setCard] = useState(null);
  const [busy, setBusy] = useState(true);
  const [optional, setOptional] = useState(false);
  const [err, setErr] = useState("");
  const [diff, setDiff] = useState(null); // { domain }

  useEffect(() => {
    setBusy(true); setErr("");
    projectApi.comparison(project.id, optional)
      .then((c) => { setCard(c); setBusy(false); })
      .catch((e) => { setErr(String(e)); setBusy(false); });
  }, [project.id, optional]);

  const main = useMemo(() => (card && card.sites || []).find((s) => s.role === "main" && s.status === "ok"), [card]);
  const freshness = useMemo(() => {
    const ts = (card && card.sites || []).filter((s) => s.status === "ok").map((s) => s.started);
    if (ts.length < 2) return 0;
    return Math.round((Math.max(...ts) - Math.min(...ts)) / 86400);
  }, [card]);

  if (busy) return <div className="scroll" style={{ padding: 30, color: "var(--ink-faint)", fontSize: 13 }}>Computing scorecard…</div>;
  if (err) return <div className="scroll" style={{ padding: 30, color: "var(--s-4xx)", fontSize: 13 }}>{err}</div>;
  if (!card || !card.sites || card.sites.length === 0) return <Empty icon="bar-chart-3" title="Nothing to compare">Add competitor domains in Overview, then crawl them.</Empty>;

  const delta = (val, base, goodHigh) => {
    if (base == null || val == null) return null;
    const d = val - base;
    if (Math.abs(d) < 1e-9) return null;
    const good = goodHigh ? d > 0 : d < 0;
    return <span style={{ color: good ? "var(--sev-ok)" : "var(--s-4xx)", fontSize: 10.5, marginLeft: 4 }}>{d > 0 ? "+" : ""}{Math.round(d * 10) / 10}</span>;
  };

  return (
    <div className="scroll" style={{ padding: 22 }}>
      <div style={{ maxWidth: 1100, margin: "0 auto" }}>
        {card.config_diverges && (
          <div className="card" style={{ padding: "11px 14px", marginBottom: 14, display: "flex", gap: 9, alignItems: "center", borderColor: "color-mix(in oklab, var(--sev-warn) 40%, var(--border))", background: "color-mix(in oklab, var(--sev-warn) 6%, var(--surface))" }}>
            <Icon name="triangle-alert" size={15} style={{ color: "var(--sev-warn)", flex: "0 0 15px" }} />
            <span style={{ fontSize: 12.5, color: "var(--ink-2)" }}>
              Sites were crawled with different settings ({(card.diverging_dims || []).join(", ")}) — metrics aren't fully apples-to-apples. Re-crawl each site for a fair comparison.
            </span>
          </div>
        )}
        {freshness >= 7 && (
          <div className="hint" style={{ marginBottom: 12, display: "flex", gap: 7, alignItems: "center" }}>
            <Icon name="clock" size={13} /> These crawls span {freshness} days — refresh older sites for a fairer snapshot.
          </div>
        )}

        <div style={{ display: "flex", alignItems: "center", marginBottom: 10 }}>
          <div className="sb-sectlabel" style={{ padding: 0 }}>Scorecard · latest comparable crawl per site</div>
          <div style={{ flex: 1 }} />
          <span style={{ fontSize: 12, color: "var(--ink-2)", display: "flex", alignItems: "center", gap: 8 }}>
            <Toggle on={optional} onChange={setOptional} /> content & schema metrics
          </span>
        </div>

        <div className="card" style={{ overflowX: "auto" }}>
          <table className="cmp-table" style={{ width: "100%", borderCollapse: "collapse", fontSize: 12.5 }}>
            <thead>
              <tr style={{ textAlign: "left", color: "var(--ink-faint)", fontSize: 11 }}>
                <th style={th}>Site</th>
                <th style={th}>When</th>
                <th style={thR}>URLs</th>
                <th style={thR}>Index%</th>
                <th style={thR}>Err</th>
                <th style={thR}>Warn</th>
                <th style={thR}>Opp</th>
                <th style={thR}>Link</th>
                {optional && <th style={thR}>Words</th>}
                {optional && <th style={thR}>Schema%</th>}
                <th style={th}>Config</th>
              </tr>
            </thead>
            <tbody>
              {card.sites.map((s) => {
                const isMain = s.role === "main";
                if (s.status !== "ok") {
                  return (
                    <tr key={s.domain} style={{ borderTop: "1px solid var(--border-soft)", background: isMain ? "var(--surface-2)" : undefined }}>
                      <td style={td}><SiteCell s={s} isMain={isMain} /></td>
                      <td style={td} colSpan={optional ? 10 : 8}>
                        <span style={{ color: "var(--ink-faint)", fontStyle: "italic" }}>no comparable crawl — crawl this site</span>
                      </td>
                    </tr>
                  );
                }
                return (
                  <tr key={s.domain} style={{ borderTop: "1px solid var(--border-soft)", background: isMain ? "var(--surface-2)" : undefined, cursor: "pointer" }}
                    onClick={() => setDiff({ domain: s.domain })} title="Click for over-time changes">
                    <td style={td}><SiteCell s={s} isMain={isMain} /></td>
                    <td style={{ ...td, color: "var(--ink-2)" }}>{fmtDate(s.started)}<span style={{ color: "var(--ink-faint)" }}> · {ageDays(s.started)}d</span></td>
                    <td style={tdR}>{(s.urls || 0).toLocaleString()}</td>
                    <td style={tdR}>{Math.round((s.indexable_rate || 0) * 100)}%{!isMain && main && delta(Math.round(s.indexable_rate * 100), Math.round(main.indexable_rate * 100), true)}</td>
                    <td style={{ ...tdR, color: s.errors ? "var(--sev-issue)" : "var(--ink-3)" }}>{s.errors || 0}{!isMain && main && delta(s.errors, main.errors, false)}</td>
                    <td style={tdR}>{s.warnings || 0}</td>
                    <td style={tdR}>{s.opportunities || 0}</td>
                    <td style={tdR}>{(s.avg_link_score || 0).toFixed(0)}</td>
                    {optional && <td style={tdR}>{Math.round(s.avg_word_count || 0)}</td>}
                    {optional && <td style={tdR}>{Math.round((s.schema_coverage || 0) * 100)}%</td>}
                    <td style={td}><span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>{configBadges(s)}</span></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <div className="hint" style={{ marginTop: 10 }}>Click a row to see what changed on that site since its previous crawl.</div>
      </div>
      {diff && <DiffModal project={project} domain={diff.domain} onClose={() => setDiff(null)} />}
    </div>
  );
}

const th = { padding: "9px 12px", fontWeight: 600 };
const thR = { ...th, textAlign: "right" };
const td = { padding: "10px 12px", verticalAlign: "middle" };
const tdR = { ...td, textAlign: "right", fontFamily: "var(--font-mono)" };

function SiteCell({ s, isMain }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 9 }}>
      <BrandMark seed={"https://" + s.domain} size={22} />
      <span className="mono" style={{ fontWeight: isMain ? 650 : 500 }}>{s.domain}</span>
      {isMain && <span style={{ fontSize: 10, color: "var(--accent)" }}>main</span>}
    </span>
  );
}

function DiffModal({ project, domain, onClose }) {
  const [state, setState] = useState({ loading: true });
  useEffect(() => {
    projectApi.diff(project.id, domain)
      .then((d) => setState({ loading: false, data: d }))
      .catch((e) => setState({ loading: false, err: String(e) }));
  }, [project.id, domain]);

  let body;
  if (state.loading) body = <span style={{ color: "var(--ink-faint)" }}>Comparing the two latest crawls…</span>;
  else if (state.err) body = <span style={{ color: "var(--s-4xx)" }}>{state.err}</span>;
  else if (!state.data || !state.data.ok) body = <span>This site needs at least two comparable crawls to show changes. Crawl it again to start a history.</span>;
  else {
    const r = state.data.result || {};
    let appeared = 0, resolved = 0;
    (r.issue_deltas || []).forEach((d) => {
      appeared += (d.new || []).length + (d.added || []).length;
      resolved += (d.removed || []).length + (d.missing || []).length;
    });
    body = (
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <div style={{ fontSize: 12.5, color: "var(--ink-2)" }}>Latest crawl vs the previous one.</div>
        <div style={{ display: "flex", gap: 20 }}>
          <Stat n={(r.new_pages || []).length} label="pages added" c="var(--sev-ok)" />
          <Stat n={(r.missing_pages || []).length} label="pages gone" c="var(--s-4xx)" />
          <Stat n={resolved} label="issues fixed" c="var(--sev-ok)" />
          <Stat n={appeared} label="issues appeared" c="var(--sev-warn)" />
        </div>
        <div className="hint">{r.pages_previous} → {r.pages_current} pages crawled.</div>
      </div>
    );
  }
  return <Modal icon="git-compare" title={domain + " — over time"} onClose={onClose} body={body}
    actions={<Btn variant="primary" onClick={onClose}>Close</Btn>} />;
}

function Stat({ n, label, c }) {
  return <div>
    <div style={{ fontSize: 22, fontWeight: 650, color: c, fontFamily: "var(--font-mono)" }}>{n}</div>
    <div style={{ fontSize: 11, color: "var(--ink-faint)" }}>{label}</div>
  </div>;
}
