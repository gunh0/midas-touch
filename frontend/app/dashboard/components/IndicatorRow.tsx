export function IndicatorRow({ label, value, badge }: { label: string; value: string; badge?: { text: string; cls: string } }) {
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
