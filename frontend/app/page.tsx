"use client";

import { useEffect, useRef, useState } from "react";

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8000";

// ── Types ──────────────────────────────────────────────────────────────────
interface CandleDoc {
  symbol: string;
  timestamp: string;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

interface Indicators {
  RSI14: number;
  SMA20: number;
  SMA50: number;
  BBUpper: number;
  BBMid: number;
  BBLower: number;
  SupertrendDir: number;
  SupertrendLine: number;
  ATR14: number;
}

interface Score {
  RSIScore: number;
  MAScore: number;
  BBScore: number;
  SupertrendScore: number;
  Total: number;
}

interface Signal {
  symbol: string;
  action: string;
  trend_action?: string;
  timing_action?: string;
  is_special_signal?: boolean;
  buy_pct: number;
  sell_pct: number;
  hold_pct: number;
  reason: string;
  indicators: Indicators;
  timing_indicators?: Indicators;
  score: Score;
  timing_score?: Score;
  timestamp: string;
}

interface DBStats {
  data_size_mb: number;
  storage_size_mb: number;
  collections: number;
  objects: number;
  over_limit: boolean;
}

interface WatchlistItem {
  symbol: string;
  added_at: string;
  notify_interval_hour?: number;
  notify_mode?: "event" | "interval" | string;
  pinned?: boolean;
  sort_order?: number;
}

interface SymbolSearchResult {
  symbol: string;
  name: string;
  exchange: string;
  type_display: string;
}

interface SourceStatus {
  source: string;
  ok: boolean;
  latency_ms: number;
  error?: string;
  detail?: string;
  checked_at: string;
  score: number;
}

interface ScanRow {
  symbol: string;
  signal: Signal;
}

const MARKET_CAP_BASE_SYMBOLS = [
  "TQQQ", "QQQ", "SPY", "SOXL", "SOXX",
  "NVDA", "TSLA", "AAPL", "MSFT", "AMZN", "META", "GOOGL", "AMD", "PLTR", "SMCI",
];

const SCAN_TF = "120";
const CUSTOM_UNIVERSE_STORAGE_KEY = "midas.custom.universe.symbols";
const EXCLUDED_UNIVERSE_STORAGE_KEY = "midas.excluded.universe.symbols";

// ── Helpers ────────────────────────────────────────────────────────────────
async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, { cache: "no-store" });
  if (!res.ok) throw new Error(`${path} -> ${res.status}`);
  return res.json();
}

function actionColor(action: string) {
  if (action === "BUY") return "text-emerald-400";
  if (action === "SELL") return "text-red-400";
  return "text-yellow-400";
}

function actionBg(action: string) {
  if (action === "BUY") return "bg-emerald-500/20 border-emerald-500/40";
  if (action === "SELL") return "bg-red-500/20 border-red-500/40";
  return "bg-yellow-500/20 border-yellow-500/40";
}

function formatPrice(v: number) {
  return `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatPct(v: number) {
  const sign = v >= 0 ? "+" : "";
  return `${sign}${v.toFixed(2)}%`;
}

function notifyModeLabel(mode?: string) {
  return mode === "interval" ? "Interval" : "Event";
}

function actionEmoji(action: string) {
  if (action === "BUY") return "🟢";
  if (action === "SELL") return "🔴";
  return "🟡";
}

function scanScoreClass(v: number) {
  if (v >= 75) return "text-emerald-300";
  if (v >= 60) return "text-cyan-300";
  if (v >= 45) return "text-yellow-300";
  return "text-slate-400";
}

// ── Candlestick chart (pure canvas) ───────────────────────────────────────
function CandleChart({ candles }: { candles: CandleDoc[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || candles.length === 0) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const W = canvas.width;
    const H = canvas.height;
    if (W === 0 || H === 0) return;

    const pad = { top: 20, bottom: 30, left: 10, right: 10 };
    const chartW = W - pad.left - pad.right;
    const chartH = H - pad.top - pad.bottom;

    const recent = candles.slice(-120).filter((c) => c.close > 0);
    if (recent.length === 0) return;

    const highs = recent.map((c) => (c.high > 0 ? c.high : c.close));
    const lows = recent.map((c) => (c.low > 0 ? c.low : c.close));
    const maxP = highs.reduce((a, b) => Math.max(a, b), highs[0]);
    const minP = lows.reduce((a, b) => Math.min(a, b), lows[0]);
    const range = maxP - minP || 1;

    const toY = (p: number) => pad.top + chartH - ((p - minP) / range) * chartH;
    const barW = Math.max(1, Math.floor(chartW / recent.length) - 1);

    ctx.clearRect(0, 0, W, H);
    ctx.fillStyle = "#0f1117";
    ctx.fillRect(0, 0, W, H);

    ctx.strokeStyle = "#1e2535";
    ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i++) {
      const y = pad.top + (chartH / 4) * i;
      ctx.beginPath();
      ctx.moveTo(pad.left, y);
      ctx.lineTo(W - pad.right, y);
      ctx.stroke();
    }

    recent.forEach((c, i) => {
      const x = pad.left + (i / recent.length) * chartW;
      const open = c.open > 0 ? c.open : c.close;
      const close = c.close;
      const high = c.high > 0 ? c.high : Math.max(open, close);
      const low = c.low > 0 ? c.low : Math.min(open, close);
      const isUp = close >= open;

      ctx.strokeStyle = isUp ? "#34d399" : "#f87171";
      ctx.fillStyle = isUp ? "#34d399" : "#f87171";
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x + barW / 2, toY(high));
      ctx.lineTo(x + barW / 2, toY(low));
      ctx.stroke();

      const bodyTop = toY(Math.max(open, close));
      const bodyH = Math.max(1, Math.abs(toY(open) - toY(close)));
      ctx.fillRect(x, bodyTop, barW, bodyH);
    });

    ctx.fillStyle = "#94a3b8";
    ctx.font = "11px monospace";
    ctx.fillText(`$${recent[recent.length - 1]?.close.toFixed(2)}`, pad.left + 4, pad.top - 4);
  }, [candles]);

  return (
    <canvas
      ref={canvasRef}
      width={800}
      height={300}
      className="w-full rounded-lg"
      style={{ imageRendering: "pixelated" }}
    />
  );
}

// ── Probability bar ────────────────────────────────────────────────────────
function ProbBar({ buy, sell, hold }: { buy: number; sell: number; hold: number }) {
  return (
    <div className="w-full">
      <div className="flex h-6 rounded overflow-hidden text-xs font-bold">
        <div className="flex items-center justify-center bg-emerald-500 transition-all" style={{ width: `${buy}%` }}>
          {buy >= 15 ? `${buy}%` : ""}
        </div>
        <div className="flex items-center justify-center bg-yellow-500 transition-all" style={{ width: `${hold}%` }}>
          {hold >= 15 ? `${hold}%` : ""}
        </div>
        <div className="flex items-center justify-center bg-red-500 transition-all" style={{ width: `${sell}%` }}>
          {sell >= 15 ? `${sell}%` : ""}
        </div>
      </div>
      <div className="flex justify-between text-xs text-slate-400 mt-1">
        <span className="text-emerald-400">Buy {buy}%</span>
        <span className="text-yellow-400">Hold {hold}%</span>
        <span className="text-red-400">Sell {sell}%</span>
      </div>
    </div>
  );
}

// ── Score breakdown bar ────────────────────────────────────────────────────
function ScoreBar({ label, value }: { label: string; value: number }) {
  const pct = Math.abs(value) * 100;
  const isPos = value >= 0;
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-slate-400 w-24 shrink-0">{label}</span>
      <div className="flex-1 bg-slate-700 rounded h-2 overflow-hidden">
        <div className={`h-2 rounded transition-all ${isPos ? "bg-emerald-500" : "bg-red-500"}`} style={{ width: `${pct}%` }} />
      </div>
      <span className={`text-xs w-10 text-right font-mono ${isPos ? "text-emerald-400" : "text-red-400"}`}>
        {value >= 0 ? "+" : ""}{value.toFixed(2)}
      </span>
    </div>
  );
}

// ── Indicator row ──────────────────────────────────────────────────────────
function IndicatorRow({ label, value, badge }: { label: string; value: string; badge?: { text: string; cls: string } }) {
  return (
    <div className="flex items-center justify-between py-0.5">
      <span className="text-slate-400 text-xs">{label}</span>
      <div className="flex items-center gap-2">
        <span className="text-slate-200 text-xs font-mono">{value}</span>
        {badge && <span className={`text-xs ${badge.cls}`}>{badge.text}</span>}
      </div>
    </div>
  );
}

// ── Watchlist panel ────────────────────────────────────────────────────────
function WatchlistPanel({
  onSelect,
  onItemsChange,
}: {
  onSelect: (symbol: string) => void;
  onItemsChange?: (items: WatchlistItem[]) => void;
}) {
  const [items, setItems] = useState<WatchlistItem[]>([]);
  const [input, setInput] = useState("");
  const [intervalHour, setIntervalHour] = useState(4);
  const [notifyMode, setNotifyMode] = useState<"event" | "interval">("event");
  const [searching, setSearching] = useState(false);
  const [results, setResults] = useState<SymbolSearchResult[]>([]);
  const [saving, setSaving] = useState(false);
  const [draggingSymbol, setDraggingSymbol] = useState<string | null>(null);

  const load = async () => {
    try {
      const data = await fetchJSON<WatchlistItem[]>("/api/watchlist");
      const next = data ?? [];
      setItems(next);
      onItemsChange?.(next);
    } catch { /* ignore */ }
  };

  useEffect(() => { load(); }, []);

  useEffect(() => {
    if (input.trim().length < 2) {
      setResults([]);
      return;
    }
    const t = setTimeout(async () => {
      setSearching(true);
      try {
        const data = await fetchJSON<SymbolSearchResult[]>(`/api/symbols/search?q=${encodeURIComponent(input.trim())}&limit=8`);
        setResults(data ?? []);
      } catch {
        setResults([]);
      } finally {
        setSearching(false);
      }
    }, 250);

    return () => clearTimeout(t);
  }, [input]);

  const add = async () => {
    const sym = input.trim().toUpperCase();
    if (!sym) return;
    setSaving(true);
    try {
      await fetch(
        `${API_BASE}/api/watchlist?symbol=${sym}&notify_interval_hours=${intervalHour}&notify_mode=${notifyMode}`,
        { method: "POST" },
      );
      setInput("");
      setResults([]);
      await load();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (sym: string) => {
    await fetch(`${API_BASE}/api/watchlist?symbol=${sym}`, { method: "DELETE" });
    await load();
  };

  const updateInterval = async (sym: string, nextHour: number) => {
    const item = items.find((x) => x.symbol === sym);
    const mode = item?.notify_mode === "interval" ? "interval" : "event";
    await fetch(`${API_BASE}/api/watchlist?symbol=${sym}&notify_interval_hours=${nextHour}&notify_mode=${mode}`, { method: "POST" });
    await load();
  };

  const updateMode = async (sym: string, nextMode: "event" | "interval") => {
    const item = items.find((x) => x.symbol === sym);
    const nextHour = item?.notify_interval_hour ?? 4;
    await fetch(`${API_BASE}/api/watchlist?symbol=${sym}&notify_interval_hours=${nextHour}&notify_mode=${nextMode}`, { method: "POST" });
    await load();
  };

  const togglePin = async (item: WatchlistItem) => {
    const nextPinned = !(item.pinned ?? false);
    await fetch(`${API_BASE}/api/watchlist/pin?symbol=${item.symbol}&pinned=${nextPinned}`, { method: "POST" });
    await load();
  };

  const reorder = async (fromSym: string, toSym: string) => {
    if (!fromSym || !toSym || fromSym === toSym) return;

    const fromIdx = items.findIndex((x) => x.symbol === fromSym);
    const toIdx = items.findIndex((x) => x.symbol === toSym);
    if (fromIdx < 0 || toIdx < 0) return;

    const next = [...items];
    const [moved] = next.splice(fromIdx, 1);
    next.splice(toIdx, 0, moved);

    setItems(next);
    onItemsChange?.(next);

    const symbols = next.map((x) => x.symbol);
    const res = await fetch(`${API_BASE}/api/watchlist/reorder`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ symbols }),
    });
    if (!res.ok) {
      await load();
    }
  };

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <h3 className="text-sm font-semibold text-slate-300 mb-1">Watchlist</h3>
      <p className="text-[11px] text-slate-500 mb-3">E: 상황 변화 시 알림 · I: 주기 강제 알림</p>
      <div className="flex flex-wrap items-center gap-2 mb-3">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && add()}
          placeholder="Add symbol or name..."
          className="flex-1 px-2 py-1.5 bg-slate-700 border border-slate-600 text-white text-xs rounded focus:outline-none focus:border-indigo-500"
        />
        {notifyMode === "interval" && (
          <select
            value={intervalHour}
            onChange={(e) => setIntervalHour(Number(e.target.value))}
            className="px-2 py-1.5 bg-slate-700 border border-slate-600 text-white text-xs rounded focus:outline-none focus:border-indigo-500"
          >
            <option value={1}>1h</option>
            <option value={2}>2h</option>
            <option value={4}>4h</option>
            <option value={6}>6h</option>
            <option value={12}>12h</option>
          </select>
        )}
        <div className="inline-flex rounded border border-slate-600 overflow-hidden">
          <button
            onClick={() => setNotifyMode("event")}
            className={`px-2 py-1.5 text-[11px] transition-colors ${
              notifyMode === "event" ? "bg-cyan-500/20 text-cyan-200" : "bg-slate-700 text-slate-400"
            }`}
          >
            Event
          </button>
          <button
            onClick={() => setNotifyMode("interval")}
            className={`px-2 py-1.5 text-[11px] transition-colors border-l border-slate-600 ${
              notifyMode === "interval" ? "bg-amber-500/20 text-amber-200" : "bg-slate-700 text-slate-400"
            }`}
          >
            Interval
          </button>
        </div>
        <button
          onClick={add}
          disabled={saving}
          className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 text-white text-xs rounded transition-colors disabled:opacity-50"
        >
          Add
        </button>
      </div>
      {searching && <p className="text-[11px] text-slate-500 mb-2">Searching symbols...</p>}
      {results.length > 0 && (
        <div className="mb-3 max-h-36 overflow-auto border border-slate-700 rounded-lg">
          {results.map((row) => (
            <button
              key={`${row.symbol}-${row.exchange}`}
              onClick={() => setInput(row.symbol)}
              className="w-full text-left px-2 py-1.5 hover:bg-slate-700/60 transition-colors border-b border-slate-800 last:border-b-0"
            >
              <div className="flex items-center justify-between">
                <span className="text-xs font-mono text-indigo-300">{row.symbol}</span>
                <span className="text-[10px] text-slate-500">{row.exchange}</span>
              </div>
              <p className="text-[11px] text-slate-400 truncate">{row.name || row.type_display}</p>
            </button>
          ))}
        </div>
      )}
      {items.length === 0 ? (
        <p className="text-xs text-slate-500 text-center py-2">No symbols saved</p>
      ) : (
        <div className="space-y-1">
          {items.map((item) => (
            <div
              key={item.symbol}
              draggable
              onDragStart={() => setDraggingSymbol(item.symbol)}
              onDragEnd={() => setDraggingSymbol(null)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={async () => {
                const from = draggingSymbol;
                setDraggingSymbol(null);
                if (from) await reorder(from, item.symbol);
              }}
              className={`flex items-center justify-between group rounded px-1 ${draggingSymbol === item.symbol ? "opacity-50" : ""}`}
            >
              <div className="flex items-center gap-2">
                <span className="text-xs text-slate-500 cursor-grab select-none" title="Drag to reorder">⋮⋮</span>
                <button
                  onClick={() => togglePin(item)}
                  className={`text-xs transition-colors ${item.pinned ? "text-amber-300" : "text-slate-500 hover:text-amber-300"}`}
                  title={item.pinned ? "Unpin" : "Pin"}
                >
                  {item.pinned ? "★" : "☆"}
                </button>
                <button
                  onClick={() => onSelect(item.symbol)}
                  className="text-sm font-mono text-indigo-400 hover:text-indigo-300 transition-colors"
                >
                  {item.symbol}
                </button>
                <div className="inline-flex rounded border border-slate-600 overflow-hidden">
                  <button
                    onClick={() => updateMode(item.symbol, "event")}
                    className={`px-1.5 py-0.5 text-[10px] transition-colors ${
                      (item.notify_mode ?? "event") === "event" ? "bg-cyan-500/20 text-cyan-200" : "bg-slate-700 text-slate-400"
                    }`}
                    title="상황 변화시에만 알림"
                  >
                    E
                  </button>
                  <button
                    onClick={() => updateMode(item.symbol, "interval")}
                    className={`px-1.5 py-0.5 text-[10px] transition-colors border-l border-slate-600 ${
                      item.notify_mode === "interval" ? "bg-amber-500/20 text-amber-200" : "bg-slate-700 text-slate-400"
                    }`}
                    title="주기 강제 알림"
                  >
                    I
                  </button>
                </div>
                <span className={`text-[10px] px-1.5 py-0.5 rounded border ${item.notify_mode === "interval" ? "border-amber-500/40 text-amber-300" : "border-cyan-500/40 text-cyan-300"}`}>
                  {notifyModeLabel(item.notify_mode)}
                </span>
                {item.notify_mode === "interval" && (
                  <select
                    value={item.notify_interval_hour ?? 4}
                    onChange={(e) => updateInterval(item.symbol, Number(e.target.value))}
                    className="text-[10px] px-1.5 py-0.5 rounded bg-slate-700 border border-slate-600 text-slate-300"
                  >
                    <option value={1}>1h</option>
                    <option value={2}>2h</option>
                    <option value={4}>4h</option>
                    <option value={6}>6h</option>
                    <option value={12}>12h</option>
                  </select>
                )}
              </div>
              <button
                onClick={() => remove(item.symbol)}
                className="text-xs text-slate-600 hover:text-red-400 transition-colors opacity-0 group-hover:opacity-100"
              >
                ✕
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ── DB storage monitor panel ───────────────────────────────────────────────
function DBStatsPanel() {
  const [stats, setStats] = useState<DBStats | null>(null);
  const [pruning, setPruning] = useState(false);
  const [msg, setMsg] = useState("");

  const load = async () => {
    try {
      const s = await fetchJSON<DBStats>("/api/db/stats");
      setStats(s);
    } catch { /* ignore */ }
  };

  useEffect(() => {
    load();
    const t = setInterval(load, 60_000);
    return () => clearInterval(t);
  }, []);

  const prune = async () => {
    setPruning(true);
    setMsg("");
    try {
      const r = await fetch(`${API_BASE}/api/db/prune?keep_days=365`, { method: "POST" });
      const d = await r.json();
      setMsg(`Deleted ${d.deleted} records`);
      await load();
    } catch {
      setMsg("Error occurred");
    } finally {
      setPruning(false);
    }
  };

  if (!stats) return null;

  const usedPct = Math.min(100, (stats.storage_size_mb / 500) * 100);

  return (
    <div className={`rounded-xl border p-4 ${stats.over_limit ? "border-red-500/60 bg-red-500/10" : "border-slate-700 bg-slate-800/50"}`}>
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-slate-300">DB Storage</h3>
        {stats.over_limit && (
          <span className="text-xs bg-red-500 text-white px-2 py-0.5 rounded-full animate-pulse">⚠ Near Limit</span>
        )}
      </div>
      <div className="w-full bg-slate-700 rounded-full h-2 mb-2">
        <div
          className={`h-2 rounded-full transition-all ${usedPct > 90 ? "bg-red-500" : usedPct > 70 ? "bg-yellow-500" : "bg-emerald-500"}`}
          style={{ width: `${usedPct}%` }}
        />
      </div>
      <div className="flex justify-between text-xs text-slate-400 mb-3">
        <span>{stats.storage_size_mb.toFixed(1)} MB used</span>
        <span>500 MB limit</span>
      </div>
      <div className="grid grid-cols-2 gap-2 text-xs text-slate-400 mb-3">
        <span>Data: {stats.data_size_mb.toFixed(2)} MB</span>
        <span>Docs: {stats.objects.toLocaleString()}</span>
      </div>
      <button
        onClick={prune}
        disabled={pruning}
        className="w-full text-xs bg-slate-700 hover:bg-slate-600 text-slate-300 py-1.5 rounded transition-colors disabled:opacity-50"
      >
        {pruning ? "Pruning..." : "Prune data older than 1 year"}
      </button>
      {msg && <p className="text-xs text-center mt-2 text-emerald-400">{msg}</p>}
    </div>
  );
}

function SourceStatusPanel() {
  const [rows, setRows] = useState<SourceStatus[]>([]);

  const load = async () => {
    try {
      const data = await fetchJSON<SourceStatus[]>("/api/sources/status");
      setRows(data ?? []);
    } catch {
      setRows([]);
    }
  };

  useEffect(() => {
    load();
    const t = setInterval(load, 60_000);
    return () => clearInterval(t);
  }, []);

  const badgeClass = (r: SourceStatus) => {
    if (r.ok) return "bg-emerald-500/20 border-emerald-500/40 text-emerald-300";
    if ((r.error ?? "").includes("403")) return "bg-yellow-500/20 border-yellow-500/40 text-yellow-300";
    return "bg-red-500/20 border-red-500/40 text-red-300";
  };

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <h3 className="text-sm font-semibold text-slate-300 mb-3">Data Source Status</h3>
      {rows.length === 0 ? (
        <p className="text-xs text-slate-500">No status data</p>
      ) : (
        <div className="space-y-2">
          {rows.map((r) => (
            <div key={r.source} className="rounded-lg border border-slate-700 p-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-mono text-slate-200">{r.source}</span>
                <span className={`text-[10px] px-2 py-0.5 rounded border ${badgeClass(r)}`}>
                  {r.ok ? "OK" : "ISSUE"}
                </span>
              </div>
              <p className="text-[11px] text-slate-500 mt-1">{r.detail} • {r.latency_ms}ms</p>
              {r.error && <p className="text-[11px] text-slate-400 mt-1 line-clamp-2">{r.error}</p>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Hourly7DayPanel({ symbol, candles }: { symbol: string; candles: CandleDoc[] }) {
  const latest = candles[candles.length - 1];
  const base24h = candles.length > 24 ? candles[candles.length - 25] : undefined;
  const currentPrice = latest?.close ?? 0;
  const change24hPct = base24h && base24h.close > 0 ? ((currentPrice - base24h.close) / base24h.close) * 100 : 0;

  const last24 = candles.slice(-24).reverse();

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="font-semibold text-slate-200">{symbol} 7D Hourly Movement</h2>
        <span className="text-[11px] text-slate-500">최근 7일 · 1시간봉</span>
      </div>

      {candles.length === 0 ? (
        <p className="text-xs text-slate-500">No hourly data</p>
      ) : (
        <>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 mb-3">
            <div className="rounded-lg border border-slate-700 bg-slate-900/40 p-3">
              <p className="text-[11px] text-slate-500 mb-1">Current Price</p>
              <p className="text-xl font-black text-white">{formatPrice(currentPrice)}</p>
              <p className="text-[11px] text-slate-500 mt-1">{latest?.timestamp ? new Date(latest.timestamp).toLocaleString() : "-"}</p>
            </div>
            <div className="rounded-lg border border-slate-700 bg-slate-900/40 p-3">
              <p className="text-[11px] text-slate-500 mb-1">24H Change</p>
              <p className={`text-xl font-black ${change24hPct >= 0 ? "text-emerald-300" : "text-red-300"}`}>{formatPct(change24hPct)}</p>
              <p className="text-[11px] text-slate-500 mt-1">vs 24 hours ago</p>
            </div>
            <div className="rounded-lg border border-slate-700 bg-slate-900/40 p-3">
              <p className="text-[11px] text-slate-500 mb-1">7D Range</p>
              <p className="text-sm font-semibold text-slate-200">
                {formatPrice(Math.min(...candles.map((x) => x.close)))} ~ {formatPrice(Math.max(...candles.map((x) => x.close)))}
              </p>
              <p className="text-[11px] text-slate-500 mt-1">high / low</p>
            </div>
          </div>

          <div className="rounded-lg border border-slate-700 overflow-hidden">
            <div className="max-h-48 overflow-auto">
              <table className="w-full text-xs">
                <thead className="bg-slate-900/60 text-slate-400 sticky top-0">
                  <tr>
                    <th className="text-left px-2 py-1.5 font-medium">Hour</th>
                    <th className="text-right px-2 py-1.5 font-medium">Price</th>
                    <th className="text-right px-2 py-1.5 font-medium">Change</th>
                  </tr>
                </thead>
                <tbody>
                  {last24.map((row, idx) => {
                    const prev = idx < last24.length - 1 ? last24[idx + 1] : undefined;
                    const pct = prev && prev.close > 0 ? ((row.close - prev.close) / prev.close) * 100 : 0;
                    return (
                      <tr key={`${row.timestamp}-${idx}`} className="border-t border-slate-800">
                        <td className="px-2 py-1.5 text-slate-300">{new Date(row.timestamp).toLocaleString()}</td>
                        <td className="px-2 py-1.5 text-right text-slate-200 font-mono">{formatPrice(row.close)}</td>
                        <td className={`px-2 py-1.5 text-right font-mono ${pct >= 0 ? "text-emerald-300" : "text-red-300"}`}>{formatPct(pct)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function DailyClose30Panel({ symbol, candles }: { symbol: string; candles: CandleDoc[] }) {
  const last30 = candles.slice(-30).reverse();

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="font-semibold text-slate-200">{symbol} Daily Close (30D)</h2>
        <span className="text-[11px] text-slate-500">최근 30일 종가</span>
      </div>

      {last30.length === 0 ? (
        <p className="text-xs text-slate-500">No daily close data</p>
      ) : (
        <div className="rounded-lg border border-slate-700 overflow-hidden">
          <div className="max-h-64 overflow-auto">
            <table className="w-full text-xs">
              <thead className="bg-slate-900/60 text-slate-400 sticky top-0">
                <tr>
                  <th className="text-left px-2 py-1.5 font-medium">Date</th>
                  <th className="text-right px-2 py-1.5 font-medium">Close</th>
                  <th className="text-right px-2 py-1.5 font-medium">D-1 Change</th>
                </tr>
              </thead>
              <tbody>
                {last30.map((row, idx) => {
                  const prev = idx < last30.length - 1 ? last30[idx + 1] : undefined;
                  const pct = prev && prev.close > 0 ? ((row.close - prev.close) / prev.close) * 100 : 0;
                  return (
                    <tr key={`${row.timestamp}-${idx}`} className="border-t border-slate-800">
                      <td className="px-2 py-1.5 text-slate-300">{new Date(row.timestamp).toLocaleDateString()}</td>
                      <td className="px-2 py-1.5 text-right text-slate-200 font-mono">{formatPrice(row.close)}</td>
                      <td className={`px-2 py-1.5 text-right font-mono ${pct >= 0 ? "text-emerald-300" : "text-red-300"}`}>{formatPct(pct)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

// ── Main dashboard ─────────────────────────────────────────────────────────
export default function Dashboard() {
  const [symbol, setSymbol] = useState("NVDA");
  const [inputSymbol, setInputSymbol] = useState("NVDA");
  const [timingTF, setTimingTF] = useState("120");
  const [watchlistItems, setWatchlistItems] = useState<WatchlistItem[]>([]);
  const [candles, setCandles] = useState<CandleDoc[]>([]);
  const [hourlyCandles, setHourlyCandles] = useState<CandleDoc[]>([]);
  const [daily30Candles, setDaily30Candles] = useState<CandleDoc[]>([]);
  const [signal, setSignal] = useState<Signal | null>(null);
  const [loading, setLoading] = useState(false);
  const [notifying, setNotifying] = useState(false);
  const [notifyMsg, setNotifyMsg] = useState("");
  const [error, setError] = useState("");
  const [lastUpdate, setLastUpdate] = useState("");
  const [autoRefreshEnabled, setAutoRefreshEnabled] = useState(true);
  const [autoRefreshSec, setAutoRefreshSec] = useState(30);

  const [scanRows, setScanRows] = useState<ScanRow[]>([]);
  const [scanLoading, setScanLoading] = useState(false);
  const [scanError, setScanError] = useState("");
  const [scanUpdatedAt, setScanUpdatedAt] = useState("");
  const [scanActionBusy, setScanActionBusy] = useState<string | null>(null);
  const [batchAnalyzing, setBatchAnalyzing] = useState(false);
  const [batchMsg, setBatchMsg] = useState("");

  const [customUniverseSymbols, setCustomUniverseSymbols] = useState<string[]>([]);
  const [excludedUniverseSymbols, setExcludedUniverseSymbols] = useState<string[]>([]);
  const [listInputSymbol, setListInputSymbol] = useState("");

  const loadWatchlist = async () => {
    try {
      const rows = await fetchJSON<WatchlistItem[]>("/api/watchlist");
      setWatchlistItems(rows ?? []);
    } catch {
      setWatchlistItems([]);
    }
  };

  const loadData = async (sym: string, opts?: { silent?: boolean }) => {
    const silent = opts?.silent ?? false;
    if (!silent) {
      setLoading(true);
      setError("");
      setSignal(null);
      setHourlyCandles([]);
    }
    try {
      const [c, h7, d30, s] = await Promise.all([
        fetchJSON<CandleDoc[]>(`/api/candles?symbol=${sym}&timeframe=1d&limit=300`),
        fetchJSON<CandleDoc[]>(`/api/candles?symbol=${sym}&timeframe=60&limit=168`),
        fetchJSON<CandleDoc[]>(`/api/candles?symbol=${sym}&timeframe=1d&limit=30`),
        fetchJSON<Signal>(`/api/signal?symbol=${sym}&timing_tf=${timingTF}`),
      ]);
      setCandles(c);
      setHourlyCandles(h7);
      setDaily30Candles(d30);
      setSignal(s);
      setLastUpdate(new Date().toISOString().slice(11, 19));
    } catch (e) {
      setError(String(e));
    } finally {
      if (!silent) {
        setLoading(false);
      }
    }
  };

  const handleAnalyze = (sym?: string) => {
    const target = (sym ?? inputSymbol).trim().toUpperCase();
    if (!target) return;
    setInputSymbol(target);
    setSymbol(target);
    setNotifyMsg("");
    loadData(target);
  };

  const handleNotify = async () => {
    setNotifying(true);
    setNotifyMsg("");
    try {
      const res = await fetch(`${API_BASE}/api/notify?symbol=${symbol}&timing_tf=${timingTF}`, { method: "POST" });
      const d = await res.json();
      if (!res.ok) throw new Error(d.error ?? "unknown error");
      setNotifyMsg(`Sent [${d.symbol}] ${d.action} to Telegram`);
    } catch (e) {
      setNotifyMsg(`Error: ${String(e)}`);
    } finally {
      setNotifying(false);
    }
  };

  const buildManagedSymbols = () => {
    const watchlistSymbols = watchlistItems.map((x) => x.symbol.toUpperCase());
    const base = MARKET_CAP_BASE_SYMBOLS.filter((sym) => !excludedUniverseSymbols.includes(sym));
    const custom = customUniverseSymbols.filter((sym) => !excludedUniverseSymbols.includes(sym));
    const extraWatchlist = watchlistSymbols.filter((sym) => !base.includes(sym) && !custom.includes(sym));
    return Array.from(new Set([...base, ...custom, ...extraWatchlist]));
  };

  const loadUniverseScan = async () => {
    setScanLoading(true);
    setScanError("");
    try {
      const symbols = buildManagedSymbols();
      const settled = await Promise.allSettled(
        symbols.map(async (sym) => {
          const data = await fetchJSON<Signal>(`/api/signal?symbol=${sym}&timing_tf=${SCAN_TF}`);
          return { symbol: sym, signal: data };
        }),
      );

      const rows: ScanRow[] = settled
        .filter((r): r is PromiseFulfilledResult<ScanRow> => r.status === "fulfilled")
        .map((r) => r.value)
        .sort((a, b) => symbols.indexOf(a.symbol) - symbols.indexOf(b.symbol));

      setScanRows(rows);
      setScanUpdatedAt(new Date().toLocaleTimeString("ko-KR", { hour12: false }));
    } catch (e) {
      setScanRows([]);
      setScanError(String(e));
    } finally {
      setScanLoading(false);
    }
  };

  const addAlertTarget = async (sym: string, mode: "event" | "interval") => {
    setScanActionBusy(`${sym}:${mode}:add`);
    try {
      const hour = 4;
      const res = await fetch(`${API_BASE}/api/watchlist?symbol=${sym}&notify_interval_hours=${hour}&notify_mode=${mode}`, {
        method: "POST",
      });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? "failed to add watchlist");
      }
      await loadWatchlist();
      return true;
    } catch (e) {
      setBatchMsg(`등록 실패: ${String(e)}`);
      return false;
    } finally {
      setScanActionBusy(null);
    }
  };

  const removeAlertTarget = async (sym: string) => {
    setScanActionBusy(`${sym}:remove`);
    try {
      const res = await fetch(`${API_BASE}/api/watchlist?symbol=${sym}`, { method: "DELETE" });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? "failed to remove watchlist");
      }
      await loadWatchlist();
    } catch (e) {
      setBatchMsg(`해제 실패: ${String(e)}`);
    } finally {
      setScanActionBusy(null);
    }
  };

  const registerAllVisible = async (mode: "event" | "interval") => {
    setBatchAnalyzing(true);
    setBatchMsg("");
    let ok = 0;
    for (const row of scanRows) {
      const success = await addAlertTarget(row.symbol, mode);
      if (success) ok += 1;
    }
    setBatchMsg(`알림 대상 일괄 등록 완료: ${ok}/${scanRows.length} (${mode.toUpperCase()})`);
    setBatchAnalyzing(false);
  };

  const analyzeAllAlertTargets = async () => {
    const targets = watchlistItems.map((x) => x.symbol);
    if (targets.length === 0) {
      setBatchMsg("분석할 알림 대상이 없습니다.");
      return;
    }
    setBatchAnalyzing(true);
    setBatchMsg("알림 대상 순차 분석 중...");

    let buyCount = 0;
    let sellCount = 0;
    let holdCount = 0;
    for (const sym of targets) {
      try {
        const s = await fetchJSON<Signal>(`/api/signal?symbol=${sym}&timing_tf=${SCAN_TF}`);
        if (s.action === "BUY") buyCount += 1;
        else if (s.action === "SELL") sellCount += 1;
        else holdCount += 1;
      } catch {
        // keep going
      }
    }
    setBatchMsg(`순차 분석 완료: BUY ${buyCount} / HOLD ${holdCount} / SELL ${sellCount}`);
    setBatchAnalyzing(false);
    await loadUniverseScan();
  };

  const addUniverseSymbol = () => {
    const sym = listInputSymbol.trim().toUpperCase();
    if (!sym) return;

    const nextCustom = customUniverseSymbols.includes(sym) || MARKET_CAP_BASE_SYMBOLS.includes(sym)
      ? customUniverseSymbols
      : [...customUniverseSymbols, sym];
    setCustomUniverseSymbols(nextCustom);
    localStorage.setItem(CUSTOM_UNIVERSE_STORAGE_KEY, JSON.stringify(nextCustom));

    const nextExcluded = excludedUniverseSymbols.filter((x) => x !== sym);
    setExcludedUniverseSymbols(nextExcluded);
    localStorage.setItem(EXCLUDED_UNIVERSE_STORAGE_KEY, JSON.stringify(nextExcluded));
    setListInputSymbol("");
  };

  const removeUniverseSymbol = async (sym: string) => {
    if (MARKET_CAP_BASE_SYMBOLS.includes(sym)) {
      const nextExcluded = excludedUniverseSymbols.includes(sym) ? excludedUniverseSymbols : [...excludedUniverseSymbols, sym];
      setExcludedUniverseSymbols(nextExcluded);
      localStorage.setItem(EXCLUDED_UNIVERSE_STORAGE_KEY, JSON.stringify(nextExcluded));
    } else {
      const nextCustom = customUniverseSymbols.filter((x) => x !== sym);
      setCustomUniverseSymbols(nextCustom);
      localStorage.setItem(CUSTOM_UNIVERSE_STORAGE_KEY, JSON.stringify(nextCustom));
    }

    if (watchlistItems.some((x) => x.symbol === sym)) {
      await removeAlertTarget(sym);
    }
  };

  useEffect(() => {
    loadData(symbol);
  }, [timingTF]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!autoRefreshEnabled) {
      return;
    }
    const t = setInterval(() => {
      loadData(symbol, { silent: true });
    }, autoRefreshSec * 1000);
    return () => clearInterval(t);
  }, [symbol, timingTF, autoRefreshEnabled, autoRefreshSec]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    loadWatchlist();
    const rawCustom = localStorage.getItem(CUSTOM_UNIVERSE_STORAGE_KEY);
    const rawExcluded = localStorage.getItem(EXCLUDED_UNIVERSE_STORAGE_KEY);
    try {
      if (rawCustom) {
        const parsed = JSON.parse(rawCustom);
        if (Array.isArray(parsed)) setCustomUniverseSymbols(parsed.map((x) => String(x).toUpperCase()));
      }
      if (rawExcluded) {
        const parsed = JSON.parse(rawExcluded);
        if (Array.isArray(parsed)) setExcludedUniverseSymbols(parsed.map((x) => String(x).toUpperCase()));
      }
    } catch {
      // ignore invalid storage
    }
  }, []);

  useEffect(() => {
    loadUniverseScan();
    const t = setInterval(loadUniverseScan, 180_000);
    return () => clearInterval(t);
  }, [watchlistItems, customUniverseSymbols, excludedUniverseSymbols]); // eslint-disable-line react-hooks/exhaustive-deps

  const watchlistSymbols = watchlistItems.map((item) => item.symbol);
  const hasCurrentInWatchlist = watchlistSymbols.includes(symbol);
  const currentPrice = hourlyCandles[hourlyCandles.length - 1]?.close ?? candles[candles.length - 1]?.close;

  return (
    <div className="min-h-screen bg-[#0f1117] text-slate-200 p-4 md:p-6">
      <section className="glass-panel scan-grid scan-ambient rounded-2xl p-4 md:p-6 mb-5 overflow-hidden">
        <div className="flex flex-col md:flex-row md:items-end md:justify-between gap-4 mb-4">
          <div>
            <p className="text-[11px] tracking-[0.2em] uppercase text-cyan-300/80">Midas Touch Control Room</p>
            <h2 className="text-xl md:text-2xl font-bold text-white mt-1">Market Cap Priority List</h2>
            <p className="text-xs md:text-sm text-slate-400 mt-1">시총 우선 기본 리스트 + 사용자 추가/삭제 | Timing: {SCAN_TF}m</p>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[11px] text-slate-400">Updated</span>
            <span className="text-xs font-mono text-cyan-300">{scanUpdatedAt || "--:--:--"}</span>
            <input
              type="text"
              value={listInputSymbol}
              onChange={(e) => setListInputSymbol(e.target.value.toUpperCase())}
              onKeyDown={(e) => e.key === "Enter" && addUniverseSymbol()}
              placeholder="Add symbol"
              className="w-28 text-xs px-2 py-1.5 rounded border border-slate-600 bg-slate-900/70 text-slate-200"
            />
            <button onClick={addUniverseSymbol} className="text-xs px-3 py-1.5 rounded border border-emerald-500/40 bg-emerald-500/10 text-emerald-200 hover:bg-emerald-500/20 transition-colors">리스트 추가</button>
            <button onClick={loadUniverseScan} disabled={scanLoading} className="text-xs px-3 py-1.5 rounded border border-cyan-400/40 bg-cyan-400/10 text-cyan-200 hover:bg-cyan-400/20 transition-colors disabled:opacity-60">{scanLoading ? "Scanning..." : "Rescan"}</button>
            <button onClick={() => registerAllVisible("event")} disabled={batchAnalyzing || scanRows.length === 0} className="text-xs px-3 py-1.5 rounded border border-cyan-500/40 bg-cyan-500/10 text-cyan-200 hover:bg-cyan-500/20 transition-colors disabled:opacity-60">일괄등록 E</button>
            <button onClick={() => registerAllVisible("interval")} disabled={batchAnalyzing || scanRows.length === 0} className="text-xs px-3 py-1.5 rounded border border-amber-500/40 bg-amber-500/10 text-amber-200 hover:bg-amber-500/20 transition-colors disabled:opacity-60">일괄등록 I</button>
            <button onClick={analyzeAllAlertTargets} disabled={batchAnalyzing || watchlistItems.length === 0} className="text-xs px-3 py-1.5 rounded border border-indigo-400/40 bg-indigo-500/10 text-indigo-200 hover:bg-indigo-500/20 transition-colors disabled:opacity-60">{batchAnalyzing ? "분석중..." : "알림대상 전체 분석"}</button>
          </div>
        </div>
        {batchMsg && <div className="mb-3 rounded border border-slate-700 bg-slate-900/50 px-3 py-2 text-xs text-slate-300">{batchMsg}</div>}

        <div className="grid grid-cols-1 xl:grid-cols-12 gap-4">
          <div className="xl:col-span-4 rounded-xl border border-slate-700/90 bg-[#0c1422]/80 p-4">
            <p className="text-[11px] text-slate-500 uppercase tracking-wide">Live Summary</p>
            <p className="text-3xl font-black text-white mt-2">{scanRows.length}</p>
            <p className="text-xs text-slate-400">managed symbols</p>
            <div className="mt-4 space-y-2 text-xs">
              <div className="flex items-center justify-between"><span className="text-slate-500">Base</span><span className="font-mono text-slate-300">{MARKET_CAP_BASE_SYMBOLS.length}</span></div>
              <div className="flex items-center justify-between"><span className="text-slate-500">Custom Added</span><span className="font-mono text-slate-300">{customUniverseSymbols.length}</span></div>
              <div className="flex items-center justify-between"><span className="text-slate-500">Excluded</span><span className="font-mono text-slate-300">{excludedUniverseSymbols.length}</span></div>
            </div>
            {scanRows[0] && (
              <div className="mt-4 rounded-lg border border-emerald-400/30 bg-emerald-400/10 p-3">
                <p className="text-[11px] text-emerald-300 uppercase tracking-wide">Top Of List</p>
                <button onClick={() => handleAnalyze(scanRows[0].symbol)} className="mt-1 text-left w-full">
                  <p className="text-lg font-black text-white">{scanRows[0].symbol}</p>
                  <p className="text-xs text-emerald-200">Buy {scanRows[0].signal.buy_pct.toFixed(0)}%</p>
                </button>
              </div>
            )}
          </div>

          <div className="xl:col-span-8 rounded-xl border border-slate-700/90 bg-[#0b1320]/85 overflow-hidden">
            <div className="max-h-[420px] overflow-auto">
              <table className="w-full text-xs md:text-sm">
                <thead className="sticky top-0 bg-[#0e1b2c] text-slate-400">
                  <tr>
                    <th className="text-left px-3 py-2 font-medium">Symbol</th>
                    <th className="text-right px-3 py-2 font-medium">Buy</th>
                    <th className="text-right px-3 py-2 font-medium">Hold</th>
                    <th className="text-right px-3 py-2 font-medium">Sell</th>
                    <th className="text-left px-3 py-2 font-medium">Direction</th>
                    <th className="text-left px-3 py-2 font-medium">Timing</th>
                    <th className="text-left px-3 py-2 font-medium">Alert</th>
                    <th className="text-left px-3 py-2 font-medium">List</th>
                  </tr>
                </thead>
                <tbody>
                  {scanRows.map((row) => {
                    const wl = watchlistItems.find((x) => x.symbol === row.symbol);
                    const isOn = Boolean(wl);
                    const busy = scanActionBusy?.startsWith(`${row.symbol}:`);
                    return (
                      <tr key={row.symbol} className="border-t border-slate-800 hover:bg-cyan-500/5 transition-colors cursor-pointer" onClick={() => handleAnalyze(row.symbol)}>
                        <td className="px-3 py-2 font-mono text-white">{actionEmoji(row.signal.action)} {row.symbol}</td>
                        <td className={`px-3 py-2 text-right font-mono ${scanScoreClass(row.signal.buy_pct)}`}>{row.signal.buy_pct.toFixed(0)}%</td>
                        <td className="px-3 py-2 text-right font-mono text-slate-400">{row.signal.hold_pct.toFixed(0)}%</td>
                        <td className="px-3 py-2 text-right font-mono text-slate-400">{row.signal.sell_pct.toFixed(0)}%</td>
                        <td className={`px-3 py-2 ${actionColor(row.signal.trend_action ?? row.signal.action)}`}>{row.signal.trend_action ?? row.signal.action}</td>
                        <td className={`px-3 py-2 ${actionColor(row.signal.timing_action ?? row.signal.action)}`}>{row.signal.timing_action ?? row.signal.action}</td>
                        <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                          <div className="flex items-center gap-1.5">
                            <span className={`text-[10px] px-1.5 py-0.5 rounded border ${isOn ? "border-emerald-500/50 text-emerald-300" : "border-slate-600 text-slate-400"}`}>{isOn ? `ON ${notifyModeLabel(wl?.notify_mode)}` : "OFF"}</span>
                            {!isOn && (
                              <>
                                <button onClick={() => addAlertTarget(row.symbol, "event")} disabled={busy} className="text-[10px] px-1.5 py-0.5 rounded border border-cyan-500/40 text-cyan-200 hover:bg-cyan-500/20 disabled:opacity-50">E+</button>
                                <button onClick={() => addAlertTarget(row.symbol, "interval")} disabled={busy} className="text-[10px] px-1.5 py-0.5 rounded border border-amber-500/40 text-amber-200 hover:bg-amber-500/20 disabled:opacity-50">I+</button>
                              </>
                            )}
                            {isOn && <button onClick={() => removeAlertTarget(row.symbol)} disabled={busy} className="text-[10px] px-1.5 py-0.5 rounded border border-red-500/40 text-red-200 hover:bg-red-500/20 disabled:opacity-50">해제</button>}
                          </div>
                        </td>
                        <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                          <button onClick={() => removeUniverseSymbol(row.symbol)} className="text-[10px] px-1.5 py-0.5 rounded border border-slate-600 text-slate-300 hover:border-red-400 hover:text-red-300">삭제</button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              {!scanLoading && scanRows.length === 0 && <div className="p-6 text-center text-slate-500 text-xs">리스트에 표시할 종목이 없습니다.</div>}
              {scanLoading && <div className="p-6 text-center text-cyan-300 text-xs">스캔 중...</div>}
              {scanError && <div className="p-4 text-center text-red-300 text-xs">{scanError}</div>}
            </div>
          </div>
        </div>
      </section>

      <div className="sticky top-2 z-30 mb-3">
        <div className="inline-flex items-center gap-2 rounded-full border border-indigo-400/40 bg-slate-900/90 backdrop-blur px-3 py-1.5 shadow-lg">
          <span className="text-[11px] text-slate-400">현재가</span>
          <span className="text-xs font-mono text-indigo-300">{symbol}</span>
          <span className="text-sm font-black text-white">{typeof currentPrice === "number" ? formatPrice(currentPrice) : "-"}</span>
          {lastUpdate && <span className="text-[10px] text-slate-500">{lastUpdate}</span>}
        </div>
      </div>

      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-6">
        <div>
          <h1 className="text-2xl font-bold text-white">✨ Midas Touch</h1>
          <p className="text-sm text-slate-400">Signal Dashboard</p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <input
            type="text"
            value={inputSymbol}
            onChange={(e) => setInputSymbol(e.target.value.toUpperCase())}
            onKeyDown={(e) => e.key === "Enter" && handleAnalyze()}
            placeholder="NVDA, AAPL, TSLA..."
            className="px-3 py-2 bg-slate-800 border border-slate-600 text-white text-sm rounded-lg w-36 focus:outline-none focus:border-indigo-500"
          />
          <button onClick={() => handleAnalyze()} disabled={loading} className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm rounded-lg transition-colors disabled:opacity-50">{loading ? "Loading..." : "Analyze"}</button>
          <select value={timingTF} onChange={(e) => setTimingTF(e.target.value)} className="px-2 py-2 bg-slate-800 border border-slate-600 text-white text-sm rounded-lg focus:outline-none focus:border-indigo-500">
            <option value="60">Timing 1H</option>
            <option value="120">Timing 2H</option>
            <option value="240">Timing 4H</option>
          </select>
          <div className="inline-flex items-center gap-1 rounded-lg border border-slate-600 bg-slate-800 px-2 py-1.5">
            <button
              onClick={() => setAutoRefreshEnabled((v) => !v)}
              className={`text-[11px] px-2 py-0.5 rounded border transition-colors ${autoRefreshEnabled ? "border-emerald-500/50 bg-emerald-500/20 text-emerald-200" : "border-slate-600 bg-slate-700 text-slate-400"}`}
            >
              Auto {autoRefreshEnabled ? "ON" : "OFF"}
            </button>
            <select
              value={autoRefreshSec}
              onChange={(e) => setAutoRefreshSec(Number(e.target.value))}
              disabled={!autoRefreshEnabled}
              className="text-[11px] px-1.5 py-0.5 rounded bg-slate-700 border border-slate-600 text-slate-200 disabled:opacity-50"
            >
              <option value={10}>10s</option>
              <option value={30}>30s</option>
              <option value={60}>60s</option>
            </select>
          </div>
          {watchlistSymbols.length > 0 && (
            <select value={symbol} onChange={(e) => handleAnalyze(e.target.value)} className="px-2 py-2 bg-slate-800 border border-slate-600 text-white text-sm rounded-lg focus:outline-none focus:border-indigo-500">
              {!hasCurrentInWatchlist && <option value={symbol}>{symbol}</option>}
              {watchlistSymbols.map((sym) => <option key={sym} value={sym}>{sym}</option>)}
            </select>
          )}
          <button onClick={handleNotify} disabled={notifying || !signal} className="px-4 py-2 bg-emerald-700 hover:bg-emerald-600 text-white text-sm rounded-lg transition-colors disabled:opacity-50">{notifying ? "Sending..." : "Send to Telegram"}</button>
          {lastUpdate && <span className="text-xs text-slate-500">Updated: {lastUpdate}</span>}
        </div>
      </div>

      {watchlistSymbols.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-2">
          {watchlistSymbols.map((sym) => (
            <button
              key={sym}
              onClick={() => handleAnalyze(sym)}
              className={`text-xs px-2.5 py-1 rounded border transition-colors ${sym === symbol ? "border-indigo-400 bg-indigo-500/20 text-indigo-200" : "border-slate-600 bg-slate-800 text-slate-300 hover:border-indigo-500"}`}
            >
              {sym}
            </button>
          ))}
        </div>
      )}

      {notifyMsg && <div className={`mb-3 p-2 rounded-lg text-xs text-center ${notifyMsg.startsWith("Error") ? "bg-red-500/20 text-red-300" : "bg-emerald-500/20 text-emerald-300"}`}>{notifyMsg}</div>}
      {error && <div className="mb-4 p-3 bg-red-500/20 border border-red-500/40 rounded-lg text-red-300 text-sm">{error}</div>}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="lg:col-span-2 space-y-4">
          <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <h2 className="font-semibold text-slate-200">{symbol} Daily Chart</h2>
              {signal && <span className={`text-xs font-bold px-2 py-1 rounded border ${actionBg(signal.action)}`}><span className={actionColor(signal.action)}>{signal.action}</span></span>}
            </div>
            {candles.length > 0 ? <CandleChart candles={candles} /> : <div className="h-[300px] flex items-center justify-center text-slate-500">{loading ? "Loading chart..." : "No data"}</div>}
          </div>

          {signal?.score && (
            <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
              <h2 className="font-semibold text-slate-200 mb-3">Signal Score Breakdown</h2>
              <div className="space-y-2">
                <ScoreBar label="Supertrend (35%)" value={signal.score.SupertrendScore} />
                <ScoreBar label="MA Cross (30%)" value={signal.score.MAScore} />
                <ScoreBar label="Bollinger (20%)" value={signal.score.BBScore} />
                <ScoreBar label="RSI (15%)" value={signal.score.RSIScore} />
                <div className="border-t border-slate-700 pt-2 mt-1"><ScoreBar label="Total" value={signal.score.Total} /></div>
              </div>
              <p className="text-xs text-slate-500 mt-3 leading-relaxed">{signal.reason}</p>
            </div>
          )}

          <Hourly7DayPanel symbol={symbol} candles={hourlyCandles} />
          <DailyClose30Panel symbol={symbol} candles={daily30Candles} />
        </div>

        <div className="space-y-4">
          {signal && (
            <div className={`rounded-xl border p-4 ${actionBg(signal.action)}`}>
              <div className="flex items-center justify-between mb-3"><h2 className="font-semibold text-slate-200">Overall Signal</h2><span className={`text-2xl font-black ${actionColor(signal.action)}`}>{signal.action}</span></div>
              <div className="flex items-center gap-2 mb-2 text-xs text-slate-300">
                <span className="px-2 py-0.5 rounded bg-slate-800/70 border border-slate-600">D: {signal.trend_action ?? signal.action}</span>
                <span className="px-2 py-0.5 rounded bg-slate-800/70 border border-slate-600">H: {signal.timing_action ?? signal.action}</span>
                {signal.is_special_signal && <span className="px-2 py-0.5 rounded bg-amber-500/20 border border-amber-500/40 text-amber-300">Special</span>}
              </div>
              <ProbBar buy={signal.buy_pct} sell={signal.sell_pct} hold={signal.hold_pct} />
              <p className="text-xs text-slate-500 mt-2">{signal.timestamp ? new Date(signal.timestamp).toISOString().replace("T", " ").slice(0, 19) : ""}</p>
            </div>
          )}

          {signal?.indicators && (
            <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
              <h2 className="font-semibold text-slate-200 mb-3">Technical Indicators</h2>
              <div className="space-y-1 text-sm">
                <IndicatorRow
                  label="RSI (14)"
                  value={signal.indicators.RSI14.toFixed(1)}
                  badge={signal.indicators.RSI14 > 70 ? { text: "Overbought", cls: "text-red-400" } : signal.indicators.RSI14 < 30 ? { text: "Oversold", cls: "text-emerald-400" } : { text: "Neutral", cls: "text-slate-400" }}
                />
                <IndicatorRow label="SMA 20" value={`$${signal.indicators.SMA20.toFixed(2)}`} />
                <IndicatorRow label="SMA 50" value={`$${signal.indicators.SMA50.toFixed(2)}`} />
                <div className="border-t border-slate-700 pt-2 mt-1">
                  <p className="text-xs text-slate-500 mb-1">Bollinger Bands</p>
                  <IndicatorRow label="Upper" value={`$${signal.indicators.BBUpper.toFixed(2)}`} />
                  <IndicatorRow label="Mid" value={`$${signal.indicators.BBMid.toFixed(2)}`} />
                  <IndicatorRow label="Lower" value={`$${signal.indicators.BBLower.toFixed(2)}`} />
                </div>
                <div className="border-t border-slate-700 pt-2 mt-1">
                  <IndicatorRow label="Supertrend" value={`$${signal.indicators.SupertrendLine.toFixed(2)}`} badge={signal.indicators.SupertrendDir > 0 ? { text: "Bullish", cls: "text-emerald-400" } : { text: "Bearish", cls: "text-red-400" }} />
                  <IndicatorRow label="ATR (14)" value={signal.indicators.ATR14.toFixed(2)} />
                </div>
              </div>
            </div>
          )}

          <SourceStatusPanel />
          <DBStatsPanel />
        </div>
      </div>
    </div>
  );
}
