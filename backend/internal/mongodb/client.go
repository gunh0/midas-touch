package mongodb

import (
	"context"
	"fmt"
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
}

// WatchlistItem is a user-registered symbol.
type WatchlistItem struct {
	Symbol             string     `bson:"symbol" json:"symbol"`
	AddedAt            time.Time  `bson:"added_at" json:"added_at"`
	NotifyIntervalHour int        `bson:"notify_interval_hour" json:"notify_interval_hour"`
	NotifyMode         string     `bson:"notify_mode" json:"notify_mode"`
	Pinned             bool       `bson:"pinned" json:"pinned"`
	SortOrder          int        `bson:"sort_order" json:"sort_order"`
	LastNotifiedAt     *time.Time `bson:"last_notified_at,omitempty" json:"last_notified_at,omitempty"`
	LastSpecialAt      *time.Time `bson:"last_special_at,omitempty" json:"last_special_at,omitempty"`
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

	return &Client{db: client.Database(dbName)}, nil
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

// AddToWatchlist adds a symbol (upsert — no duplicates) and updates notify settings.
func (c *Client) AddToWatchlist(ctx context.Context, symbol string, notifyIntervalHour int, notifyMode string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if notifyIntervalHour <= 0 {
		notifyIntervalHour = 4
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
		{Key: "$set", Value: bson.D{{Key: "notify_interval_hour", Value: notifyIntervalHour}, {Key: "notify_mode", Value: notifyMode}}},
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
		if items[i].NotifyIntervalHour <= 0 {
			items[i].NotifyIntervalHour = 4
		}
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
