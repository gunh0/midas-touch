import type { CandleDoc } from "../types";
import { formatPct, formatPrice } from "../utils";

export function DailyClose30Panel({ symbol, candles }: { symbol: string; candles: CandleDoc[] }) {
  const last30 = candles.slice(-30).reverse();

  return (
    <div className="bg-slate-800/50 border border-slate-700 rounded-xl p-4">
      <div className="flex items-center justify-between mb-3">
        <h2 className="font-semibold text-slate-200">{symbol} Daily Close (30D)</h2>
        <span className="text-[11px] text-slate-500">Last 30 daily closes</span>
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
