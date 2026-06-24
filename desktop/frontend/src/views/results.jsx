/* ===========================================================================
   Results workspace — generic sortable/filterable/column-toggle data table
   over the backend's export datasets ({header, rows} of strings).
   =========================================================================== */
import React, { useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Icon, Btn, Search, Empty, StatusBadge, IndexBadge, CopyButton } from "../ui";
import { urlShort } from "../api";

/* Row height = --row-h (30px, single-line cells) + 1px bottom border.
   Rows are uniform, so a fixed estimate keeps virtualization jitter-free. */
const ROW_H = 31;

/* column behaviour inferred from the export header name */
function colKind(label) {
  const l = label.toLowerCase();
  if (l === "status code" || l === "status") return "status";
  if (l.includes("indexability") && !l.includes("status")) return "index";
  if (l === "address" || l === "url" || l === "source" || l === "destination" || l === "target" || l.includes("redirect url") || l.includes("canonical")) return "url";
  if (/(count|depth|inlinks|outlinks|size|time|score|length|words|hops|kb|ms|line|refs|occurrences)/.test(l)) return "num";
  return "text";
}

function Cell({ kind, value }) {
  if (kind === "status") {
    const n = parseInt(value, 10);
    if (!isNaN(n)) return <StatusBadge status={n} />;
  }
  if (kind === "index" && (value === "Indexable" || value === "Non-Indexable" || value === "indexable" || value === "non-indexable")) {
    return <IndexBadge value={value === "Indexable" || value === "indexable"} />;
  }
  if (kind === "url") {
    return <span className="mono" title={value} style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", display: "block", color: "var(--ink)", fontSize: 11.5 }}>{urlShort(value)}</span>;
  }
  if (kind === "num") {
    return <span className="mono" style={{ color: "var(--ink-2)", display: "block", textAlign: "right" }}>{value || "—"}</span>;
  }
  const missing = value === "" || value == null;
  return <span style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", display: "block", color: missing ? "var(--ink-faint)" : "var(--ink-2)" }}>{missing ? "—" : value}</span>;
}

export function DataTable({ header, rows, total, truncated, onRowClick, urlColumn = 0 }) {
  const [sort, setSort] = useState({ idx: -1, dir: 1 });
  const [hidden, setHidden] = useState({});
  const [colMenu, setColMenu] = useState(false);
  const [q, setQ] = useState("");
  const parentRef = useRef(null);

  const kinds = useMemo(() => header.map(colKind), [header]);
  const visible = header.map((_, i) => i).filter((i) => !hidden[i]);

  const filtered = useMemo(() => {
    let r = rows;
    if (q) {
      const s = q.toLowerCase();
      r = r.filter((row) => row.some((v) => String(v ?? "").toLowerCase().includes(s)));
    }
    if (sort.idx >= 0) {
      const idx = sort.idx, num = kinds[idx] === "num" || kinds[idx] === "status";
      r = [...r].sort((a, b) => {
        const va = a[idx] ?? "", vb = b[idx] ?? "";
        if (num) return ((parseFloat(va) || 0) - (parseFloat(vb) || 0)) * sort.dir;
        return String(va).localeCompare(String(vb)) * sort.dir;
      });
    }
    return r;
  }, [rows, q, sort, kinds]);

  const rowVirtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_H,
    overscan: 16,
  });

  const widthFor = (i) => {
    const k = kinds[i];
    if (k === "url") return "minmax(220px,2.4fr)";
    if (k === "status") return "82px";
    if (k === "index") return "118px";
    if (k === "num") return "84px";
    return "minmax(110px,1.2fr)";
  };
  const grid = visible.map(widthFor).join(" ");

  function toggleSort(i) { setSort((s) => s.idx === i ? { idx: i, dir: -s.dir } : { idx: i, dir: 1 }); }

  return (
    <div style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      {/* table toolbar */}
      <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 14px", borderBottom: "1px solid var(--border-soft)", flex: "0 0 auto" }}>
        <Search value={q} onChange={setQ} placeholder="Filter rows…" width={230} />
        <span className="mono" style={{ fontSize: 11.5, color: "var(--ink-faint)" }}>{filtered.length.toLocaleString()} {q ? "matched" : "rows"}</span>
        <div style={{ flex: 1 }} />
        <div style={{ position: "relative" }}>
          <Btn size="sm" icon="columns-3" onClick={() => setColMenu((v) => !v)}>Columns</Btn>
          {colMenu && (
            <>
              <div style={{ position: "fixed", inset: 0, zIndex: 30 }} onClick={() => setColMenu(false)} />
              <div className="card fade" style={{ position: "absolute", right: 0, top: 34, zIndex: 31, width: 230, padding: 6, boxShadow: "var(--shadow-lg)", maxHeight: 320, overflowY: "auto" }}>
                {header.map((label, i) => (
                  <label key={i} style={{ display: "flex", alignItems: "center", gap: 9, padding: "6px 8px", fontSize: 12, cursor: "default" }}
                    onMouseEnter={(e) => e.currentTarget.style.background = "var(--surface-hover)"}
                    onMouseLeave={(e) => e.currentTarget.style.background = "transparent"}>
                    <input type="checkbox" checked={!hidden[i]} onChange={() => setHidden((h) => ({ ...h, [i]: !h[i] }))} style={{ accentColor: "var(--accent)" }} />
                    {label}
                  </label>
                ))}
              </div>
            </>
          )}
        </div>
        <Btn size="sm" icon="copy" title="Copy table as TSV" onClick={() => {
          const tsv = [header.join("\t"), ...filtered.map((r) => r.join("\t"))].join("\n");
          navigator.clipboard && navigator.clipboard.writeText(tsv);
        }} />
      </div>

      {/* head — right padding reserves the body's scrollbar gutter so the
         header columns line up with the rows below. */}
      <div style={{ display: "grid", gridTemplateColumns: grid, gap: 0, paddingLeft: 8, paddingRight: "calc(8px + var(--sbw))", borderBottom: "1px solid var(--border)", background: "var(--surface-2)", flex: "0 0 auto" }}>
        {visible.map((i) => (
          <div key={i} onClick={() => toggleSort(i)}
            style={{ display: "flex", alignItems: "center", gap: 4, padding: "8px 8px", fontSize: 10.5, fontWeight: 600, letterSpacing: ".04em", textTransform: "uppercase", color: sort.idx === i ? "var(--ink)" : "var(--ink-faint)", justifyContent: kinds[i] === "num" ? "flex-end" : "flex-start", userSelect: "none", whiteSpace: "nowrap", overflow: "hidden" }}>
            {header[i]}
            {sort.idx === i && <Icon name={sort.dir === 1 ? "arrow-up" : "arrow-down"} size={11} />}
          </div>
        ))}
      </div>

      {/* body — only the rows in view (plus overscan) are mounted; the spacer
         div carries the full scroll height so the scrollbar stays accurate. */}
      <div className="scroll" ref={parentRef} style={{ flex: 1, scrollbarGutter: "stable" }}>
        {filtered.length === 0 && <Empty icon="search-x" title="No matching rows">Try a different filter, or clear the search to see all {rows.length.toLocaleString()} rows.</Empty>}
        {filtered.length > 0 && (
          <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative", width: "100%" }}>
            {rowVirtualizer.getVirtualItems().map((vr) => {
              const row = filtered[vr.index];
              return (
                <div key={vr.key} className="datarow copyhost" onClick={() => onRowClick && onRowClick(row)}
                  style={{ position: "absolute", top: 0, left: 0, width: "100%", transform: `translateY(${vr.start}px)`, display: "grid", gridTemplateColumns: grid, gap: 0, padding: "0 8px", borderBottom: "1px solid var(--border-soft)", alignItems: "center", minHeight: "var(--row-h)" }}>
                  {visible.map((i) => (
                    <div key={i} style={{ padding: "4px 8px", fontSize: 12, minWidth: 0, overflow: "hidden", position: kinds[i] === "url" ? "relative" : undefined }}>
                      <Cell kind={kinds[i]} value={row[i]} />
                      {kinds[i] === "url" && row[i] && (
                        <CopyButton text={row[i]} style={{ position: "absolute", right: 2, top: "50%", transform: "translateY(-50%)", background: "var(--surface-hover)", boxShadow: "-8px 0 7px 3px var(--surface-hover)" }} />
                      )}
                    </div>
                  ))}
                </div>
              );
            })}
          </div>
        )}
        {truncated && filtered.length > 0 && (
          <div style={{ padding: "12px 16px", fontSize: 11, color: "var(--ink-faint)", display: "flex", alignItems: "center", gap: 7 }}>
            <Icon name="info" size={13} />Showing the first {rows.length.toLocaleString()} of {total.toLocaleString()} rows — export to get the full set.
          </div>
        )}
      </div>
    </div>
  );
}
