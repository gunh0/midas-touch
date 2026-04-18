"use client";

import { useEffect, useState } from "react";
import { fetchJSON } from "../api";
import type { SourceStatus } from "../types";

export function SourceStatusPanel() {
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
