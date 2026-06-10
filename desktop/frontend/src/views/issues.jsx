/* ===========================================================================
   Issues browser — full catalogue grouped by category, severity encoding
   =========================================================================== */
import React, { useEffect, useState } from "react";
import { Icon, Seg, Search, Toggle, Empty, SevDot, SEV, PRIO } from "../ui";
import { api } from "../api";
import { prettyCat } from "./results-shell";

export function IssuesBrowser({ crawlId, onFilterByIssue }) {
  const [sev, setSev] = useState("all");
  const [q, setQ] = useState("");
  const [showPassed, setShowPassed] = useState(false);
  const [cat, setCat] = useState([]);
  const [error, setError] = useState("");

  useEffect(() => {
    api.issueSummary(crawlId).then((g) => setCat(g || [])).catch((e) => setError(String(e)));
  }, [crawlId]);

  if (error) return <Empty icon="circle-alert" title="Couldn't load issues">{error}</Empty>;

  const all = cat.flatMap((g) => g.items || []);
  const totals = { issue: 0, warning: 0, opportunity: 0 };
  all.forEach((i) => { if (totals[i.severity] != null) totals[i.severity] += i.count; });
  const passedCount = all.filter((i) => i.count === 0).length;

  const groups = cat.map((g) => ({
    ...g,
    items: (g.items || []).filter((i) =>
      (sev === "all" || i.severity === sev) &&
      (showPassed || i.count > 0) &&
      (q === "" || i.name.toLowerCase().includes(q.toLowerCase()) || g.category.toLowerCase().includes(q.toLowerCase()))),
  })).filter((g) => g.items.length);

  return (
    <div style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      {/* filter bar */}
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "10px 16px", borderBottom: "1px solid var(--border-soft)" }}>
        <Seg value={sev} onChange={setSev} options={[
          { value: "all", label: "All" },
          { value: "issue", label: `Issues ${totals.issue}` },
          { value: "warning", label: `Warnings ${totals.warning}` },
          { value: "opportunity", label: `Opps ${totals.opportunity}` },
        ]} />
        <Search value={q} onChange={setQ} placeholder="Search checks…" width={220} />
        <div style={{ flex: 1 }} />
        <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12, color: "var(--ink-2)" }}>
          <Toggle on={showPassed} onChange={setShowPassed} /> Show {passedCount} passed
        </label>
      </div>

      <div className="scroll" style={{ padding: "8px 0" }}>
        {groups.length === 0 && <Empty icon="circle-check" title="Nothing here">No checks match this filter.</Empty>}
        {groups.map((g) => {
          const live = g.items.filter((i) => i.count > 0);
          return (
            <div key={g.category} style={{ marginBottom: 4 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 9, padding: "9px 16px 7px" }}>
                <span style={{ fontSize: 11, fontWeight: 700, letterSpacing: ".04em", textTransform: "uppercase", color: "var(--ink-2)" }}>{prettyCat(g.category)}</span>
                <span style={{ height: 1, flex: 1, background: "var(--border-soft)" }} />
                {live.length === 0
                  ? <span style={{ display: "inline-flex", alignItems: "center", gap: 5, fontSize: 11, color: "var(--sev-ok)", fontWeight: 600 }}><Icon name="circle-check" size={13} />All passed</span>
                  : <span className="mono" style={{ fontSize: 11, color: "var(--ink-faint)" }}>{live.reduce((a, b) => a + b.count, 0)} affected</span>}
              </div>
              {g.items.map((it) => {
                const passed = it.count === 0;
                return (
                  <div key={it.id} className={passed ? "" : "datarow"} onClick={() => !passed && onFilterByIssue("internal", { id: it.id, name: it.name })}
                    style={{ display: "grid", gridTemplateColumns: "22px minmax(0,1fr) 78px 58px 52px 18px", gap: 9, alignItems: "center", padding: "8px 16px", borderBottom: "1px solid var(--border-soft)", cursor: "default", opacity: passed ? 0.5 : 1 }}>
                    {passed ? <Icon name="check" size={14} style={{ color: "var(--sev-ok)" }} /> : <SevDot severity={it.severity} />}
                    <span style={{ fontSize: 12.5, fontWeight: passed ? 400 : 500, color: passed ? "var(--ink-3)" : "var(--ink)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }} title={it.name}>{it.name}</span>
                    <span style={{ fontSize: 11, fontWeight: 600, color: passed ? "var(--ink-faint)" : (SEV[it.severity] || SEV.ok).c }}>{passed ? "—" : (SEV[it.severity] || SEV.ok).label}</span>
                    <span style={{ fontSize: 11, color: "var(--ink-faint)" }}>{PRIO[it.priority] || it.priority}</span>
                    <span className="mono" style={{ fontSize: 12.5, fontWeight: 600, textAlign: "right", color: passed ? "var(--ink-faint)" : it.severity === "issue" ? "var(--sev-issue)" : "var(--ink-2)" }}>
                      {passed ? "0" : it.count.toLocaleString()}
                    </span>
                    {passed ? <span /> : <Icon name="chevron-right" size={14} style={{ color: "var(--ink-faint)" }} />}
                  </div>
                );
              })}
            </div>
          );
        })}
        {all.length > 0 && totals.issue === 0 && totals.warning === 0 && totals.opportunity === 0 && (
          <Empty icon="party-popper" title="No issues — nicely done">This crawl turned up zero problems. That's rare. Pop the bubbly.</Empty>
        )}
      </div>
    </div>
  );
}
