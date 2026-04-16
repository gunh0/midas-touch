"use client";

import { useEffect, useRef, useState } from "react";

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

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
  buy_pct: number;
  sell_pct: number;
  hold_pct: number;
  reason: string;
  indicators: Indicators;
  score: Score;
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
}

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
function WatchlistPanel({ onSelect }: { onSelect: (symbol: string) => void }) {
  const [items, setItems] = useState<WatchlistItem[]>([]);
  const [input, setInput] = useState("");
  const [saving, setSaving] = useState(false);

  const load = async () => {
    try {
      const data = await fetchJSON<WatchlistItem[]>("/api/watchlist");
      setItems(data ?? []);
    } catch { /* ignore */ }
  };

  useEffect(() => { load(); }, []);

  const add = async () => {
    const sym = input.trim().toUpperCase();
    if (!sym) return;
    setSaving(true);
    try {
      await fetch(`${API_BASE}/api/watchlist?symbol=${sym}`, { method: "POST" });
      setInput("");
      await load();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (sym: string) => {
    await fetch(`${API_BASE}/api/watchlist?symbol=${sym}`, { method: "DELETE" });
    await load();
  };

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <h3 className="text-sm font-semibold text-slate-300 mb-3">Watchlist</h3>
      <div className="flex gap-2 mb-3">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value.toUpperCase())}
          onKeyDown={(e) => e.key === "Enter" && add()}
          placeholder="Add symbol..."
          className="flex-1 px-2 py-1.5 bg-slate-700 border border-slate-600 text-white text-xs rounded focus:outline-none focus:border-indigo-500"
        />
        <button
          onClick={add}
          disabled={saving}
          className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 text-white text-xs rounded transition-colors disabled:opacity-50"
        >
          Add
        </button>
      </div>
      {items.length === 0 ? (
        <p className="text-xs text-slate-500 text-center py-2">No symbols saved</p>
      ) : (
        <div className="space-y-1">
          {items.map((item) => (
            <div key={item.symbol} className="flex items-center justify-between group">
              <button
                onClick={() => onSelect(item.symbol)}
                className="text-sm font-mono text-indigo-400 hover:text-indigo-300 transition-colors"
              >
                {item.symbol}
              </button>
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

// ── Main dashboard ─────────────────────────────────────────────────────────
export default function Dashboard() {
  const [symbol, setSymbol] = useState("NVDA");
  const [inputSymbol, setInputSymbol] = useState("NVDA");
  const [candles, setCandles] = useState<CandleDoc[]>([]);
  const [signal, setSignal] = useState<Signal | null>(null);
  const [loading, setLoading] = useState(false);
  const [notifying, setNotifying] = useState(false);
  const [notifyMsg, setNotifyMsg] = useState("");
  const [error, setError] = useState("");
  const [lastUpdate, setLastUpdate] = useState("");

  const loadData = async (sym: string) => {
    setLoading(true);
    setError("");
    setSignal(null);
    try {
      const [c, s] = await Promise.all([
        fetchJSON<CandleDoc[]>(`/api/candles?symbol=${sym}&limit=300`),
        fetchJSON<Signal>(`/api/signal?symbol=${sym}`),
      ]);
      setCandles(c);
      setSignal(s);
      setLastUpdate(new Date().toISOString().slice(11, 19));
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
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
      const res = await fetch(`${API_BASE}/api/notify?symbol=${symbol}`, { method: "POST" });
      const d = await res.json();
      if (!res.ok) throw new Error(d.error ?? "unknown error");
      setNotifyMsg(`Sent [${d.symbol}] ${d.action} to Telegram`);
    } catch (e) {
      setNotifyMsg(`Error: ${String(e)}`);
    } finally {
      setNotifying(false);
    }
  };

  useEffect(() => {
    loadData(symbol);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="min-h-screen bg-[#0f1117] text-slate-200 p-4 md:p-6">
      {/* Header */}
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
          <button
            onClick={() => handleAnalyze()}
            disabled={loading}
            className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm rounded-lg transition-colors disabled:opacity-50"
          >
            {loading ? "Loading..." : "Analyze"}
          </button>
          <button
            onClick={handleNotify}
            disabled={notifying || !signal}
            className="px-4 py-2 bg-emerald-700 hover:bg-emerald-600 text-white text-sm rounded-lg transition-colors disabled:opacity-50"
          >
            {notifying ? "Sending..." : "Send to Telegram"}
          </button>
          {lastUpdate && <span className="text-xs text-slate-500">Updated: {lastUpdate}</span>}
        </div>
      </div>

      {notifyMsg && (
        <div className={`mb-3 p-2 rounded-lg text-xs text-center ${notifyMsg.startsWith("Error") ? "bg-red-500/20 text-red-300" : "bg-emerald-500/20 text-emerald-300"}`}>
          {notifyMsg}
        </div>
      )}

      {error && (
        <div className="mb-4 p-3 bg-red-500/20 border border-red-500/40 rounded-lg text-red-300 text-sm">
          {error}
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Left: Chart + Score breakdown */}
        <div className="lg:col-span-2 space-y-4">
          <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <h2 className="font-semibold text-slate-200">{symbol} Daily Chart</h2>
              {signal && (
                <span className={`text-xs font-bold px-2 py-1 rounded border ${actionBg(signal.action)}`}>
                  <span className={actionColor(signal.action)}>{signal.action}</span>
                </span>
              )}
            </div>
            {candles.length > 0 ? (
              <CandleChart candles={candles} />
            ) : (
              <div className="h-[300px] flex items-center justify-center text-slate-500">
                {loading ? "Loading chart..." : "No data"}
              </div>
            )}
          </div>

          {signal?.score && (
            <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
              <h2 className="font-semibold text-slate-200 mb-3">Signal Score Breakdown</h2>
              <div className="space-y-2">
                <ScoreBar label="Supertrend (35%)" value={signal.score.SupertrendScore} />
                <ScoreBar label="MA Cross (30%)" value={signal.score.MAScore} />
                <ScoreBar label="Bollinger (20%)" value={signal.score.BBScore} />
                <ScoreBar label="RSI (15%)" value={signal.score.RSIScore} />
                <div className="border-t border-slate-700 pt-2 mt-1">
                  <ScoreBar label="Total" value={signal.score.Total} />
                </div>
              </div>
              <p className="text-xs text-slate-500 mt-3 leading-relaxed">{signal.reason}</p>
            </div>
          )}
        </div>

        {/* Right: Signal + Indicators + Watchlist + DB */}
        <div className="space-y-4">
          {signal && (
            <div className={`rounded-xl border p-4 ${actionBg(signal.action)}`}>
              <div className="flex items-center justify-between mb-3">
                <h2 className="font-semibold text-slate-200">Overall Signal</h2>
                <span className={`text-2xl font-black ${actionColor(signal.action)}`}>{signal.action}</span>
              </div>
              <ProbBar buy={signal.buy_pct} sell={signal.sell_pct} hold={signal.hold_pct} />
              <p className="text-xs text-slate-500 mt-2">
                {signal.timestamp ? new Date(signal.timestamp).toISOString().replace("T", " ").slice(0, 19) : ""}
              </p>
            </div>
          )}

          {signal?.indicators && (
            <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
              <h2 className="font-semibold text-slate-200 mb-3">Technical Indicators</h2>
              <div className="space-y-1 text-sm">
                <IndicatorRow
                  label="RSI (14)"
                  value={signal.indicators.RSI14.toFixed(1)}
                  badge={
                    signal.indicators.RSI14 > 70 ? { text: "Overbought", cls: "text-red-400" }
                    : signal.indicators.RSI14 < 30 ? { text: "Oversold", cls: "text-emerald-400" }
                    : { text: "Neutral", cls: "text-slate-400" }
                  }
                />
                <IndicatorRow label="SMA 20" value={`$${signal.indicators.SMA20.toFixed(2)}`} />
                <IndicatorRow label="SMA 50" value={`$${signal.indicators.SMA50.toFixed(2)}`} />
                <div className="border-t border-slate-700 pt-2 mt-1">
                  <p className="text-xs text-slate-500 mb-1">Bollinger Bands</p>
                  <IndicatorRow label="Upper" value={`$${signal.indicators.BBUpper.toFixed(2)}`} />
                  <IndicatorRow label="Mid"   value={`$${signal.indicators.BBMid.toFixed(2)}`} />
                  <IndicatorRow label="Lower" value={`$${signal.indicators.BBLower.toFixed(2)}`} />
                </div>
                <div className="border-t border-slate-700 pt-2 mt-1">
                  <IndicatorRow
                    label="Supertrend"
                    value={`$${signal.indicators.SupertrendLine.toFixed(2)}`}
                    badge={signal.indicators.SupertrendDir > 0 ? { text: "Bullish", cls: "text-emerald-400" } : { text: "Bearish", cls: "text-red-400" }}
                  />
                  <IndicatorRow label="ATR (14)" value={signal.indicators.ATR14.toFixed(2)} />
                </div>
              </div>
            </div>
          )}

          <WatchlistPanel onSelect={(sym) => handleAnalyze(sym)} />
          <DBStatsPanel />
        </div>
      </div>
    </div>
  );
}
