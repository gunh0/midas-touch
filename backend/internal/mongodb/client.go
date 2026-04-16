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
	Symbol    string    `bson:"symbol"     json:"symbol"`
	AddedAt   time.Time `bson:"added_at"   json:"added_at"`
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
		filter := bson.D{{Key: "symbol", Value: cd.Symbol}, {Key: "timestamp", Value: cd.Timestamp}}
		update := bson.D{{Key: "$set", Value: cd}}
		models = append(models, mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(update).SetUpsert(true))
	}
	_, err := col.BulkWrite(ctx, models)
	return err
}

func (c *Client) GetCandles(ctx context.Context, symbol string, limit int) ([]CandleDoc, error) {
	col := c.db.Collection(candlesCol)
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(int64(limit))
	cursor, err := col.Find(ctx, bson.D{{Key: "symbol", Value: symbol}}, opts)
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

func (c *Client) PruneOldCandles(ctx context.Context, symbol string, keepDays int) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	filter := bson.D{
		{Key: "symbol", Value: symbol},
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

// ── Watchlist ──────────────────────────────────────────────────────────────

// AddToWatchlist adds a symbol (upsert — no duplicates).
func (c *Client) AddToWatchlist(ctx context.Context, symbol string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	filter := bson.D{{Key: "symbol", Value: symbol}}
	update := bson.D{{Key: "$setOnInsert", Value: WatchlistItem{Symbol: symbol, AddedAt: time.Now()}}}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := c.db.Collection(watchlistCol).UpdateOne(ctx, filter, update, opts)
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
	opts := options.Find().SetSort(bson.D{{Key: "symbol", Value: 1}})
	cursor, err := c.db.Collection(watchlistCol).Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var items []WatchlistItem
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
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
