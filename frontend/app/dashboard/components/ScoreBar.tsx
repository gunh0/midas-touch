export function ScoreBar({ label, value }: { label: string; value: number }) {
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
