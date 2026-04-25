"use client";

import { useCallback, useEffect, useState } from "react";
import { API_BASE, fetchJSON } from "./dashboard/api";
import { CandleChart } from "./dashboard/components/CandleChart";
import { DailyClose30Panel } from "./dashboard/components/DailyClose30Panel";
import { DBStatsPanel } from "./dashboard/components/DBStatsPanel";
import { Hourly7DayPanel } from "./dashboard/components/Hourly7DayPanel";
import { IndicatorRow } from "./dashboard/components/IndicatorRow";
import { ProbBar } from "./dashboard/components/ProbBar";
import { ScoreBar } from "./dashboard/components/ScoreBar";
import { SourceStatusPanel } from "./dashboard/components/SourceStatusPanel";
import type {
  CandleDoc,
  ScanRow,
  Signal,
  SymbolSearchResult,
  UniverseSymbol,
  ViewHistoryRow,
  WatchlistItem,
} from "./dashboard/types";
import {
  buildExecutionPlan,
  actionBg,
  actionColor,
  actionEmoji,
  formatPrice,
  formatPct,
  inferCountryFromExchange,
  intervalShortLabel,
  normalizeTypeLabel,
  notifyModeLabel,
  scanScoreClass,
  topListScore,
  typeBadgeClass,
} from "./dashboard/utils";

const MARKET_CAP_BASE_SYMBOLS = [
  "TQQQ", "QQQ", "SPY", "SOXL", "SOXX",
  "NVDA", "TSLA", "AAPL", "MSFT", "AMZN", "META", "GOOGL", "AMD", "PLTR", "SMCI",
];

const SCAN_TF = "120";

type FavoriteAnalyzeNotifyResult = {
  ok: boolean;
  target_count: number;
  sent_count: number;
  buy_count: number;
  hold_count: number;
  sell_count: number;
  failed?: Array<{ symbol?: string; stage?: string; error?: string }>;
};

const DASHBOARD_PREFS_STORAGE_KEY = "midas.dashboard.preferences";
const INTERVAL_OPTIONS_MINUTES = [
  3,
  5,
  10,
  15,
  30,
  60,
  120,
  180,
  240,
  360,
  480,
  720,
  1440,
];
const DEFAULT_INTERVAL_MINUTE = 720;

// ── Main dashboard ─────────────────────────────────────────────────────────
export default function Dashboard() {
  const [symbol, setSymbol] = useState("NVDA");
  const [inputSymbol, setInputSymbol] = useState("NVDA");
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
  const [autoRefreshSec, setAutoRefreshSec] = useState(60);
  const [prefsReady, setPrefsReady] = useState(false);
  const [defaultSymbolSeeded, setDefaultSymbolSeeded] = useState(false);

  const [scanRows, setScanRows] = useState<ScanRow[]>([]);
  const [scanLoading, setScanLoading] = useState(false);
  const [scanError, setScanError] = useState("");
  const [scanUpdatedAt, setScanUpdatedAt] = useState("");
  const [scanActionBusy, setScanActionBusy] = useState<string | null>(null);
  const [batchAnalyzing, setBatchAnalyzing] = useState(false);
  const [batchMsg, setBatchMsg] = useState("");

  const [universeSymbols, setUniverseSymbols] = useState<UniverseSymbol[]>([]);
  const [viewHistory, setViewHistory] = useState<string[]>([]);
  const [historyBusy, setHistoryBusy] = useState<string | null>(null);
  const [pendingIntervalBySymbol, setPendingIntervalBySymbol] = useState<Record<string, number>>({});
  const [listSearchResults, setListSearchResults] = useState<SymbolSearchResult[]>([]);
  const [listSearching, setListSearching] = useState(false);

  const loadUniverse = useCallback(async () => {
    try {
      const rows = await fetchJSON<UniverseSymbol[]>("/api/universe");
      setUniverseSymbols(rows ?? []);
    } catch {
      setUniverseSymbols([]);
    }
  }, []);

  const loadViewHistory = useCallback(async () => {
    try {
      const rows = await fetchJSON<ViewHistoryRow[]>("/api/view-history?limit=20");
      setViewHistory((rows ?? []).map((x) => String(x.symbol).toUpperCase()));
    } catch {
      setViewHistory([]);
    }
  }, []);

  const touchViewHistory = useCallback(async (sym: string) => {
    const normalized = sym.trim().toUpperCase();
    if (!normalized) return;
    setViewHistory((prev) => [normalized, ...prev.filter((x) => x !== normalized)].slice(0, 20));
    try {
      await fetch(`${API_BASE}/api/view-history?symbol=${normalized}`, { method: "POST" });
    } catch {
      // ignore
    }
  }, []);

  const removeViewHistorySymbol = useCallback(async (sym: string) => {
    const normalized = sym.trim().toUpperCase();
    if (!normalized || historyBusy === normalized) {
      return;
    }

    const prev = viewHistory;
    setHistoryBusy(normalized);
    setViewHistory((rows) => rows.filter((x) => x !== normalized));

    try {
      const res = await fetch(`${API_BASE}/api/view-history?symbol=${normalized}`, { method: "DELETE" });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? "unknown error");
      }
    } catch {
      setViewHistory(prev);
    } finally {
      setHistoryBusy(null);
    }
  }, [historyBusy, viewHistory]);

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
        fetchJSON<Signal>(`/api/signal?symbol=${sym}`),
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
    setNotifyMsg("");
    touchViewHistory(target);
    if (target === symbol) {
      loadData(target);
      return;
    }
    setSymbol(target);
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

  const buildManagedSymbols = () => {
    return Array.from(new Set(universeSymbols.map((x) => String(x.symbol).toUpperCase())));
  };

  const loadUniverseScan = async () => {
    setScanLoading(true);
    setScanError("");
    try {
      const symbols = buildManagedSymbols();
      if (symbols.length === 0) {
        setScanRows([]);
        setScanUpdatedAt(new Date().toLocaleTimeString("en-US", { hour12: false }));
        return;
      }

      const batchRes = await fetch(`${API_BASE}/api/signals/batch?timing_tf=${SCAN_TF}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ symbols }),
      });
      if (!batchRes.ok) {
        const d = await batchRes.json().catch(() => ({}));
        throw new Error(d.error ?? `batch signals -> ${batchRes.status}`);
      }
      const batchRows = (await batchRes.json()) as Array<Signal & { error?: string }>;
      const signalBySymbol = new Map<string, Signal>();
      for (const row of batchRows) {
        if (row && !row.error && row.symbol) {
          signalBySymbol.set(String(row.symbol).toUpperCase(), row);
        }
      }

      const metaCache = new Map(
        scanRows.map((r) => [r.symbol.toUpperCase(), { companyName: r.companyName, exchange: r.exchange, typeLabel: r.typeLabel }]),
      );
      const settled = await Promise.allSettled(
        symbols.map(async (sym) => {
          const data = signalBySymbol.get(sym);
          if (!data) {
            throw new Error(`missing batch signal: ${sym}`);
          }
          const cached = metaCache.get(sym);
          if (cached && (cached.companyName || cached.exchange || cached.typeLabel)) {
            return {
              symbol: sym,
              signal: data,
              companyName: cached.companyName,
              exchange: cached.exchange,
              typeLabel: cached.typeLabel,
            };
          }

          const searchRows = await fetchJSON<SymbolSearchResult[]>(`/api/symbols/search?q=${encodeURIComponent(sym)}&limit=5`).catch(() => [] as SymbolSearchResult[]);
          const exact = (searchRows ?? []).find((x) => x.symbol.toUpperCase() === sym);
          const fallback = (searchRows ?? [])[0];
          const picked = exact ?? fallback;
          return {
            symbol: sym,
            signal: data,
            companyName: picked?.name || "",
            exchange: picked?.exchange || "",
            typeLabel: normalizeTypeLabel(picked?.type_display),
          };
        }),
      );

      const rows: ScanRow[] = settled
        .filter((r): r is PromiseFulfilledResult<ScanRow> => r.status === "fulfilled")
        .map((r) => r.value)
        .sort((a, b) => {
          const scoreGap = topListScore(b) - topListScore(a);
          if (scoreGap !== 0) return scoreGap;
          const buyGap = b.signal.buy_pct - a.signal.buy_pct;
          if (buyGap !== 0) return buyGap;
          return a.symbol.localeCompare(b.symbol);
        });

      setScanRows(rows);
      setScanUpdatedAt(new Date().toLocaleTimeString("en-US", { hour12: false }));
    } catch (e) {
      setScanRows([]);
      setScanError(String(e));
    } finally {
      setScanLoading(false);
    }
  };

  const addAlertTarget = async (sym: string, mode: "event" | "interval", selectedMinute?: number) => {
    setScanActionBusy(`${sym}:${mode}:add`);
    try {
      const minute = mode === "interval" ? (selectedMinute ?? DEFAULT_INTERVAL_MINUTE) : 5;
      const res = await fetch(`${API_BASE}/api/watchlist?symbol=${sym}&notify_interval_minutes=${minute}&notify_mode=${mode}`, {
        method: "POST",
      });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? "failed to add watchlist");
      }
      await loadWatchlist();
      return true;
    } catch (e) {
      setBatchMsg(`Failed to add alert target: ${String(e)}`);
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
      setBatchMsg(`Failed to remove alert target: ${String(e)}`);
    } finally {
      setScanActionBusy(null);
    }
  };

  const analyzeAllFavorites = async () => {
    const favorites = watchlistItems.filter((x) => x.pinned).map((x) => x.symbol);
    if (favorites.length === 0) {
      setBatchMsg("No favorites to analyze.");
      return;
    }
    setBatchAnalyzing(true);
    setBatchMsg("Analyzing favorites and sending Telegram messages sequentially...");

    try {
      const res = await fetch(`${API_BASE}/api/watchlist/favorites/analyze-notify?timing_tf=${SCAN_TF}`, { method: "POST" });
      const d = (await res.json().catch(() => ({}))) as Partial<FavoriteAnalyzeNotifyResult> & { error?: string };
      if (!res.ok) {
        throw new Error(d.error ?? "failed to analyze favorites");
      }

      const failedCount = Array.isArray(d.failed) ? d.failed.length : 0;
      const baseMsg = `Favorites analysis complete: target ${d.target_count ?? 0} | Sent ${d.sent_count ?? 0} | BUY ${d.buy_count ?? 0} / HOLD ${d.hold_count ?? 0} / SELL ${d.sell_count ?? 0}`;
      setBatchMsg(failedCount > 0 ? `${baseMsg} | Failed ${failedCount}` : baseMsg);
    } catch (e) {
      setBatchMsg(`Failed to analyze favorites: ${String(e)}`);
    } finally {
      setBatchAnalyzing(false);
      await loadUniverseScan();
    }
  };

  const commitUniverseSymbol = async (sym: string) => {
    const normalized = sym.trim().toUpperCase();
    if (!normalized || universeSymbols.some((x) => x.symbol === normalized)) {
      return;
    }
    const kind = MARKET_CAP_BASE_SYMBOLS.includes(normalized) ? "base" : "custom";
    const res = await fetch(`${API_BASE}/api/universe?symbol=${normalized}&kind=${kind}`, { method: "POST" });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      setBatchMsg(`Failed to add symbol: ${d.error ?? "unknown error"}`);
      return;
    }
    await loadUniverse();
    setBatchMsg(`Added to managed list: ${normalized}`);
  };

  const addUniverseSymbol = async () => {
    const query = inputSymbol.trim();
    const sym = query.toUpperCase();
    if (!sym) return;

    try {
      setListSearching(true);
      const results = await fetchJSON<SymbolSearchResult[]>(`/api/symbols/search?q=${encodeURIComponent(query)}&limit=8`);
      setListSearchResults(results ?? []);

      const exact = (results ?? []).find((r) => r.symbol.toUpperCase() === sym);
      if (exact) {
        await commitUniverseSymbol(exact.symbol);
        setInputSymbol(exact.symbol.toUpperCase());
        setListSearchResults([]);
        return;
      }

      if ((results ?? []).length > 0) {
        await commitUniverseSymbol(results[0].symbol);
        setInputSymbol(results[0].symbol.toUpperCase());
        setListSearchResults([]);
        return;
      }

      setBatchMsg(`No results found for '${query}', nothing added.`);
    } catch (e) {
      setBatchMsg(`Symbol validation failed: ${String(e)}`);
    } finally {
      setListSearching(false);
    }
  };

  const removeUniverseSymbol = async (sym: string) => {
    if (scanActionBusy === `${sym}:delete`) {
      return;
    }

    const prevScanRows = scanRows;
    const prevUniverse = universeSymbols;
    const prevWatchlist = watchlistItems;

    setScanActionBusy(`${sym}:delete`);
    setScanRows((prev) => prev.filter((row) => row.symbol !== sym));
    setUniverseSymbols((prev) => prev.filter((row) => row.symbol !== sym));
    setWatchlistItems((prev) => prev.filter((row) => row.symbol !== sym));
    setBatchMsg(`${sym} removed locally. Syncing DB...`);

    try {
      const res = await fetch(`${API_BASE}/api/universe?symbol=${sym}`, { method: "DELETE" });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? "unknown error");
      }
      setBatchMsg(`${sym} deleted from DB managed list.`);
      void Promise.all([loadUniverse(), loadWatchlist(), loadUniverseScan()]);
    } catch (e) {
      setScanRows(prevScanRows);
      setUniverseSymbols(prevUniverse);
      setWatchlistItems(prevWatchlist);
      setBatchMsg(`Failed to delete ${sym}: ${String(e)}`);
    } finally {
      setScanActionBusy(null);
    }
  };

  useEffect(() => {
    if (!prefsReady) {
      return;
    }
    loadData(symbol);
    touchViewHistory(symbol);
  }, [prefsReady, symbol]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!prefsReady || !autoRefreshEnabled) {
      return;
    }
    const t = setInterval(() => {
      loadData(symbol, { silent: true });
    }, autoRefreshSec * 1000);
    return () => clearInterval(t);
  }, [symbol, autoRefreshEnabled, autoRefreshSec, prefsReady]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!prefsReady) {
      return;
    }
    localStorage.setItem(
      DASHBOARD_PREFS_STORAGE_KEY,
      JSON.stringify({ symbol, autoRefreshEnabled, autoRefreshSec }),
    );
  }, [prefsReady, symbol, autoRefreshEnabled, autoRefreshSec]);

  useEffect(() => {
    if (!prefsReady || defaultSymbolSeeded) {
      return;
    }
    const rawDashboardPrefs = localStorage.getItem(DASHBOARD_PREFS_STORAGE_KEY);
    if (rawDashboardPrefs) {
      setDefaultSymbolSeeded(true);
      return;
    }
    if (watchlistItems.length > 0) {
      const first = String(watchlistItems[0].symbol ?? "").trim().toUpperCase();
      if (first) {
        setSymbol(first);
        setInputSymbol(first);
      }
      setDefaultSymbolSeeded(true);
    }
  }, [prefsReady, defaultSymbolSeeded, watchlistItems]);

  useEffect(() => {
    loadWatchlist();
    loadUniverse();
    loadViewHistory();

    const rawDashboardPrefs = localStorage.getItem(DASHBOARD_PREFS_STORAGE_KEY);
    try {
      if (rawDashboardPrefs) {
        const parsed = JSON.parse(rawDashboardPrefs) as {
          symbol?: string;
          autoRefreshEnabled?: boolean;
          autoRefreshSec?: number;
        };
        const savedSymbol = String(parsed.symbol ?? "").trim().toUpperCase();
        if (savedSymbol) {
          setSymbol(savedSymbol);
          setInputSymbol(savedSymbol);
        }
        if (typeof parsed.autoRefreshEnabled === "boolean") {
          setAutoRefreshEnabled(parsed.autoRefreshEnabled);
        }
        if ([60, 120, 300].includes(Number(parsed.autoRefreshSec))) {
          setAutoRefreshSec(Number(parsed.autoRefreshSec));
        }
      }
    } catch {
      // ignore invalid storage
    } finally {
      setPrefsReady(true);
    }
  }, [loadUniverse, loadViewHistory]);

  useEffect(() => {
    loadUniverseScan();
    const t = setInterval(loadUniverseScan, 180_000);
    return () => clearInterval(t);
  }, [universeSymbols]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const q = inputSymbol.trim();
    if (q.length < 2) {
      setListSearchResults([]);
      return;
    }
    const t = setTimeout(async () => {
      try {
        setListSearching(true);
        const rows = await fetchJSON<SymbolSearchResult[]>(`/api/symbols/search?q=${encodeURIComponent(q)}&limit=8`);
        setListSearchResults(rows ?? []);
      } catch {
        setListSearchResults([]);
      } finally {
        setListSearching(false);
      }
    }, 250);
    return () => clearTimeout(t);
  }, [inputSymbol]);

  const recentSymbols = viewHistory.length > 0 ? viewHistory : watchlistItems.map((x) => x.symbol).slice(0, 20);
  const currentPrice = hourlyCandles[hourlyCandles.length - 1]?.close ?? candles[candles.length - 1]?.close;
  const managedSymbols = buildManagedSymbols();
  const managedSet = new Set(managedSymbols);
  const alertsOnCount = watchlistItems.filter((x) => managedSet.has(String(x.symbol).toUpperCase())).length;
  const totalManagedCount = managedSymbols.length;
  const favoriteCount = watchlistItems.filter((x) => x.pinned).length;
  const executionPlan = signal ? buildExecutionPlan(signal, currentPrice) : null;

  const pinnedSet = new Set(
    watchlistItems.filter((x) => x.pinned).map((x) => String(x.symbol).toUpperCase()),
  );
  const watchlistBySymbol = new Map(
    watchlistItems.map((x) => [String(x.symbol).toUpperCase(), x]),
  );
  const notifyModeRank = (sym: string) => {
    const item = watchlistBySymbol.get(sym.toUpperCase());
    // Event-first policy: interval-configured symbols are intentionally shown later.
    return item?.notify_mode === "interval" ? 1 : 0;
  };
  const sortScanRows = (a: ScanRow, b: ScanRow) => {
    const aPinned = pinnedSet.has(a.symbol);
    const bPinned = pinnedSet.has(b.symbol);
    if (aPinned !== bPinned) {
      return aPinned ? -1 : 1;
    }

    const modeGap = notifyModeRank(a.symbol) - notifyModeRank(b.symbol);
    if (modeGap !== 0) {
      return modeGap;
    }

    const scoreGap = topListScore(b) - topListScore(a);
    if (scoreGap !== 0) return scoreGap;
    const buyGap = b.signal.buy_pct - a.signal.buy_pct;
    if (buyGap !== 0) return buyGap;
    return a.symbol.localeCompare(b.symbol);
  };
  const scanRowsSorted = [...scanRows].sort(sortScanRows);
  const topRow = scanRowsSorted.length === 0 ? null : scanRowsSorted[0];

  const togglePinnedFromScan = async (sym: string) => {
    const wl = watchlistItems.find((x) => x.symbol === sym);
    const nextPinned = !(wl?.pinned ?? false);

    // If the symbol is not yet an alert target, auto-register it first so
    // favorites can be managed directly from the list.
    if (!wl) {
      const optimistic: WatchlistItem = {
        symbol: sym,
        added_at: new Date().toISOString(),
        notify_mode: "event",
        notify_interval_minute: 5,
        notify_interval_hour: 1,
        pinned: nextPinned,
        sort_order: watchlistItems.length,
      };
      setWatchlistItems((prev) => [...prev, optimistic]);
      try {
        const addRes = await fetch(`${API_BASE}/api/watchlist?symbol=${sym}&notify_interval_minutes=5&notify_mode=event`, {
          method: "POST",
        });
        if (!addRes.ok) {
          const d = await addRes.json().catch(() => ({}));
          throw new Error(d.error ?? "failed to create watchlist target");
        }

        const pinRes = await fetch(`${API_BASE}/api/watchlist/pin?symbol=${sym}&pinned=${nextPinned}`, { method: "POST" });
        if (!pinRes.ok) {
          const d = await pinRes.json().catch(() => ({}));
          throw new Error(d.error ?? "failed to pin");
        }
        await loadWatchlist();
      } catch (e) {
        setWatchlistItems((prev) => prev.filter((x) => x.symbol !== sym));
        setBatchMsg(`Failed to set favorite: ${String(e)}`);
      }
      return;
    }

    setWatchlistItems((prev) => prev.map((x) => (x.symbol === sym ? { ...x, pinned: nextPinned } : x)));
    try {
      const res = await fetch(`${API_BASE}/api/watchlist/pin?symbol=${sym}&pinned=${nextPinned}`, { method: "POST" });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? "failed to pin");
      }
    } catch {
      setWatchlistItems((prev) => prev.map((x) => (x.symbol === sym ? { ...x, pinned: !nextPinned } : x)));
    }
  };

  return (
    <div className="min-h-screen bg-[#0f1117] text-slate-200 p-4 md:p-6">
      <section className="glass-panel scan-grid scan-ambient rounded-2xl p-4 md:p-6 mb-5 overflow-hidden">
        <div className="flex flex-col md:flex-row md:items-end md:justify-between gap-4 mb-4">
          <div>
            <p className="text-[11px] tracking-[0.2em] uppercase text-cyan-300/80">Midas Touch Control Room</p>
            <h2 className="text-xl md:text-2xl font-bold text-white mt-1">Market Cap Priority List</h2>
            <p className="text-xs md:text-sm text-slate-400 mt-1">Market-cap seeded list + user add/remove | Timing: {SCAN_TF}m</p>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[11px] text-slate-400">Updated</span>
            <span className="text-xs font-mono text-cyan-300">{scanUpdatedAt || "--:--:--"}</span>
            <button onClick={loadUniverseScan} disabled={scanLoading} className="text-xs px-3 py-1.5 rounded border border-cyan-400/40 bg-cyan-400/10 text-cyan-200 hover:bg-cyan-400/20 transition-colors disabled:opacity-60">{scanLoading ? "Scanning..." : "Rescan"}</button>
            <button onClick={analyzeAllFavorites} disabled={batchAnalyzing || favoriteCount === 0} className="text-xs px-3 py-1.5 rounded border border-indigo-400/40 bg-indigo-500/10 text-indigo-200 hover:bg-indigo-500/20 transition-colors disabled:opacity-60">{batchAnalyzing ? "Analyzing..." : "Analyze All Favorites"}</button>
          </div>
        </div>
        {batchMsg && <div className="mb-3 rounded border border-slate-700 bg-slate-900/50 px-3 py-2 text-xs text-slate-300">{batchMsg}</div>}

        <div className="grid grid-cols-1 xl:grid-cols-12 gap-4">
          <div className="xl:col-span-4 rounded-xl border border-slate-700/90 bg-[#0c1422]/80 p-4">
            <p className="text-[11px] text-slate-500 uppercase tracking-wide">Live Summary</p>
            <p className="text-3xl font-black text-white mt-2">{alertsOnCount} / {totalManagedCount}</p>
            <p className="text-xs text-slate-400">alerts on / total managed</p>
            <div className="mt-4 space-y-2 text-xs">
              <div className="flex items-center justify-between"><span className="text-slate-500">Alert Targets</span><span className="font-mono text-slate-300">{alertsOnCount}</span></div>
              <div className="flex items-center justify-between"><span className="text-slate-500">Managed Universe</span><span className="font-mono text-slate-300">{totalManagedCount}</span></div>
            </div>
            {topRow && (
              <div className="mt-4 rounded-lg border border-emerald-400/30 bg-emerald-400/10 p-3">
                <p className="text-[11px] text-emerald-300 uppercase tracking-wide">Top Of List</p>
                <button onClick={() => handleAnalyze(topRow.symbol)} className="mt-1 text-left w-full">
                  <p className="text-lg font-black text-white">{topRow.symbol}</p>
                  <p className="text-xs text-emerald-200">Buy {topRow.signal.buy_pct.toFixed(0)}%</p>
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
                  {scanRowsSorted.map((row) => {
                    const wl = watchlistItems.find((x) => x.symbol === row.symbol);
                    const isOn = Boolean(wl);
                    const isPinned = Boolean(wl?.pinned);
                    const busy = scanActionBusy?.startsWith(`${row.symbol}:`);
                    return (
                      <tr key={row.symbol} className="border-t border-slate-800 hover:bg-cyan-500/5 transition-colors cursor-pointer" onClick={() => handleAnalyze(row.symbol)}>
                        <td className="px-3 py-2">
                          <div className="font-mono text-white flex items-center gap-1.5">
                            <button
                              onClick={(e) => {
                                e.stopPropagation();
                                togglePinnedFromScan(row.symbol);
                              }}
                              className={`text-[11px] px-1.5 py-0.5 rounded border transition-colors ${isPinned ? "border-amber-500/50 text-amber-300 bg-amber-500/10" : "border-slate-600 text-slate-500 hover:text-amber-300 hover:border-amber-500/50"}`}
                              title={isPinned ? "Favorite ON" : "Favorite OFF"}
                            >
                              {isPinned ? "★" : "☆"}
                            </button>
                            <span>{actionEmoji(row.signal.action)} {row.symbol}</span>
                          </div>
                          <div className="mt-0.5 text-[10px] text-slate-400 truncate max-w-[220px]">{row.companyName || "No company name"}</div>
                        </td>
                        <td className={`px-3 py-2 text-right font-mono ${scanScoreClass(row.signal.buy_pct)}`}>{row.signal.buy_pct.toFixed(0)}%</td>
                        <td className="px-3 py-2 text-right font-mono text-slate-400">{row.signal.hold_pct.toFixed(0)}%</td>
                        <td className="px-3 py-2 text-right font-mono text-slate-400">{row.signal.sell_pct.toFixed(0)}%</td>
                        <td className={`px-3 py-2 ${actionColor(row.signal.trend_action ?? row.signal.action)}`}>{row.signal.trend_action ?? row.signal.action}</td>
                        <td className={`px-3 py-2 ${actionColor(row.signal.timing_action ?? row.signal.action)}`}>{row.signal.timing_action ?? row.signal.action}</td>
                        <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                          <div className="flex items-center gap-1.5">
                            <span className={`text-[10px] px-1.5 py-0.5 rounded border ${isOn ? "border-emerald-500/50 text-emerald-300" : "border-slate-600 text-slate-400"}`}>
                              {isOn
                                ? (wl?.notify_mode === "interval"
                                  ? `ON ${notifyModeLabel(wl?.notify_mode)} ${intervalShortLabel(wl?.notify_interval_minute, wl?.notify_interval_hour)}`
                                  : `ON ${notifyModeLabel(wl?.notify_mode)}`)
                                : "OFF"}
                            </span>
                            {!isOn && (
                              <>
                                <button onClick={() => addAlertTarget(row.symbol, "event")} disabled={busy} className="text-[10px] px-1.5 py-0.5 rounded border border-cyan-500/40 text-cyan-200 hover:bg-cyan-500/20 disabled:opacity-50">E+</button>
                                <select
                                  value={pendingIntervalBySymbol[row.symbol] ?? DEFAULT_INTERVAL_MINUTE}
                                  onChange={(e) => setPendingIntervalBySymbol((prev) => ({ ...prev, [row.symbol]: Number(e.target.value) }))}
                                  className="text-[10px] px-1.5 py-0.5 rounded bg-slate-700 border border-slate-600 text-slate-200"
                                  title="Interval"
                                >
                                  {INTERVAL_OPTIONS_MINUTES.map((m) => (
                                    <option key={m} value={m}>
                                      {m % 60 === 0 ? `${m / 60}h` : `${m}m`}
                                    </option>
                                  ))}
                                </select>
                                <button onClick={() => addAlertTarget(row.symbol, "interval", pendingIntervalBySymbol[row.symbol] ?? DEFAULT_INTERVAL_MINUTE)} disabled={busy} className="text-[10px] px-1.5 py-0.5 rounded border border-amber-500/40 text-amber-200 hover:bg-amber-500/20 disabled:opacity-50">I+</button>
                              </>
                            )}
                            {isOn && <button onClick={() => removeAlertTarget(row.symbol)} disabled={busy} className="text-[10px] px-1.5 py-0.5 rounded border border-red-500/40 text-red-200 hover:bg-red-500/20 disabled:opacity-50">Remove</button>}
                          </div>
                        </td>
                        <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                          <button
                            onClick={() => removeUniverseSymbol(row.symbol)}
                            title="Delete from DB watchlist"
                            className="text-[10px] px-1.5 py-0.5 rounded border border-slate-600 text-slate-300 hover:border-red-400 hover:text-red-300"
                          >
                            Delete
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              {!scanLoading && scanRows.length === 0 && <div className="p-6 text-center text-slate-500 text-xs">No symbols available in this list.</div>}
              {scanLoading && <div className="p-6 text-center text-cyan-300 text-xs">Scanning...</div>}
              {scanError && <div className="p-4 text-center text-red-300 text-xs">{scanError}</div>}
            </div>
          </div>
        </div>
      </section>

      <div className="sticky top-2 z-30 mb-3">
        <div className="inline-flex items-center gap-2 rounded-full border border-indigo-400/40 bg-slate-900/90 backdrop-blur px-3 py-1.5 shadow-lg">
          <span className="text-[11px] text-slate-400">Price</span>
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
          <button onClick={addUniverseSymbol} disabled={listSearching} className="px-4 py-2 bg-emerald-700 hover:bg-emerald-600 text-white text-sm rounded-lg transition-colors disabled:opacity-50">{listSearching ? "Validating..." : "Add to List"}</button>
          <div className="inline-flex items-center gap-1 rounded-lg border border-slate-600 bg-slate-800 px-2 py-1.5">
            <button
              onClick={() => setAutoRefreshEnabled((v) => !v)}
              className={`text-[11px] px-2 py-0.5 rounded border transition-colors ${autoRefreshEnabled ? "border-emerald-500/50 bg-emerald-500/20 text-emerald-200" : "border-slate-600 bg-slate-700 text-slate-400"}`}
            >
              Auto-refresh {autoRefreshEnabled ? "ON" : "OFF"}
            </button>
            <select
              value={autoRefreshSec}
              onChange={(e) => setAutoRefreshSec(Number(e.target.value))}
              disabled={!autoRefreshEnabled}
              className="text-[11px] px-1.5 py-0.5 rounded bg-slate-700 border border-slate-600 text-slate-200 disabled:opacity-50"
            >
              <option value={60}>1m</option>
              <option value={120}>2m</option>
              <option value={300}>5m</option>
            </select>
          </div>
          <button onClick={handleNotify} disabled={notifying || !signal} className="px-4 py-2 bg-emerald-700 hover:bg-emerald-600 text-white text-sm rounded-lg transition-colors disabled:opacity-50">{notifying ? "Sending..." : "Send to Telegram"}</button>
          {lastUpdate && <span className="text-xs text-slate-500">Last refresh: {lastUpdate}</span>}
        </div>
      </div>

      {(listSearching || listSearchResults.length > 0) && (
        <div className="mb-4 rounded-lg border border-slate-700 bg-slate-900/70 p-2">
          {listSearching && <p className="text-[11px] text-slate-500 px-1 py-1">Searching symbols...</p>}
          {listSearchResults.length > 0 && (
            <div className="max-h-36 overflow-auto">
              {listSearchResults.map((row) => (
                <button
                  key={`${row.symbol}-${row.exchange}`}
                  onClick={() => {
                    setInputSymbol(row.symbol.toUpperCase());
                    setListSearchResults([]);
                  }}
                  className="w-full text-left px-2 py-1.5 rounded hover:bg-slate-800 transition-colors"
                >
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-mono text-cyan-200">{row.symbol}</span>
                    <span className="text-[10px] text-slate-500">{row.exchange}</span>
                  </div>
                  <p className="text-[11px] text-slate-400 truncate">{row.name || "(no name)"}</p>
                  <div className="mt-1 flex items-center gap-1.5">
                    <span className="text-[10px] px-1.5 py-0.5 rounded border border-slate-600 text-slate-300 bg-slate-800/70">
                      Country {inferCountryFromExchange(row.exchange)}
                    </span>
                    <span className={`text-[10px] px-1.5 py-0.5 rounded border ${typeBadgeClass(normalizeTypeLabel(row.type_display))}`}>
                      {normalizeTypeLabel(row.type_display)}
                    </span>
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {recentSymbols.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-2">
          {recentSymbols.map((sym) => (
            <div
              key={sym}
              className={`inline-flex items-center gap-1 text-xs px-2 py-1 rounded border transition-colors ${sym === symbol ? "border-indigo-400 bg-indigo-500/20 text-indigo-200" : "border-slate-600 bg-slate-800 text-slate-300"}`}
            >
              <button
                onClick={() => handleAnalyze(sym)}
                className="hover:text-white"
              >
                {sym}
              </button>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  removeViewHistorySymbol(sym);
                }}
                disabled={historyBusy === sym}
                className="text-[10px] leading-none px-1 rounded border border-slate-500/40 hover:border-red-400 hover:text-red-300 disabled:opacity-50"
                title="Remove from history"
              >
                x
              </button>
            </div>
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
                {signal.data_quality_note && <span className="px-2 py-0.5 rounded bg-orange-500/20 border border-orange-500/40 text-orange-200">Data Warning</span>}
              </div>
              {signal.data_quality_note && (
                <p className="mb-2 text-[11px] text-orange-200/90">{signal.data_quality_note}</p>
              )}
              <div className="mb-2">
                <p className="text-[11px] text-slate-400 mb-1">Multi-timeframe view</p>
                <div className="flex flex-wrap gap-1.5 text-[10px]">
                  <span className="px-2 py-0.5 rounded border border-slate-600 bg-slate-800/70">30m: {signal.timeframe_bias?.["30m"] ?? signal.timing_action ?? signal.action}</span>
                  <span className="px-2 py-0.5 rounded border border-slate-600 bg-slate-800/70">1h: {signal.timeframe_bias?.["1h"] ?? signal.timing_action ?? signal.action}</span>
                  <span className="px-2 py-0.5 rounded border border-slate-600 bg-slate-800/70">4h: {signal.timeframe_bias?.["4h"] ?? signal.timing_action ?? signal.action}</span>
                  <span className="px-2 py-0.5 rounded border border-slate-600 bg-slate-800/70">1d: {signal.timeframe_bias?.["1d"] ?? signal.trend_action ?? signal.action}</span>
                  <span className="px-2 py-0.5 rounded border border-slate-600 bg-slate-800/70">1w: {signal.weekly_action ?? signal.timeframe_bias?.["1mo"] ?? signal.trend_action ?? signal.action}</span>
                  <span className="px-2 py-0.5 rounded border border-slate-600 bg-slate-800/70">1mo: {signal.timeframe_bias?.["1mo"] ?? signal.weekly_action ?? signal.trend_action ?? signal.action}</span>
                </div>
              </div>
              <ProbBar buy={signal.buy_pct} sell={signal.sell_pct} hold={signal.hold_pct} />
              {executionPlan && (
                <div className="mt-3 rounded-lg border border-slate-700 bg-slate-900/40 p-3">
                  <p className="text-[11px] text-slate-400 mb-2">Execution Guide</p>
                  <div className="space-y-1.5 text-[11px] text-slate-200">
                    <p className="font-mono">- Entry: {formatPrice(executionPlan.entry)} ({formatPct(executionPlan.entryPct)})</p>
                    <p className="font-mono">- Stop Loss: {formatPrice(executionPlan.stop)} ({formatPct(executionPlan.stopPct)})</p>
                    <p className="font-mono">- Target 1: {formatPrice(executionPlan.target1)} ({formatPct(executionPlan.target1Pct)})</p>
                    <p className="font-mono">- Target 2: {formatPrice(executionPlan.target2)} ({formatPct(executionPlan.target2Pct)})</p>
                  </div>
                </div>
              )}
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

        </div>
      </div>

      <section className="mt-6 rounded-2xl border border-slate-700/80 bg-slate-900/40 p-4 md:p-5">
        <div className="flex items-center justify-between mb-3">
          <div>
            <p className="text-[11px] uppercase tracking-[0.16em] text-slate-500">System</p>
            <h2 className="text-base md:text-lg font-semibold text-slate-200">System Monitor</h2>
          </div>
          <p className="text-[11px] text-slate-500">Infrastructure and data-source health</p>
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <SourceStatusPanel />
          <DBStatsPanel />
        </div>
      </section>
    </div>
  );
}
