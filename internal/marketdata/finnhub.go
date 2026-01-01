package marketdata

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const finnhubQuoteAPI = "https://finnhub.io/api/v1/quote"
const finnhubCandleAPI = "https://finnhub.io/api/v1/stock/candle"
const usdkrwRateAPI = "https://api.frankfurter.app/latest?from=USD&to=KRW"
const stooqDailyCSVAPI = "https://stooq.com/q/d/l/"

type Client struct {
	apiKey     string
	httpClient *http.Client
}

type Quote struct {
	Symbol        string
	Price         float64
	PreviousClose float64
	ChangePercent float64
}

type finnhubQuoteResponse struct {
	CurrentPrice  float64 `json:"c"`
	PreviousClose float64 `json:"pc"`
}

type fxRateResponse struct {
	Rates map[string]float64 `json:"rates"`
}

type finnhubCandleResponse struct {
	Close  []float64 `json:"c"`
	Status string    `json:"s"`
}

func NewClient() *Client {
	return &Client{
		apiKey:     os.Getenv("FINNHUB_API_KEY"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) FetchQuotes(symbols []string) (map[string]Quote, error) {
	if len(symbols) == 0 {
		return nil, fmt.Errorf("symbols cannot be empty")
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("FINNHUB_API_KEY is required")
	}

	quotes := make(map[string]Quote, len(symbols))
	for _, symbol := range symbols {
		providerSymbol := toFinnhubSymbol(symbol)
		quote, err := c.fetchFinnhubQuote(providerSymbol)
		if err != nil {
			return nil, fmt.Errorf("fetch %s (%s): %w", symbol, providerSymbol, err)
		}
		quotes[symbol] = makeQuote(symbol, quote.CurrentPrice, quote.PreviousClose)
	}

	if len(quotes) != len(symbols) {
		return nil, fmt.Errorf("partial quotes returned: got %d, want %d", len(quotes), len(symbols))
	}

	return quotes, nil
}

func (c *Client) FetchUSDKRWRate() (float64, error) {
	req, err := http.NewRequest(http.MethodGet, usdkrwRateAPI, nil)
	if err != nil {
		return 0, fmt.Errorf("build usdkrw request: %w", err)
	}
	req.Header.Set("User-Agent", "midas-touch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request usdkrw rate api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("usdkrw rate api returned status %d", resp.StatusCode)
	}

	var decoded fxRateResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("decode usdkrw rate response: %w", err)
	}

	rate, ok := decoded.Rates["KRW"]
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("invalid usdkrw rate response")
	}

	return rate, nil
}

func (c *Client) FetchDailyCloses(symbol string, lookbackDays int) ([]float64, error) {
	if lookbackDays <= 0 {
		return nil, fmt.Errorf("lookbackDays must be positive")
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("FINNHUB_API_KEY is required")
	}

	providerSymbol := toFinnhubSymbol(symbol)
	requestURL, err := url.Parse(finnhubCandleAPI)
	if err != nil {
		return nil, fmt.Errorf("parse finnhub candle api url: %w", err)
	}

	to := time.Now().Unix()
	from := time.Now().AddDate(0, 0, -(lookbackDays * 2)).Unix()

	q := requestURL.Query()
	q.Set("symbol", providerSymbol)
	q.Set("resolution", "D")
	q.Set("from", strconv.FormatInt(from, 10))
	q.Set("to", strconv.FormatInt(to, 10))
	q.Set("token", c.apiKey)
	requestURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build finnhub candle request: %w", err)
	}
	req.Header.Set("User-Agent", "midas-touch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request finnhub candle api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.fetchDailyClosesFromStooq(symbol, lookbackDays)
	}

	var decoded finnhubCandleResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode finnhub candle response: %w", err)
	}
	if decoded.Status != "ok" || len(decoded.Close) == 0 {
		return c.fetchDailyClosesFromStooq(symbol, lookbackDays)
	}

	closes := decoded.Close
	if len(closes) > lookbackDays+5 {
		closes = closes[len(closes)-(lookbackDays+5):]
	}

	return closes, nil
}

func (c *Client) fetchDailyClosesFromStooq(symbol string, lookbackDays int) ([]float64, error) {
	stooqSymbol := toStooqSymbol(symbol)
	if stooqSymbol == "" {
		return nil, fmt.Errorf("no stooq mapping for symbol %s", symbol)
	}

	requestURL, err := url.Parse(stooqDailyCSVAPI)
	if err != nil {
		return nil, fmt.Errorf("parse stooq api url: %w", err)
	}
	q := requestURL.Query()
	q.Set("s", stooqSymbol)
	q.Set("i", "d")
	requestURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build stooq request: %w", err)
	}
	req.Header.Set("User-Agent", "midas-touch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request stooq api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stooq api returned status %d", resp.StatusCode)
	}

	reader := csv.NewReader(resp.Body)
	_, err = reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read stooq header: %w", err)
	}

	closes := make([]float64, 0, lookbackDays+5)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read stooq row: %w", err)
		}
		if len(record) < 5 {
			continue
		}
		closePrice, err := strconv.ParseFloat(record[4], 64)
		if err != nil || closePrice <= 0 {
			continue
		}
		closes = append(closes, closePrice)
	}

	if len(closes) == 0 {
		return nil, fmt.Errorf("stooq returned no valid closes")
	}

	if len(closes) > lookbackDays+5 {
		closes = closes[len(closes)-(lookbackDays+5):]
	}

	return closes, nil
}

func (c *Client) fetchFinnhubQuote(symbol string) (finnhubQuoteResponse, error) {
	requestURL, err := url.Parse(finnhubQuoteAPI)
	if err != nil {
		return finnhubQuoteResponse{}, fmt.Errorf("parse finnhub api url: %w", err)
	}

	q := requestURL.Query()
	q.Set("symbol", symbol)
	q.Set("token", c.apiKey)
	requestURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return finnhubQuoteResponse{}, fmt.Errorf("build finnhub request: %w", err)
	}
	req.Header.Set("User-Agent", "midas-touch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return finnhubQuoteResponse{}, fmt.Errorf("request finnhub quote api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return finnhubQuoteResponse{}, fmt.Errorf("finnhub quote api returned status %d", resp.StatusCode)
	}

	var decoded finnhubQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return finnhubQuoteResponse{}, fmt.Errorf("decode finnhub quote response: %w", err)
	}
	if decoded.PreviousClose == 0 {
		return finnhubQuoteResponse{}, fmt.Errorf("invalid previous close in finnhub response")
	}

	return decoded, nil
}

func makeQuote(symbol string, price, previousClose float64) Quote {
	changePct := ((price - previousClose) / previousClose) * 100
	return Quote{
		Symbol:        symbol,
		Price:         price,
		PreviousClose: previousClose,
		ChangePercent: changePct,
	}
}

func toFinnhubSymbol(symbol string) string {
	switch symbol {
	case "^VIX":
		return "VIXY"
	case "NQ=F":
		return "QQQ"
	case "USDKRW=X":
		return "UUP"
	default:
		return symbol
	}
}

func toStooqSymbol(symbol string) string {
	switch symbol {
	case "NVDA":
		return "nvda.us"
	case "^VIX":
		return "vixy.us"
	case "NQ=F":
		return "qqq.us"
	case "USDKRW=X":
		return "uup.us"
	default:
		return ""
	}
}
