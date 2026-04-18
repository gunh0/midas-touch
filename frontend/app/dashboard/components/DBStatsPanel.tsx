"use client";

import { useEffect, useState } from "react";
import { API_BASE, fetchJSON } from "../api";
import type { DBStats } from "../types";

export function DBStatsPanel() {
  const [stats, setStats] = useState<DBStats | null>(null);
  const [pruning, setPruning] = useState(false);
  const [msg, setMsg] = useState("");

  const load = async () => {
    try {
      const s = await fetchJSON<DBStats>("/api/db/stats");
      setStats(s);
    } catch {
      // ignore
    }
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
