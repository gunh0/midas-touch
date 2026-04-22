package mongodb

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	dbName       = "midas_touch"
	candlesCol   = "candles"
	signalsCol   = "signals"
	watchlistCol = "watchlist"
	universeCol  = "universe_symbols"
	historyCol   = "view_history"
	maxStorageMB = 450 // warn threshold (out of 500MB free tier)
)

type Client struct {
	db *mongo.Database
}

// CandleDoc represents a single OHLCV candle stored in MongoDB.
type CandleDoc struct {
	Symbol    string    `bson:"symbol"    json:"symbol"`
	Timeframe string    `bson:"timeframe" json:"timeframe"`
	Source    string    `bson:"source"    json:"source"`
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Open      float64   `bson:"open"      json:"open"`
	High      float64   `bson:"high"      json:"high"`
	Low       float64   `bson:"low"       json:"low"`
	Close     float64   `bson:"close"     json:"close"`
	Volume    float64   `bson:"volume"    json:"volume"`
}

// SignalDoc represents a trading signal event.
type SignalDoc struct {
	Symbol    string    `bson:"symbol"    json:"symbol"`
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Action    string    `bson:"action"    json:"action"`
	BuyPct    float64   `bson:"buy_pct"   json:"buy_pct"`
	SellPct   float64   `bson:"sell_pct"  json:"sell_pct"`
	HoldPct   float64   `bson:"hold_pct"  json:"hold_pct"`
	Reason    string    `bson:"reason"    json:"reason"`
	Notified  bool      `bson:"notified"  json:"notified"`
	IsSpecial bool      `bson:"is_special,omitempty" json:"is_special,omitempty"`
}

// WatchlistItem is a user-registered symbol.
type WatchlistItem struct {
	Symbol               string     `bson:"symbol" json:"symbol"`
	AddedAt              time.Time  `bson:"added_at" json:"added_at"`
	NotifyIntervalMinute int        `bson:"notify_interval_minute,omitempty" json:"notify_interval_minute,omitempty"`
	NotifyIntervalHour   int        `bson:"notify_interval_hour" json:"notify_interval_hour"`
	NotifyMode           string     `bson:"notify_mode" json:"notify_mode"`
	Pinned               bool       `bson:"pinned" json:"pinned"`
	SortOrder            int        `bson:"sort_order" json:"sort_order"`
	LastNotifiedAt       *time.Time `bson:"last_notified_at,omitempty" json:"last_notified_at,omitempty"`
	LastSpecialAt        *time.Time `bson:"last_special_at,omitempty" json:"last_special_at,omitempty"`
}

type UniverseSymbolDoc struct {
	Symbol    string    `bson:"symbol" json:"symbol"`
	Kind      string    `bson:"kind" json:"kind"` // base | custom
	AddedAt   time.Time `bson:"added_at" json:"added_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

type ViewHistoryDoc struct {
	Symbol     string    `bson:"symbol" json:"symbol"`
	LastViewed time.Time `bson:"last_viewed" json:"last_viewed"`
}

func normalizeNotifyIntervalMinute(minute, hour int) int {
	if minute <= 0 && hour > 0 {
		minute = hour * 60
	}
	if minute <= 0 {
		minute = 3
	}
	if minute < 1 {
		minute = 1
	}
	if minute > 24*60 {
		minute = 24 * 60
	}
	return minute
}

func normalizeNotifyMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "interval" {
		return "interval"
	}
	return "event"
}

// DBStats holds storage usage info.
type DBStats struct {
	DataSizeMB    float64 `json:"data_size_mb"`
	StorageSizeMB float64 `json:"storage_size_mb"`
	Collections   int     `json:"collections"`
	Objects       int64   `json:"objects"`
	OverLimit     bool    `json:"over_limit"`
}

func NewClient(ctx context.Context) (*Client, error) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		return nil, fmt.Errorf("MONGODB_URI environment variable is required")
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect to mongodb: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}

	db := client.Database(dbName)
	c := &Client{db: db}
	if err := c.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}

	return c, nil
}

func (c *Client) ensureIndexes(ctx context.Context) error {
	// candles: fast latest retrieval and conflict-free upsert on OHLC key.
	candleIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "timeframe", Value: 1}, {Key: "timestamp", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uq_candle_symbol_tf_ts"),
		},
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "timeframe", Value: 1}, {Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("idx_candle_symbol_tf_ts_desc"),
		},
	}
	if _, err := c.db.Collection(candlesCol).Indexes().CreateMany(ctx, candleIndexes); err != nil {
		if strings.Contains(err.Error(), "E11000 duplicate key error") {
			if dedupeErr := c.deduplicateCandles(ctx); dedupeErr != nil {
				return dedupeErr
			}
			if _, retryErr := c.db.Collection(candlesCol).Indexes().CreateMany(ctx, candleIndexes); retryErr != nil {
				return retryErr
			}
		} else {
			return err
		}
	}

	// signals: latest notified/recent signal lookups.
	if _, err := c.db.Collection(signalsCol).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("idx_signal_symbol_ts_desc"),
		},
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "notified", Value: 1}, {Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("idx_signal_symbol_notified_ts_desc"),
		},
	}); err != nil {
		return err
	}

	// watchlist: unique symbol and sort path used by GET /watchlist.
	if _, err := c.db.Collection(watchlistCol).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uq_watchlist_symbol"),
		},
		{
			Keys:    bson.D{{Key: "pinned", Value: -1}, {Key: "sort_order", Value: 1}, {Key: "symbol", Value: 1}},
			Options: options.Index().SetName("idx_watchlist_pinned_sort_symbol"),
		},
	}); err != nil {
		return err
	}

	// universe/history: unique symbol and recency sort.
	if _, err := c.db.Collection(universeCol).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uq_universe_symbol"),
		},
		{
			Keys:    bson.D{{Key: "kind", Value: 1}, {Key: "updated_at", Value: -1}},
			Options: options.Index().SetName("idx_universe_kind_updated_desc"),
		},
	}); err != nil {
		return err
	}

	if _, err := c.db.Collection(historyCol).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uq_history_symbol"),
		},
		{
			Keys:    bson.D{{Key: "last_viewed", Value: -1}},
			Options: options.Index().SetName("idx_history_last_viewed_desc"),
		},
	}); err != nil {
		return err
	}

	return nil
}

type duplicateCandleGroup struct {
	IDs []bson.ObjectID `bson:"ids"`
}

func (c *Client) deduplicateCandles(ctx context.Context) error {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "symbol", Value: "$symbol"}, {Key: "timeframe", Value: "$timeframe"}, {Key: "timestamp", Value: "$timestamp"}}},
			{Key: "ids", Value: bson.D{{Key: "$push", Value: "$_id"}}},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
		bson.D{{Key: "$match", Value: bson.D{{Key: "count", Value: bson.D{{Key: "$gt", Value: 1}}}}}},
	}

	cursor, err := c.db.Collection(candlesCol).Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var grp duplicateCandleGroup
		if err := cursor.Decode(&grp); err != nil {
			return err
		}
		if len(grp.IDs) <= 1 {
			continue
		}
		obsoleteIDs := grp.IDs[1:]
		_, err := c.db.Collection(candlesCol).DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: obsoleteIDs}}}})
		if err != nil {
			return err
		}
	}

	return cursor.Err()
}

// ── Candles ────────────────────────────────────────────────────────────────

func (c *Client) UpsertCandles(ctx context.Context, candles []CandleDoc) error {
	if len(candles) == 0 {
		return nil
	}
	col := c.db.Collection(candlesCol)
	models := make([]mongo.WriteModel, 0, len(candles))
	for _, cd := range candles {
		if cd.Timeframe == "" {
			cd.Timeframe = "1d"
		}
		if cd.Source == "" {
			cd.Source = "unknown"
		}
		filter := bson.D{{Key: "symbol", Value: cd.Symbol}, {Key: "timeframe", Value: cd.Timeframe}, {Key: "timestamp", Value: cd.Timestamp}}
		update := bson.D{{Key: "$set", Value: cd}}
		models = append(models, mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(update).SetUpsert(true))
	}
	_, err := col.BulkWrite(ctx, models)
	return err
}

func (c *Client) GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]CandleDoc, error) {
	col := c.db.Collection(candlesCol)
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(int64(limit))
	if timeframe == "" {
		timeframe = "1d"
	}
	cursor, err := col.Find(ctx, bson.D{{Key: "symbol", Value: symbol}, {Key: "timeframe", Value: timeframe}}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []CandleDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	// reverse to chronological order
	for i, j := 0, len(docs)-1; i < j; i, j = i+1, j-1 {
		docs[i], docs[j] = docs[j], docs[i]
	}
	return docs, nil
}

func (c *Client) PruneOldCandles(ctx context.Context, symbol, timeframe string, keepDays int) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	if timeframe == "" {
		timeframe = "1d"
	}
	filter := bson.D{
		{Key: "symbol", Value: symbol},
		{Key: "timeframe", Value: timeframe},
		{Key: "timestamp", Value: bson.D{{Key: "$lt", Value: cutoff}}},
	}
	res, err := c.db.Collection(candlesCol).DeleteMany(ctx, filter)
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

// ── Signals ────────────────────────────────────────────────────────────────

func (c *Client) SaveSignal(ctx context.Context, sig SignalDoc) error {
	_, err := c.db.Collection(signalsCol).InsertOne(ctx, sig)
	return err
}

func (c *Client) GetRecentSignals(ctx context.Context, symbol string, limit int) ([]SignalDoc, error) {
	col := c.db.Collection(signalsCol)
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(int64(limit))
	cursor, err := col.Find(ctx, bson.D{{Key: "symbol", Value: symbol}}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []SignalDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (c *Client) GetLatestNotifiedSignal(ctx context.Context, symbol string) (*SignalDoc, error) {
	col := c.db.Collection(signalsCol)
	opts := options.FindOne().SetSort(bson.D{{Key: "timestamp", Value: -1}})

	var doc SignalDoc
	err := col.FindOne(ctx, bson.D{{Key: "symbol", Value: symbol}, {Key: "notified", Value: true}}, opts).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// ── Watchlist ──────────────────────────────────────────────────────────────

// AddToWatchlist adds a symbol (upsert - no duplicates) and updates notify settings.
func (c *Client) AddToWatchlist(ctx context.Context, symbol string, notifyIntervalMinute int, notifyMode string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	notifyIntervalMinute = normalizeNotifyIntervalMinute(notifyIntervalMinute, 0)
	notifyIntervalHour := int(math.Ceil(float64(notifyIntervalMinute) / 60.0))
	if notifyIntervalHour < 1 {
		notifyIntervalHour = 1
	}
	notifyMode = normalizeNotifyMode(notifyMode)
	order, err := c.nextWatchlistOrder(ctx)
	if err != nil {
		return err
	}
	filter := bson.D{{Key: "symbol", Value: symbol}}
	update := bson.D{
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "symbol", Value: symbol},
			{Key: "added_at", Value: time.Now()},
			{Key: "pinned", Value: false},
			{Key: "sort_order", Value: order},
		}},
		{Key: "$set", Value: bson.D{
			{Key: "notify_interval_minute", Value: notifyIntervalMinute},
			{Key: "notify_interval_hour", Value: notifyIntervalHour},
			{Key: "notify_mode", Value: notifyMode},
		}},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err = c.db.Collection(watchlistCol).UpdateOne(ctx, filter, update, opts)
	return err
}

func (c *Client) SetWatchlistPinned(ctx context.Context, symbol string, pinned bool) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	_, err := c.db.Collection(watchlistCol).UpdateOne(
		ctx,
		bson.D{{Key: "symbol", Value: symbol}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "pinned", Value: pinned}}}},
	)
	return err
}

func (c *Client) ReorderWatchlist(ctx context.Context, symbols []string) error {
	if len(symbols) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(symbols))
	for i, sym := range symbols {
		norm := strings.ToUpper(strings.TrimSpace(sym))
		if norm == "" {
			continue
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "symbol", Value: norm}}).
			SetUpdate(bson.D{{Key: "$set", Value: bson.D{{Key: "sort_order", Value: i}}}}))
	}
	if len(models) == 0 {
		return nil
	}
	_, err := c.db.Collection(watchlistCol).BulkWrite(ctx, models)
	return err
}

func (c *Client) MarkWatchlistNotified(ctx context.Context, symbol string, special bool, at time.Time) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	set := bson.D{{Key: "last_notified_at", Value: at}}
	if special {
		set = append(set, bson.E{Key: "last_special_at", Value: at})
	}
	_, err := c.db.Collection(watchlistCol).UpdateOne(ctx, bson.D{{Key: "symbol", Value: symbol}}, bson.D{{Key: "$set", Value: set}})
	return err
}

// RemoveFromWatchlist removes a symbol.
func (c *Client) RemoveFromWatchlist(ctx context.Context, symbol string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	_, err := c.db.Collection(watchlistCol).DeleteOne(ctx, bson.D{{Key: "symbol", Value: symbol}})
	return err
}

// GetWatchlist returns all saved symbols sorted alphabetically.
func (c *Client) GetWatchlist(ctx context.Context) ([]WatchlistItem, error) {
	opts := options.Find().SetSort(bson.D{{Key: "pinned", Value: -1}, {Key: "sort_order", Value: 1}, {Key: "symbol", Value: 1}})
	cursor, err := c.db.Collection(watchlistCol).Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var items []WatchlistItem
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].NotifyIntervalMinute = normalizeNotifyIntervalMinute(items[i].NotifyIntervalMinute, items[i].NotifyIntervalHour)
		items[i].NotifyIntervalHour = int(math.Ceil(float64(items[i].NotifyIntervalMinute) / 60.0))
		if items[i].NotifyMode == "" {
			items[i].NotifyMode = "event"
		}
		if items[i].SortOrder <= 0 {
			items[i].SortOrder = i
		}
	}
	return items, nil
}

func (c *Client) nextWatchlistOrder(ctx context.Context) (int, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "sort_order", Value: -1}})
	var last WatchlistItem
	err := c.db.Collection(watchlistCol).FindOne(ctx, bson.D{}, opts).Decode(&last)
	if err == mongo.ErrNoDocuments {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return last.SortOrder + 1, nil
}

// ── Managed Universe ───────────────────────────────────────────────────────

func normalizeUniverseKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "custom" {
		return "custom"
	}
	return "base"
}

func (c *Client) EnsureBaseUniverse(ctx context.Context, symbols []string) error {
	if len(symbols) == 0 {
		return nil
	}
	col := c.db.Collection(universeCol)
	now := time.Now()
	models := make([]mongo.WriteModel, 0, len(symbols))
	for _, sym := range symbols {
		norm := strings.ToUpper(strings.TrimSpace(sym))
		if norm == "" {
			continue
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "symbol", Value: norm}}).
			SetUpdate(bson.D{
				{Key: "$setOnInsert", Value: bson.D{
					{Key: "symbol", Value: norm},
					{Key: "kind", Value: "base"},
					{Key: "added_at", Value: now},
					{Key: "updated_at", Value: now},
				}},
			}).
			SetUpsert(true))
	}
	if len(models) == 0 {
		return nil
	}
	_, err := col.BulkWrite(ctx, models)
	return err
}

func (c *Client) GetUniverseSymbols(ctx context.Context) ([]UniverseSymbolDoc, error) {
	opts := options.Find().SetSort(bson.D{{Key: "kind", Value: 1}, {Key: "symbol", Value: 1}})
	cursor, err := c.db.Collection(universeCol).Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []UniverseSymbolDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (c *Client) AddUniverseSymbol(ctx context.Context, symbol, kind string) error {
	norm := strings.ToUpper(strings.TrimSpace(symbol))
	if norm == "" {
		return nil
	}
	kind = normalizeUniverseKind(kind)
	now := time.Now()
	_, err := c.db.Collection(universeCol).UpdateOne(
		ctx,
		bson.D{{Key: "symbol", Value: norm}},
		bson.D{
			{Key: "$setOnInsert", Value: bson.D{{Key: "added_at", Value: now}}},
			{Key: "$set", Value: bson.D{{Key: "symbol", Value: norm}, {Key: "kind", Value: kind}, {Key: "updated_at", Value: now}}},
		},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (c *Client) RemoveUniverseSymbol(ctx context.Context, symbol string) error {
	norm := strings.ToUpper(strings.TrimSpace(symbol))
	if norm == "" {
		return nil
	}
	_, err := c.db.Collection(universeCol).DeleteOne(ctx, bson.D{{Key: "symbol", Value: norm}})
	return err
}

// ── View History ───────────────────────────────────────────────────────────

func (c *Client) TouchViewHistory(ctx context.Context, symbol string, limit int) error {
	norm := strings.ToUpper(strings.TrimSpace(symbol))
	if norm == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	now := time.Now()
	_, err := c.db.Collection(historyCol).UpdateOne(
		ctx,
		bson.D{{Key: "symbol", Value: norm}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "symbol", Value: norm}, {Key: "last_viewed", Value: now}}}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return err
	}

	// Keep only most recent `limit` symbols.
	opts := options.Find().SetSort(bson.D{{Key: "last_viewed", Value: -1}})
	cursor, err := c.db.Collection(historyCol).Find(ctx, bson.D{}, opts)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	var docs []ViewHistoryDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return err
	}
	if len(docs) <= limit {
		return nil
	}

	obsolete := docs[limit:]
	symbols := make([]string, 0, len(obsolete))
	for _, d := range obsolete {
		symbols = append(symbols, d.Symbol)
	}
	_, err = c.db.Collection(historyCol).DeleteMany(ctx, bson.D{{Key: "symbol", Value: bson.D{{Key: "$in", Value: symbols}}}})
	return err
}

func (c *Client) GetViewHistory(ctx context.Context, limit int) ([]ViewHistoryDoc, error) {
	if limit <= 0 {
		limit = 20
	}
	opts := options.Find().SetSort(bson.D{{Key: "last_viewed", Value: -1}}).SetLimit(int64(limit))
	cursor, err := c.db.Collection(historyCol).Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []ViewHistoryDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (c *Client) RemoveViewHistorySymbol(ctx context.Context, symbol string) error {
	norm := strings.ToUpper(strings.TrimSpace(symbol))
	if norm == "" {
		return nil
	}
	_, err := c.db.Collection(historyCol).DeleteOne(ctx, bson.D{{Key: "symbol", Value: norm}})
	return err
}

// ── DB Stats ───────────────────────────────────────────────────────────────

func (c *Client) GetDBStats(ctx context.Context) (*DBStats, error) {
	result := c.db.RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}, {Key: "scale", Value: 1024 * 1024}})
	if result.Err() != nil {
		return nil, result.Err()
	}

	var raw bson.M
	if err := result.Decode(&raw); err != nil {
		return nil, err
	}

	toFloat := func(v interface{}) float64 {
		switch n := v.(type) {
		case float64:
			return n
		case int32:
			return float64(n)
		case int64:
			return float64(n)
		}
		return 0
	}
	toInt64 := func(v interface{}) int64 {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int32:
			return int64(n)
		case int64:
			return n
		}
		return 0
	}

	storageMB := toFloat(raw["storageSizeMB"])
	if storageMB == 0 {
		storageMB = toFloat(raw["storageSize"])
	}
	dataMB := toFloat(raw["dataSizeMB"])
	if dataMB == 0 {
		dataMB = toFloat(raw["dataSize"])
	}

	return &DBStats{
		DataSizeMB:    dataMB,
		StorageSizeMB: storageMB,
		Collections:   int(toInt64(raw["collections"])),
		Objects:       toInt64(raw["objects"]),
		OverLimit:     storageMB >= maxStorageMB,
	}, nil
}
