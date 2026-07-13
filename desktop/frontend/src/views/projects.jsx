/* ===========================================================================
   Projects — opt-in competitor study: a main domain + competitor sites.
   A site's crawl history is resolved live from the registry; nothing here is
   stored against a crawl. This whole view is removable with the feature.
   =========================================================================== */
import React, { useEffect, useMemo, useState } from "react";
import { Icon, Btn, IconBtn, BrandMark, Empty, Modal, Seg, Toggle, CopyButton, StatusBar, SevDot } from "../ui";
import { api, projectApi, hostOf, DEFAULT_PROFILE } from "../api";
import { CrawlSetupCard, defaultCrawlSetup, setupToRequest, useBaseKnobs } from "./newcrawl";
import { CompareResult } from "./compare";

const fmtDate = (v) => {
  if (!v) return "—";
  const d = typeof v === "number" ? new Date(v * 1000) : new Date(v);
  return isNaN(d) ? "—" : d.toISOString().slice(0, 10);
};
const ageDays = (unix) => (unix ? Math.floor((Date.now() - unix * 1000) / 86400000) : null);
// compact big numbers so tight card columns never overflow: 56,339 → "56.3k";
// exact values live in the tooltip
const fmtCompact = (n) => {
  const a = Math.abs(n), trim = (s) => s.replace(/\.0$/, "");
  if (a >= 1e6) return trim((n / 1e6).toFixed(1)) + "M";
  if (a >= 1e4) return trim((n / 1e3).toFixed(1)) + "k";
  return n.toLocaleString();
};

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
              <div key={p.id} className="card copyhost" style={{ padding: 16, cursor: "pointer", display: "flex", gap: 12, alignItems: "center" }} onClick={() => setSelId(p.id)}>
                <BrandMark seed={"https://" + p.main_domain} size={34} />
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontWeight: 650, fontSize: 13.5, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{p.name}</div>
                  <div className="mono" style={{ fontSize: 11.5, color: "var(--ink-faint)", marginTop: 2 }}>{p.main_domain}</div>
                </div>
                <div style={{ flex: 1 }} />
                <CopyButton text={p.main_domain} title="Copy domain" />
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
  const [crawlAllOpen, setCrawlAllOpen] = useState(false);

  return (
    <div className="main">
      <div className="toolbar">
        <IconBtn icon="arrow-left" title="All projects" onClick={onBack} />
        <BrandMark seed={"https://" + project.main_domain} size={24} />
        <span className="title" style={{ marginLeft: 2 }}>{project.name}</span>
        <div style={{ flex: 1 }} />
        <Btn icon="radar" onClick={() => setCrawlAllOpen(true)}
          title="Set up and queue a crawl of every member domain">
          Crawl all
        </Btn>
        <Seg value={tab} onChange={setTab} options={[{ value: "overview", label: "Overview" }, { value: "comparison", label: "Comparison" }]} />
        <IconBtn icon="pencil" title="Rename" onClick={() => setRenaming(true)} />
        <IconBtn icon="trash-2" title="Delete project" onClick={() => setConfirmDel(true)} />
      </div>
      {tab === "overview"
        ? <Overview project={project} onCrawlSite={onCrawlSite} />
        : <Comparison project={project} onCrawlSite={onCrawlSite} />}
      {crawlAllOpen && <CrawlAllModal project={project} onClose={() => setCrawlAllOpen(false)} />}
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

/* "Crawl all" has two modes (#88). Default: every site crawls with its own
   saved setup — the setup its domain last ran with, app settings when never
   crawled; nothing is stored per member, the resolution happens when each job
   is queued. Override: the shared setup card (a base + touched quick knobs)
   applied to every member — today's one-setup-for-the-batch journey. */
function CrawlAllModal({ project, onClose }) {
  const [mode, setMode] = useState("perSite");
  const [profiles, setProfiles] = useState([DEFAULT_PROFILE]);
  const [setup, setSetup] = useState({ ...defaultCrawlSetup(), source: "" });
  const [plan, setPlan] = useState(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.listProfiles().then((p) => p && p.length && setProfiles(p)).catch(() => {});
    projectApi.crawlAllPlan(project.id).then((rows) => setPlan(rows || [])).catch(() => setPlan([]));
  }, [project.id]);
  useBaseKnobs(setup, setSetup, null); // mirror the shared card's base

  const count = plan == null ? null : plan.length;
  const planBroken = mode === "perSite" && (plan || []).some((m) => m.error);

  async function go() {
    setBusy(true);
    setErr("");
    try {
      // per-site mode: each member resolves its own last-crawl setup at
      // enqueue; no quick knobs — switch to "one setup" to shape the batch
      const req = mode === "perSite" ? { configSource: "last", rate: -1 } : setupToRequest(setup);
      await projectApi.crawlAll(project.id, req);
      onClose();
      // the dispatcher starts the first crawl and crawl:started switches to its
      // live view; the rest run behind it (up to the parallel-crawl slots).
    } catch (e) {
      setErr(String(e));
      setBusy(false);
    }
  }

  return <Modal icon="radar" title={"Crawl all sites — " + project.name} onClose={onClose} width={640}
    body={<div>
      <div style={{ display: "flex", justifyContent: "center", marginBottom: 14 }}>
        <Seg value={mode} onChange={setMode} options={[
          { value: "perSite", label: "Each site's saved setup" },
          { value: "shared", label: "One setup for every site" },
        ]} />
      </div>
      {mode === "perSite" ? (
        <div>
          <div className="hint" style={{ marginBottom: 10 }}>
            Every site crawls with the setup it last ran with — resolved when its job is queued and frozen into the crawl. Sites never crawled before use the app settings.
          </div>
          <div className="card" style={{ overflow: "hidden" }}>
            {plan == null && <div style={{ padding: 16, textAlign: "center", color: "var(--ink-faint)", fontSize: 12.5 }}>Loading…</div>}
            {(plan || []).map((m) => (
              <div key={m.domain} style={{ display: "flex", alignItems: "center", gap: 10, padding: "10px 14px", borderTop: "1px solid var(--border-soft)", fontSize: 12 }}>
                <span className="mono" style={{ fontWeight: 600, flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>{m.domain}</span>
                {m.error
                  ? <span style={{ color: "var(--s-4xx)", display: "flex", alignItems: "center", gap: 6 }}><Icon name="circle-alert" size={13} />{m.error}</span>
                  : m.hasLast
                    ? <span style={{ color: "var(--ink-2)" }}>last crawl setup · {fmtDate(m.started)}</span>
                    : <span style={{ color: "var(--ink-faint)" }}>app settings · never crawled</span>}
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div>
          <div className="hint" style={{ marginBottom: 12 }}>
            One setup for every site in this project, frozen into each crawl when it's queued.
          </div>
          <CrawlSetupCard profiles={profiles} value={setup} onChange={setSetup} hint="frozen into each crawl" />
        </div>
      )}
      {err && <div style={{ marginTop: 10, display: "flex", alignItems: "center", gap: 8, color: "var(--s-4xx)", fontSize: 12.5, fontWeight: 500 }}><Icon name="circle-alert" size={15} />{err}</div>}
    </div>}
    actions={<>
      <Btn onClick={onClose} disabled={busy}>Cancel</Btn>
      <Btn variant="primary" icon="radar" onClick={go} disabled={busy || count === 0 || planBroken}>
        {busy ? "Queuing…" : "Queue " + (count == null ? "all" : count) + (count === 1 ? " crawl" : " crawls")}
      </Btn>
    </>} />;
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
              <div key={s.domain} className="copyhost" style={{ display: "flex", alignItems: "center", gap: 13, padding: "13px 16px", borderTop: "1px solid var(--border-soft)" }}>
                <BrandMark seed={"https://" + s.domain} size={30} />
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span className="mono" style={{ fontWeight: 600, fontSize: 12.5 }}>{s.domain}</span>
                    {s.role === "main" && <span className="pill" style={{ fontSize: 10, height: 18, color: "var(--accent)", borderColor: "var(--accent)" }}>main</span>}
                    <CopyButton text={s.domain} title="Copy domain" />
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
                {onCrawlSite && <Btn size="sm" icon="radar" onClick={() => onCrawlSite(s.domain)} title="Set up a crawl of this site — opens New Crawl prefilled">Crawl</Btn>}
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
          <span>"Crawl all" uses each site's saved setup by default — the setup its last crawl ran with — or one shared setup for the whole batch. A site's Crawl button opens New Crawl prefilled with its last setup preselected. Scheduled re-crawls are coming later.</span>
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

function Comparison({ project, onCrawlSite }) {
  const [card, setCard] = useState(null);
  const [busy, setBusy] = useState(true);
  const [optional, setOptional] = useState(false);
  const [err, setErr] = useState("");
  const [diff, setDiff] = useState(null); // { domain }
  const [pulses, setPulses] = useState({}); // domain -> {loading} | {data} | {err}

  useEffect(() => {
    setBusy(true); setErr("");
    projectApi.comparison(project.id, optional)
      .then((c) => { setCard(c); setBusy(false); })
      .catch((e) => { setErr(String(e)); setBusy(false); });
  }, [project.id, optional]);

  // Load every member's "since last crawl" pulse as soon as the scorecard is
  // up — sequentially, so a many-site project diffs one pair at a time. Each
  // pulse rides the comparison cache, so only never-compared pairs cost time.
  const okDomains = useMemo(
    () => ((card && card.sites) || []).filter((s) => s.status === "ok").map((s) => s.domain),
    [card],
  );
  const domainsKey = okDomains.join("|");
  useEffect(() => {
    if (!domainsKey) return;
    let alive = true;
    setPulses(Object.fromEntries(okDomains.map((d) => [d, { loading: true }])));
    (async () => {
      for (const domain of okDomains) {
        try {
          const data = await projectApi.memberPulse(project.id, domain);
          if (!alive) return;
          setPulses((m) => ({ ...m, [domain]: { data } }));
        } catch (e) {
          if (!alive) return;
          setPulses((m) => ({ ...m, [domain]: { err: String(e) } }));
        }
      }
    })();
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project.id, domainsKey]);

  const main = useMemo(() => (card && card.sites || []).find((s) => s.role === "main" && s.status === "ok"), [card]);
  const freshness = useMemo(() => {
    const ts = (card && card.sites || []).filter((s) => s.status === "ok").map((s) => s.started);
    if (ts.length < 2) return 0;
    return Math.round((Math.max(...ts) - Math.min(...ts)) / 86400);
  }, [card]);

  // best value per scorecard column among scored sites (only where "better"
  // is unambiguous); null when sites tie, so nothing is highlighted
  const best = useMemo(() => {
    const ok = ((card && card.sites) || []).filter((s) => s.status === "ok");
    if (ok.length < 2) return {};
    const pick = (get, high) => {
      const vals = ok.map(get);
      const lo = Math.min(...vals), hi = Math.max(...vals);
      if (lo === hi) return null;
      return high ? hi : lo;
    };
    return {
      idx: pick((s) => Math.round((s.indexable_rate || 0) * 100), true),
      err: pick((s) => s.errors || 0, false),
      warn: pick((s) => s.warnings || 0, false),
      opp: pick((s) => s.opportunities || 0, false),
      link: pick((s) => Math.round(s.avg_link_score || 0), true),
      words: pick((s) => Math.round(s.avg_word_count || 0), true),
      schema: pick((s) => Math.round((s.schema_coverage || 0) * 100), true),
    };
  }, [card]);
  const win = (key, v) => best[key] != null && v === best[key]
    ? { color: "var(--sev-ok)", fontWeight: 700 } : {};

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
              Sites were crawled with different setups ({(card.diverging_dims || []).join(", ")}) — often deliberate with per-site saved setups, but the metrics aren't fully apples-to-apples. For a strictly fair comparison, "Crawl all" with one setup for every site.
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
                <th style={{ ...th, minWidth: 90 }}>Responses</th>
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
                    <tr key={s.domain} className="copyhost" style={{ borderTop: "1px solid var(--border-soft)", background: isMain ? "var(--surface-2)" : undefined }}>
                      <td style={td}><SiteCell s={s} isMain={isMain} /></td>
                      <td style={td} colSpan={optional ? 11 : 9}>
                        <span style={{ color: "var(--ink-faint)", fontStyle: "italic" }}>no comparable crawl — crawl this site</span>
                      </td>
                    </tr>
                  );
                }
                const b = s.status_buckets || {};
                const mix = { "2xx": b[2] || 0, "3xx": b[3] || 0, "4xx": b[4] || 0, "5xx": b[5] || 0 };
                return (
                  <tr key={s.domain} className="copyhost" style={{ borderTop: "1px solid var(--border-soft)", background: isMain ? "var(--surface-2)" : undefined, cursor: "pointer" }}
                    onClick={() => setDiff({ domain: s.domain })} title="Click for over-time changes">
                    <td style={td}><SiteCell s={s} isMain={isMain} /></td>
                    <td style={{ ...td, color: "var(--ink-2)" }}>{fmtDate(s.started)}<span style={{ color: "var(--ink-faint)" }}> · {ageDays(s.started)}d</span></td>
                    <td style={tdR}>{(s.urls || 0).toLocaleString()}</td>
                    <td style={td} title={Object.entries(mix).filter(([, n]) => n).map(([k, n]) => `${k}: ${n.toLocaleString()}`).join(" · ")}>
                      <StatusBar status={mix} height={7} radius={3} />
                    </td>
                    <td style={{ ...tdR, ...win("idx", Math.round((s.indexable_rate || 0) * 100)) }}>{Math.round((s.indexable_rate || 0) * 100)}%{!isMain && main && delta(Math.round(s.indexable_rate * 100), Math.round(main.indexable_rate * 100), true)}</td>
                    <td style={{ ...tdR, color: s.errors ? "var(--sev-issue)" : "var(--ink-3)", ...win("err", s.errors || 0) }}>{s.errors || 0}{!isMain && main && delta(s.errors, main.errors, false)}</td>
                    <td style={{ ...tdR, ...win("warn", s.warnings || 0) }}>{s.warnings || 0}</td>
                    <td style={{ ...tdR, ...win("opp", s.opportunities || 0) }}>{s.opportunities || 0}</td>
                    <td style={{ ...tdR, ...win("link", Math.round(s.avg_link_score || 0)) }}>{(s.avg_link_score || 0).toFixed(0)}</td>
                    {optional && <td style={{ ...tdR, ...win("words", Math.round(s.avg_word_count || 0)) }}>{Math.round(s.avg_word_count || 0)}</td>}
                    {optional && <td style={{ ...tdR, ...win("schema", Math.round((s.schema_coverage || 0) * 100)) }}>{Math.round((s.schema_coverage || 0) * 100)}%</td>}
                    <td style={td}><span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>{configBadges(s)}</span></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <div className="hint" style={{ marginTop: 10, display: "flex", alignItems: "center", gap: 6 }}>
          <Icon name="trophy" size={12} style={{ color: "var(--sev-ok)" }} /> best value per column ·
          click a row for that site's full diff since its previous crawl
        </div>

        <div className="sb-sectlabel" style={{ padding: "18px 0 8px" }}>Momentum · what changed since each site's previous crawl</div>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(340px, 1fr))", gap: 12 }}>
          {card.sites.map((s) => (
            <PulseCard key={s.domain} site={s} pulse={pulses[s.domain]}
              onDetails={() => setDiff({ domain: s.domain })}
              onCrawl={onCrawlSite ? () => onCrawlSite(s.domain) : null} />
          ))}
        </div>
      </div>
      {diff && <DiffModal domain={diff.domain} pulse={pulses[diff.domain]} onClose={() => setDiff(null)} />}
    </div>
  );
}

/* ---- one member's momentum card ----------------------------------------- */
function PulseCard({ site, pulse, onDetails, onCrawl }) {
  const isMain = site.role === "main";
  const data = pulse && pulse.data;
  // direction lives in the label + tint, never in a sign: "8,895 pages
  // removed", not "-8,895 pages gone"
  const chip = (label, value, color) => (
    <span key={label} className="badge tint" style={{ "--c": color }}>
      <span className="mono" style={{ fontWeight: 650 }}>{value.toLocaleString()}</span> {label}
    </span>
  );

  let body;
  if (site.status !== "ok") {
    body = <PulseNote icon="circle-dashed" text="No comparable crawl yet.">{onCrawl && <Btn size="sm" icon="radar" onClick={onCrawl}>Crawl</Btn>}</PulseNote>;
  } else if (!pulse || pulse.loading) {
    body = <PulseNote icon="loader" spin text="Diffing the two latest crawls — instant once cached…" />;
  } else if (pulse.err) {
    body = <PulseNote icon="circle-alert" text={pulse.err} color="var(--s-4xx)" />;
  } else if (!data.ok) {
    body = <PulseNote icon="history" text="Only one comparable crawl — crawl again to start tracking changes.">{onCrawl && <Btn size="sm" icon="radar" onClick={onCrawl}>Crawl</Btn>}</PulseNote>;
  } else {
    const pagesNet = data.pages_curr - data.pages_prev;
    const quiet = !data.new_pages && !data.removed_pages && !data.status_flips && !data.indexability_flips && !data.element_changes && !data.issues_appeared && !data.issues_resolved;
    body = (
      <>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
          {quiet && <span className="badge tint" style={{ "--c": "var(--sev-ok)" }}><Icon name="circle-check" size={11} />no changes between the runs</span>}
          {data.new_pages > 0 && chip("pages added", data.new_pages, "var(--sev-ok)")}
          {data.removed_pages > 0 && chip("pages removed", data.removed_pages, "var(--s-4xx)")}
          {data.issues_appeared > 0 && chip("issues appeared", data.issues_appeared, "var(--s-4xx)")}
          {data.issues_resolved > 0 && chip("issues resolved", data.issues_resolved, "var(--sev-ok)")}
          {data.status_flips > 0 && chip("status flips", data.status_flips, "var(--s-5xx)")}
          {data.indexability_flips > 0 && chip("indexability flips", data.indexability_flips, "var(--s-3xx)")}
          {data.element_changes > 0 && chip("content edits", data.element_changes, "var(--sev-warn)")}
        </div>

        {(data.trend || []).length >= 2 && (
          <div style={{ display: "grid", gridTemplateColumns: "repeat(3, minmax(0, 1fr))", gap: 12, marginTop: 12 }}>
            <SparkStat label="URLs" points={(data.trend || []).map((t) => t.urls)} color="var(--accent)" />
            <SparkStat label="Issues" points={(data.trend || []).map((t) => t.issues)} color="var(--sev-issue)" invert />
            <SparkStat label="Warnings" points={(data.trend || []).map((t) => t.warnings)} color="var(--sev-warn)" invert />
          </div>
        )}

        {(data.top_moves || []).length > 0 && (
          <div style={{ marginTop: 12, display: "flex", flexDirection: "column", gap: 5 }}>
            {(data.top_moves || []).map((m) => (
              <div key={m.name} style={{ display: "flex", alignItems: "center", gap: 7, fontSize: 11.5 }}>
                <SevDot severity={m.severity} />
                <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", color: "var(--ink-2)" }}>{m.name}</span>
                <span className="mono" style={{ fontWeight: 650, color: m.net > 0 ? "var(--s-4xx)" : "var(--sev-ok)" }}>{m.net > 0 ? "+" : ""}{m.net.toLocaleString()}</span>
              </div>
            ))}
          </div>
        )}

        <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "4px 8px", marginTop: 12 }}>
          <span className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)", whiteSpace: "nowrap" }}>
            {(data.pages_prev || 0).toLocaleString()} → {(data.pages_curr || 0).toLocaleString()} pages
            {pagesNet !== 0 && <span style={{ color: pagesNet > 0 ? "var(--sev-ok)" : "var(--s-4xx)" }}> ({pagesNet > 0 ? "+" : ""}{pagesNet.toLocaleString()})</span>}
          </span>
          <div style={{ flex: 1 }} />
          <Btn size="sm" variant="ghost" icon="arrow-right" onClick={onDetails}>Full diff</Btn>
        </div>
      </>
    );
  }

  return (
    <div className="card" style={{ padding: 14 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 9, marginBottom: 10 }}>
        <BrandMark seed={"https://" + site.domain} size={22} />
        <span className="mono" style={{ fontWeight: isMain ? 650 : 500, fontSize: 12, flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{site.domain}</span>
        {isMain && <span style={{ fontSize: 10, color: "var(--accent)" }}>main</span>}
        {data && data.ok && (
          <span className="mono" style={{ fontSize: 10.5, color: "var(--ink-faint)", whiteSpace: "nowrap" }}>
            {fmtDate(data.prev_started)} → {fmtDate(data.curr_started)}
          </span>
        )}
      </div>
      {body}
    </div>
  );
}

function PulseNote({ icon, text, color, spin, children }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12, color: color || "var(--ink-faint)", minHeight: 34 }}>
      <Icon name={icon} size={14} style={spin ? { animation: "spin 1s linear infinite" } : undefined} />
      <span style={{ flex: 1 }}>{text}</span>
      {children}
    </div>
  );
}

/* ---- sparkline over the comparable-crawl history -------------------------
   Lives three-up in a ~100px grid column, so everything is overflow-safe:
   label on its own line, compact value + net below it, spark stretched to the
   column. The exact series is the tooltip. */
function SparkStat({ label, points, color, invert }) {
  const first = points[0], last = points[points.length - 1];
  const net = last - first;
  // invert: a falling line is good (issues, warnings)
  const netColor = net === 0 ? "var(--ink-faint)" : (invert ? net < 0 : net > 0) ? "var(--sev-ok)" : "var(--s-4xx)";
  const series = points.map((p) => p.toLocaleString()).join(" → ");
  return (
    <div style={{ minWidth: 0 }} title={`${label} across the last ${points.length} comparable crawls: ${series}`}>
      <div style={{ fontSize: 9.5, fontWeight: 600, color: "var(--ink-faint)", textTransform: "uppercase", letterSpacing: ".05em", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{label}</div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 5, whiteSpace: "nowrap", overflow: "hidden", marginTop: 1 }}>
        <span className="mono" style={{ fontSize: 12, fontWeight: 650 }}>{fmtCompact(last)}</span>
        {net !== 0 && <span className="mono" style={{ fontSize: 10, color: netColor }}>{net > 0 ? "+" : ""}{fmtCompact(net)}</span>}
      </div>
      <Spark points={points} color={color} />
    </div>
  );
}

function Spark({ points, color, w = 96, h = 24 }) {
  if (!points || points.length < 2) return null;
  const min = Math.min(...points), max = Math.max(...points);
  const span = max - min || 1;
  const step = (w - 6) / (points.length - 1);
  const x = (i) => 3 + i * step;
  const y = (v) => h - 3 - ((v - min) / span) * (h - 6);
  const pts = points.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} preserveAspectRatio="none"
      style={{ display: "block", marginTop: 4 }} aria-hidden>
      <polyline points={pts} fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" opacity="0.85" />
      <circle cx={x(points.length - 1)} cy={y(points[points.length - 1])} r="2.2" fill={color} />
    </svg>
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
      <CopyButton text={s.domain} title="Copy domain" />
    </span>
  );
}

/* The full diff for one member — the same rich view as a crawl's Compare tab
   (CompareResult), fed from the shared comparison cache: the pulse already
   computed and cached this pair, so opening the modal is a registry read. */
function DiffModal({ domain, pulse, onClose }) {
  const data = pulse && pulse.data;
  const [res, setRes] = useState(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (data && data.ok) {
      api.compareCrawls(data.prev_id, data.curr_id, false).then(setRes).catch((e) => setErr(String(e)));
    }
  }, [data && data.prev_id, data && data.curr_id]); // eslint-disable-line react-hooks/exhaustive-deps

  let body;
  if (!pulse || pulse.loading) body = <span style={{ color: "var(--ink-faint)" }}>Comparing the two latest crawls…</span>;
  else if (pulse.err) body = <span style={{ color: "var(--s-4xx)" }}>{pulse.err}</span>;
  else if (!data || !data.ok) body = <span>This site needs at least two comparable crawls to show changes. Crawl it again to start a history.</span>;
  else if (err) body = <span style={{ color: "var(--s-4xx)" }}>{err}</span>;
  else if (!res) body = <span style={{ color: "var(--ink-faint)" }}>Loading the diff…</span>;
  else {
    body = (
      <div style={{ maxHeight: "68vh", overflowY: "auto", margin: "0 -6px", padding: "2px 6px" }}>
        <CompareResult res={res} />
      </div>
    );
  }
  const dates = data && data.ok ? ` · ${fmtDate(data.prev_started)} → ${fmtDate(data.curr_started)}` : "";
  return <Modal icon="git-compare" width={1000} title={domain + dates} onClose={onClose} body={body}
    actions={<Btn variant="primary" onClick={onClose}>Close</Btn>} />;
}
