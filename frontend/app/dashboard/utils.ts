import type { ScanRow } from "./types";

export function actionColor(action: string) {
  if (action === "BUY") return "text-emerald-400";
  if (action === "SELL") return "text-red-400";
  return "text-yellow-400";
}

export function actionBg(action: string) {
  if (action === "BUY") return "bg-emerald-500/20 border-emerald-500/40";
  if (action === "SELL") return "bg-red-500/20 border-red-500/40";
  return "bg-yellow-500/20 border-yellow-500/40";
}

export function formatPrice(v: number) {
  return `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

export function formatPct(v: number) {
  const sign = v >= 0 ? "+" : "";
  return `${sign}${v.toFixed(2)}%`;
}

export function notifyModeLabel(mode?: string) {
  return mode === "interval" ? "Interval (time-slot scan)" : "Event (change-based)";
}

export function intervalShortLabel(minute?: number, hour?: number) {
  const m = minute ?? ((hour ?? 0) > 0 ? (hour ?? 0) * 60 : 0);
  if (!m) return "";
  if (m % 60 === 0) return `${m / 60}h`;
  return `${m}m`;
}

export function inferCountryFromExchange(exchange?: string) {
  const ex = (exchange ?? "").toUpperCase();
  if (ex.includes("NASDAQ") || ex.includes("NYSE") || ex.includes("AMEX") || ex === "NMS") return "US";
  if (ex.includes("KOSPI") || ex.includes("KOSDAQ") || ex.includes("KRX")) return "KR";
  if (ex.includes("TSE") || ex.includes("JPX")) return "JP";
  if (ex.includes("LSE")) return "UK";
  if (ex.includes("HKEX")) return "HK";
  if (ex.includes("SSE") || ex.includes("SZSE")) return "CN";
  return "-";
}

export function normalizeTypeLabel(typeDisplay?: string) {
  const t = (typeDisplay ?? "").toUpperCase();
  if (t.includes("ETF")) return "ETF";
  if (t.includes("EQUITY") || t.includes("COMMON") || t.includes("STOCK")) return "Stock";
  if (!t) return "-";
  return typeDisplay ?? "-";
}

export function typeBadgeClass(typeLabel: string) {
  if (typeLabel === "ETF") return "border-cyan-500/40 text-cyan-300 bg-cyan-500/10";
  if (typeLabel === "Stock") return "border-emerald-500/40 text-emerald-300 bg-emerald-500/10";
  return "border-slate-600 text-slate-300 bg-slate-700/40";
}

export function actionEmoji(action: string) {
  if (action === "BUY") return "🟢";
  if (action === "SELL") return "🔴";
  return "🟡";
}

export function scanScoreClass(v: number) {
  if (v >= 75) return "text-emerald-300";
  if (v >= 60) return "text-cyan-300";
  if (v >= 45) return "text-yellow-300";
  return "text-slate-400";
}

export function topListScore(row: ScanRow) {
  const direction = row.signal.trend_action ?? row.signal.action;
  const timing = row.signal.timing_action ?? row.signal.action;
  return row.signal.buy_pct - (row.signal.sell_pct * 0.5) + (direction === "BUY" ? 5 : 0) + (timing === "BUY" ? 3 : 0);
}
