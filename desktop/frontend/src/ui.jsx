/* ===========================================================================
   acrawler — shared UI primitives (ported from the design handoff)
   =========================================================================== */
import React, { useEffect } from "react";
import { icons } from "lucide-react";

/* ---- Lucide icon wrapper (name strings like "layout-grid") ------------- */
function pascal(n) {
  return n.split("-").map((s) => s.charAt(0).toUpperCase() + s.slice(1)).join("");
}
export function Icon({ name, size = 16, stroke = 2, className, style }) {
  const L = icons[pascal(name)];
  if (!L) return <svg width={size} height={size} aria-hidden />;
  return <L size={size} strokeWidth={stroke} className={className} style={style} aria-hidden />;
}

/* ---- semantic color helpers ------------------------------------------- */
export function statusVar(status) {
  if (!status || status <= 0) return "var(--s-block)";
  if (status >= 200 && status < 300) return "var(--s-2xx)";
  if (status >= 300 && status < 400) return "var(--s-3xx)";
  if (status >= 400 && status < 500) return "var(--s-4xx)";
  if (status >= 500) return "var(--s-5xx)";
  return "var(--s-block)";
}
export const SEV = {
  issue: { c: "var(--sev-issue)", label: "Issue", icon: "octagon-alert" },
  warning: { c: "var(--sev-warn)", label: "Warning", icon: "triangle-alert" },
  opportunity: { c: "var(--sev-opp)", label: "Opportunity", icon: "lightbulb" },
  ok: { c: "var(--sev-ok)", label: "Passed", icon: "circle-check" },
};
export const PRIO = { high: "High", medium: "Medium", low: "Low" };

/* ---- buttons ---------------------------------------------------------- */
export function Btn({ children, icon, variant, size, onClick, disabled, title, style }) {
  const cls = ["btn", variant === "primary" ? "primary" : variant === "ghost" ? "ghost" : "", size === "sm" ? "sm" : ""].filter(Boolean).join(" ");
  return (
    <button className={cls} onClick={onClick} disabled={disabled} title={title} style={style}>
      {icon && <Icon name={icon} size={size === "sm" ? 13 : 14} />}
      {children}
    </button>
  );
}
export function IconBtn({ icon, onClick, title, active, size = 16, style }) {
  return (
    <button className="iconbtn" onClick={onClick} title={title} style={Object.assign({ color: active ? "var(--ink)" : undefined, background: active ? "var(--surface-hover)" : undefined }, style)}>
      <Icon name={icon} size={size} />
    </button>
  );
}

/* ---- toggle ----------------------------------------------------------- */
export function Toggle({ on, onChange, disabled }) {
  return <button className={"tgl" + (on ? " on" : "")} disabled={disabled}
    style={disabled ? { opacity: 0.4 } : null}
    onClick={() => !disabled && onChange(!on)} />;
}

/* ---- segmented control ------------------------------------------------ */
export function Seg({ value, onChange, options }) {
  return (
    <div className="seg">
      {options.map((o) => {
        const val = typeof o === "string" ? o : o.value;
        const lab = typeof o === "string" ? o : o.label;
        return <button key={val} className={value === val ? "on" : ""} onClick={() => onChange(val)}>{lab}</button>;
      })}
    </div>
  );
}

/* ---- badges ----------------------------------------------------------- */
export function StatusBadge({ status, statusText }) {
  const c = statusVar(status);
  const label = !status || status <= 0 ? (status === -1 ? "ROBOTS" : "ERR") : status;
  return (
    <span className="badge tint" style={{ "--c": c }} title={statusText}>
      <span className="statusdot" style={{ background: c }} />{label}
    </span>
  );
}
export function SevDot({ severity }) {
  return <span className="statusdot" style={{ background: (SEV[severity] || SEV.ok).c }} />;
}
export function SevTag({ severity, children }) {
  const s = SEV[severity] || SEV.ok;
  return <span className="sev" style={{ "--c": s.c, color: s.c }}>
    <span className="statusdot" style={{ background: s.c }} />{children || s.label}
  </span>;
}
export function IndexBadge({ value }) {
  const ok = value === true || value === "Indexable" || value === "indexable";
  return <span className="badge tint" style={{ "--c": ok ? "var(--sev-ok)" : "var(--ink-3)" }}>
    {ok ? "Indexable" : "Non-Indexable"}
  </span>;
}

/* ---- search input ----------------------------------------------------- */
export function Search({ value, onChange, placeholder, width, autoFocus }) {
  return (
    <div className="search" style={{ width }}>
      <Icon name="search" size={14} />
      <input className="input" value={value} placeholder={placeholder || "Search"}
        autoFocus={autoFocus} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}

/* ---- field ------------------------------------------------------------ */
export function Field({ label, hint, children }) {
  return (
    <div className="field">
      {label && <label>{label}</label>}
      {children}
      {hint && <div className="hint">{hint}</div>}
    </div>
  );
}

/* ---- empty state ------------------------------------------------------ */
export function Empty({ icon, title, children, action }) {
  return (
    <div className="empty fade">
      <div className="ic"><Icon name={icon} size={26} /></div>
      <h3>{title}</h3>
      <p>{children}</p>
      {action}
    </div>
  );
}

/* ---- mini sparkbar (status breakdown) --------------------------------- */
export function StatusBar({ status, height = 8, radius = 5, showLabels }) {
  const order = [["2xx", "var(--s-2xx)"], ["3xx", "var(--s-3xx)"], ["4xx", "var(--s-4xx)"], ["5xx", "var(--s-5xx)"], ["blocked", "var(--s-block)"], ["noresp", "var(--ink-faint)"]];
  const total = Object.values(status).reduce((a, b) => a + b, 0) || 1;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", height, borderRadius: radius, overflow: "hidden", gap: 1.5, background: "var(--border-soft)" }}>
        {order.map(([k, c]) => status[k] ? <div key={k} title={`${k}: ${status[k].toLocaleString()}`} style={{ width: (status[k] / total * 100) + "%", background: c }} /> : null)}
      </div>
      {showLabels && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: "6px 14px" }}>
          {order.map(([k, c]) => status[k] ? (
            <span key={k} style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: 11.5, color: "var(--ink-2)" }}>
              <span className="statusdot" style={{ background: c }} />
              <span style={{ fontWeight: 600 }}>{k}</span>
              <span className="mono" style={{ color: "var(--ink-faint)" }}>{status[k].toLocaleString()}</span>
            </span>
          ) : null)}
        </div>
      )}
    </div>
  );
}

/* ---- donut ring ------------------------------------------------------- */
export function Ring({ value, total, size = 44, stroke = 5, color = "var(--sev-ok)" }) {
  const r = (size - stroke) / 2, c = 2 * Math.PI * r, pct = total ? value / total : 0;
  return (
    <svg width={size} height={size} style={{ transform: "rotate(-90deg)" }}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border)" strokeWidth={stroke} />
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={color} strokeWidth={stroke}
        strokeDasharray={c} strokeDashoffset={c * (1 - pct)} strokeLinecap="round" />
    </svg>
  );
}

/* ---- reusable modal --------------------------------------------------- */
export function Modal({ title, body, actions, onClose, icon, danger }) {
  useEffect(() => {
    const h = (e) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, []);
  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "oklch(0.2 0.02 262 / 0.42)", backdropFilter: "blur(2px)", zIndex: 100, display: "flex", alignItems: "center", justifyContent: "center", animation: "fadeUp .15s ease" }}>
      <div onClick={(e) => e.stopPropagation()} className="card fade" style={{ width: 420, padding: 22, boxShadow: "var(--shadow-lg)" }}>
        <div style={{ display: "flex", gap: 13, marginBottom: 14 }}>
          {icon && <div style={{ width: 38, height: 38, flex: "0 0 38px", display: "flex", alignItems: "center", justifyContent: "center", background: danger ? "color-mix(in oklab, var(--s-4xx) 14%, transparent)" : "var(--accent-soft)", color: danger ? "var(--s-4xx)" : "var(--accent)" }}><Icon name={icon} size={19} /></div>}
          <div style={{ minWidth: 0, flex: 1 }}>
            <div style={{ fontSize: 15, fontWeight: 650, marginBottom: 5 }}>{title}</div>
            <div style={{ fontSize: 12.5, color: "var(--ink-2)", lineHeight: 1.55 }}>{body}</div>
          </div>
        </div>
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>{actions}</div>
      </div>
    </div>
  );
}

/* ---- toast ------------------------------------------------------------ */
export function Toast({ msg, icon }) {
  return <div className="fade" style={{ position: "fixed", bottom: 20, left: "50%", transform: "translateX(-50%)", zIndex: 200, display: "flex", alignItems: "center", gap: 10, padding: "11px 16px", background: "var(--primary)", color: "var(--primary-ink)", boxShadow: "var(--shadow-lg)", fontSize: 12.5, fontWeight: 500 }}>
    <Icon name={icon} size={16} />{msg}
  </div>;
}
