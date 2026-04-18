export function ProbBar({ buy, sell, hold }: { buy: number; sell: number; hold: number }) {
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
