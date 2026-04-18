export interface CandleDoc {
  symbol: string;
  timestamp: string;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

export interface Indicators {
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

export interface Score {
  RSIScore: number;
  MAScore: number;
  BBScore: number;
  SupertrendScore: number;
  Total: number;
}

export interface Signal {
  symbol: string;
  action: string;
  trend_action?: string;
  timing_action?: string;
  weekly_action?: string;
  timeframe_bias?: Record<string, string>;
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

export interface DBStats {
  data_size_mb: number;
  storage_size_mb: number;
  collections: number;
  objects: number;
  over_limit: boolean;
}

export interface WatchlistItem {
  symbol: string;
  added_at: string;
  notify_interval_minute?: number;
  notify_interval_hour?: number;
  notify_mode?: "event" | "interval" | string;
  pinned?: boolean;
  sort_order?: number;
}

export interface SymbolSearchResult {
  symbol: string;
  name: string;
  exchange: string;
  type_display: string;
}

export interface SourceStatus {
  source: string;
  ok: boolean;
  latency_ms: number;
  error?: string;
  detail?: string;
  checked_at: string;
  score: number;
}

export interface ScanRow {
  symbol: string;
  signal: Signal;
  companyName: string;
  exchange: string;
  typeLabel: string;
}

export interface UniverseSymbol {
  symbol: string;
  kind: "base" | "custom" | string;
  added_at?: string;
  updated_at?: string;
}

export interface ViewHistoryRow {
  symbol: string;
  last_viewed: string;
}
