package marketdata

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	finnhubQuoteAPI = "https://finnhub.io/api/v1/quote"
	usdkrwRateAPI   = "https://api.frankfurter.app/latest?from=USD&to=KRW"
	yahooChartAPI   = "https://query1.finance.yahoo.com/v8/finance/chart/"
)

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

// OHLCVBar holds full OHLCV data for a single day.
type OHLCVBar struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

type finnhubQuoteResponse struct {
	CurrentPrice  float64 `json:"c"`
	PreviousClose float64 `json:"pc"`
}

type fxRateResponse struct {
	Rates map[string]float64 `json:"rates"`
}

// yahooChartResponse mirrors the Yahoo Finance v8 chart JSON structure.
type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []float64 `json:"open"`
					High   []float64 `json:"high"`
					Low    []float64 `json:"low"`
					Close  []float64 `json:"close"`
					Volume []float64 `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func NewClient() *Client {
	return &Client{
		apiKey:     os.Getenv("FINNHUB_API_KEY"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
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

// FetchDailyCloses returns only close prices.
func (c *Client) FetchDailyCloses(symbol string, lookbackDays int) ([]float64, error) {
	bars, err := c.FetchDailyBars(symbol, lookbackDays)
	if err != nil {
		return nil, err
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	return closes, nil
}

// FetchDailyBars fetches OHLCV bars from Yahoo Finance.
func (c *Client) FetchDailyBars(symbol string, lookbackDays int) ([]OHLCVBar, error) {
	if lookbackDays <= 0 {
		return nil, fmt.Errorf("lookbackDays must be positive")
	}

	yahooSymbol := toYahooSymbol(symbol)

	// Yahoo range: pick the smallest range that covers lookbackDays
	rangeParam := "2y"
	if lookbackDays <= 30 {
		rangeParam = "3mo"
	} else if lookbackDays <= 90 {
		rangeParam = "6mo"
	} else if lookbackDays <= 180 {
		rangeParam = "1y"
	}

	reqURL, err := url.Parse(yahooChartAPI + yahooSymbol)
	if err != nil {
		return nil, fmt.Errorf("parse yahoo url: %w", err)
	}
	q := reqURL.Query()
	q.Set("interval", "1d")
	q.Set("range", rangeParam)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build yahoo request: %w", err)
	}
	// Yahoo requires browser-like headers to avoid 401/empty responses
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request yahoo chart api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo chart api returned status %d for %s", resp.StatusCode, yahooSymbol)
	}

	var decoded yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode yahoo chart response: %w", err)
	}

	if decoded.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo chart error: %s - %s", decoded.Chart.Error.Code, decoded.Chart.Error.Description)
	}

	if len(decoded.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo chart returned no results for %s", yahooSymbol)
	}

	result := decoded.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo chart returned no quote data for %s", yahooSymbol)
	}

	quotes := result.Indicators.Quote[0]
	n := len(result.Timestamp)
	if n == 0 {
		return nil, fmt.Errorf("yahoo chart returned empty timestamps for %s", yahooSymbol)
	}

	bars := make([]OHLCVBar, 0, n)
	for i := 0; i < n; i++ {
		// Skip bars with nil/zero close (market holidays can produce null values)
		var closeVal float64
		if i < len(quotes.Close) {
			closeVal = quotes.Close[i]
		}
		if closeVal <= 0 {
			continue
		}

		bar := OHLCVBar{
			Timestamp: time.Unix(result.Timestamp[i], 0).UTC(),
			Close:     closeVal,
		}
		if i < len(quotes.Open) {
			bar.Open = quotes.Open[i]
		}
		if i < len(quotes.High) {
			bar.High = quotes.High[i]
		}
		if i < len(quotes.Low) {
			bar.Low = quotes.Low[i]
		}
		if i < len(quotes.Volume) {
			bar.Volume = quotes.Volume[i]
		}
		bars = append(bars, bar)
	}

	if len(bars) == 0 {
		return nil, fmt.Errorf("yahoo chart returned no valid bars for %s", yahooSymbol)
	}

	// Trim to requested lookback
	if len(bars) > lookbackDays+5 {
		bars = bars[len(bars)-(lookbackDays+5):]
	}

	return bars, nil
}

func (c *Client) fetchFinnhubQuote(symbol string) (finnhubQuoteResponse, error) {
	reqURL, err := url.Parse(finnhubQuoteAPI)
	if err != nil {
		return finnhubQuoteResponse{}, fmt.Errorf("parse finnhub api url: %w", err)
	}

	q := reqURL.Query()
	q.Set("symbol", symbol)
	q.Set("token", c.apiKey)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
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
		return finnhubQuoteResponse{}, fmt.Errorf("invalid previous close in finnhub response for %s", symbol)
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

// toFinnhubSymbol maps internal symbols to Finnhub-compatible tickers.
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

// toYahooSymbol maps internal symbols to Yahoo Finance tickers.
func toYahooSymbol(symbol string) string {
	switch symbol {
	case "^VIX":
		return "%5EVIX"
	case "NQ=F":
		return "NQ%3DF"
	case "USDKRW=X":
		return "USDKRW%3DX"
	default:
		return symbol
	}
}
