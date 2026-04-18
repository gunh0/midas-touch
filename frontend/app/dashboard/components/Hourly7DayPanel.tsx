import type { CandleDoc } from "../types";
import { formatPct, formatPrice } from "../utils";

export function Hourly7DayPanel({ symbol, candles }: { symbol: string; candles: CandleDoc[] }) {
  const latest = candles[candles.length - 1];
  const base24h = candles.length > 24 ? candles[candles.length - 25] : undefined;
  const currentPrice = latest?.close ?? 0;
  const change24hPct = base24h && base24h.close > 0 ? ((currentPrice - base24h.close) / base24h.close) * 100 : 0;

  const last24 = candles.slice(-24).reverse();

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="font-semibold text-slate-200">{symbol} 7D Hourly Movement</h2>
        <span className="text-[11px] text-slate-500">Last 7 days · 1H candles</span>
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
