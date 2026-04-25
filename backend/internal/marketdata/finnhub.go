package marketdata

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	finnhubQuoteAPI = "https://finnhub.io/api/v1/quote"
	finnhubCandleAPI = "https://finnhub.io/api/v1/stock/candle"
	usdkrwRateAPI   = "https://api.frankfurter.app/latest?from=USD&to=KRW"
	yahooChartAPI   = "https://query1.finance.yahoo.com/v8/finance/chart/"
	yahooQuoteSummaryAPI = "https://query1.finance.yahoo.com/v10/finance/quoteSummary/"
	yahooSearchAPI  = "https://query2.finance.yahoo.com/v1/finance/search"
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

type SymbolSearchResult struct {
	Symbol      string `json:"symbol"`
	Name        string `json:"name"`
	Exchange    string `json:"exchange"`
	TypeDisplay string `json:"type_display"`
}

type ValuationInputs struct {
	Symbol           string
	Currency         string
	CurrentPrice     float64
	TargetMeanPrice  float64
	TargetLowPrice   float64
	TargetHighPrice  float64
	FreeCashflow     float64
	EarningsGrowth   float64
	SharesOutstanding float64
	TrailingEPS      float64
	ForwardEPS       float64
	BookValue        float64
	DividendRate     float64
	DividendYield    float64
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

type finnhubCandleResponse struct {
	Status     string    `json:"s"`
	Timestamp  []int64   `json:"t"`
	Open       []float64 `json:"o"`
	High       []float64 `json:"h"`
	Low        []float64 `json:"l"`
	Close      []float64 `json:"c"`
	Volume     []float64 `json:"v"`
}

type yahooSearchResponse struct {
	Quotes []struct {
		Symbol   string `json:"symbol"`
		Short    string `json:"shortname"`
		Long     string `json:"longname"`
		Exchange string `json:"exchange"`
		TypeDisp string `json:"quoteType"`
	} `json:"quotes"`
}

type yahooNumberRaw struct {
	Raw float64 `json:"raw"`
}

type yahooQuoteSummaryResponse struct {
	QuoteSummary struct {
		Result []struct {
			Price struct {
				Currency           string         `json:"currency"`
				RegularMarketPrice yahooNumberRaw `json:"regularMarketPrice"`
			} `json:"price"`
			FinancialData struct {
				TargetMeanPrice yahooNumberRaw `json:"targetMeanPrice"`
				TargetLowPrice  yahooNumberRaw `json:"targetLowPrice"`
				TargetHighPrice yahooNumberRaw `json:"targetHighPrice"`
				FreeCashflow    yahooNumberRaw `json:"freeCashflow"`
				EarningsGrowth  yahooNumberRaw `json:"earningsGrowth"`
			} `json:"financialData"`
			DefaultKeyStatistics struct {
				SharesOutstanding yahooNumberRaw `json:"sharesOutstanding"`
				TrailingEps       yahooNumberRaw `json:"trailingEps"`
				ForwardEps        yahooNumberRaw `json:"forwardEps"`
				BookValue         yahooNumberRaw `json:"bookValue"`
			} `json:"defaultKeyStatistics"`
			SummaryDetail struct {
				DividendRate  yahooNumberRaw `json:"dividendRate"`
				DividendYield yahooNumberRaw `json:"dividendYield"`
			} `json:"summaryDetail"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"quoteSummary"`
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

func (c *Client) FetchValuationInputs(symbol string) (ValuationInputs, error) {
	yahooSymbol := toYahooSymbol(symbol)
	reqURL, err := url.Parse(yahooQuoteSummaryAPI + yahooSymbol)
	if err != nil {
		return ValuationInputs{}, fmt.Errorf("parse yahoo quoteSummary url: %w", err)
	}
	q := reqURL.Query()
	q.Set("modules", "price,financialData,defaultKeyStatistics,summaryDetail")
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return ValuationInputs{}, fmt.Errorf("build yahoo quoteSummary request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ValuationInputs{}, fmt.Errorf("request yahoo quoteSummary api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ValuationInputs{}, fmt.Errorf("yahoo quoteSummary api returned status %d", resp.StatusCode)
	}

	var decoded yahooQuoteSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return ValuationInputs{}, fmt.Errorf("decode yahoo quoteSummary response: %w", err)
	}

	if decoded.QuoteSummary.Error != nil {
		return ValuationInputs{}, fmt.Errorf("yahoo quoteSummary error: %s - %s", decoded.QuoteSummary.Error.Code, decoded.QuoteSummary.Error.Description)
	}
	if len(decoded.QuoteSummary.Result) == 0 {
		return ValuationInputs{}, fmt.Errorf("yahoo quoteSummary returned no results for %s", yahooSymbol)
	}

	r := decoded.QuoteSummary.Result[0]
	return ValuationInputs{
		Symbol:            strings.ToUpper(strings.TrimSpace(symbol)),
		Currency:          r.Price.Currency,
		CurrentPrice:      r.Price.RegularMarketPrice.Raw,
		TargetMeanPrice:   r.FinancialData.TargetMeanPrice.Raw,
		TargetLowPrice:    r.FinancialData.TargetLowPrice.Raw,
		TargetHighPrice:   r.FinancialData.TargetHighPrice.Raw,
		FreeCashflow:      r.FinancialData.FreeCashflow.Raw,
		EarningsGrowth:    r.FinancialData.EarningsGrowth.Raw,
		SharesOutstanding: r.DefaultKeyStatistics.SharesOutstanding.Raw,
		TrailingEPS:       r.DefaultKeyStatistics.TrailingEps.Raw,
		ForwardEPS:        r.DefaultKeyStatistics.ForwardEps.Raw,
		BookValue:         r.DefaultKeyStatistics.BookValue.Raw,
		DividendRate:      r.SummaryDetail.DividendRate.Raw,
		DividendYield:     r.SummaryDetail.DividendYield.Raw,
	}, nil
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

// FetchIntradayBars fetches short-term bars from Finnhub.
// resolution supports "5", "15", "30", "60", "120", "240" (minutes).
func (c *Client) FetchIntradayBars(symbol, resolution string, lookbackBars int) ([]OHLCVBar, error) {
	if lookbackBars <= 0 {
		return nil, fmt.Errorf("lookbackBars must be positive")
	}

	res := normalizeResolution(resolution)

	minutesPerBar := resolutionMinutes(res)
	if minutesPerBar <= 0 {
		return nil, fmt.Errorf("unsupported resolution %q", resolution)
	}

	// Prefer Finnhub for intraday, then gracefully fallback to Yahoo when unavailable (e.g., 403 plan limits).
	if c.apiKey != "" {
		if bars, err := c.fetchFinnhubIntradayBars(symbol, res, lookbackBars); err == nil {
			return bars, nil
		}
	}

	if bars, err := c.fetchYahooIntradayBars(symbol, res, lookbackBars); err == nil {
		return bars, nil
	}

	return nil, fmt.Errorf("unable to fetch intraday bars for %s (%s) from finnhub and yahoo", symbol, res)
}

func (c *Client) fetchFinnhubIntradayBars(symbol, resolution string, lookbackBars int) ([]OHLCVBar, error) {
	providerSymbol := toFinnhubSymbol(symbol)
	minutesPerBar := resolutionMinutes(resolution)

	toTs := time.Now().Unix()
	fromTs := time.Now().Add(-time.Duration(lookbackBars*minutesPerBar+minutesPerBar*8) * time.Minute).Unix()

	reqURL, err := url.Parse(finnhubCandleAPI)
	if err != nil {
		return nil, fmt.Errorf("parse finnhub candle url: %w", err)
	}
	q := reqURL.Query()
	q.Set("symbol", providerSymbol)
	q.Set("resolution", resolution)
	q.Set("from", fmt.Sprintf("%d", fromTs))
	q.Set("to", fmt.Sprintf("%d", toTs))
	q.Set("token", c.apiKey)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
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
		return nil, fmt.Errorf("finnhub candle api returned status %d", resp.StatusCode)
	}

	var decoded finnhubCandleResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode finnhub candle response: %w", err)
	}
	if decoded.Status != "ok" {
		return nil, fmt.Errorf("finnhub candle status=%s", decoded.Status)
	}

	n := len(decoded.Timestamp)
	if n == 0 {
		return nil, fmt.Errorf("finnhub candle returned no bars")
	}

	bars := make([]OHLCVBar, 0, n)
	for i := 0; i < n; i++ {
		if i >= len(decoded.Close) || decoded.Close[i] <= 0 {
			continue
		}
		bar := OHLCVBar{
			Timestamp: time.Unix(decoded.Timestamp[i], 0).UTC(),
			Close:     decoded.Close[i],
		}
		if i < len(decoded.Open) {
			bar.Open = decoded.Open[i]
		}
		if i < len(decoded.High) {
			bar.High = decoded.High[i]
		}
		if i < len(decoded.Low) {
			bar.Low = decoded.Low[i]
		}
		if i < len(decoded.Volume) {
			bar.Volume = decoded.Volume[i]
		}
		bars = append(bars, bar)
	}

	if len(bars) == 0 {
		return nil, fmt.Errorf("finnhub candle returned no valid bars")
	}
	if len(bars) > lookbackBars {
		bars = bars[len(bars)-lookbackBars:]
	}

	return bars, nil
}

func (c *Client) fetchYahooIntradayBars(symbol, resolution string, lookbackBars int) ([]OHLCVBar, error) {
	yahooSymbol := toYahooSymbol(symbol)
	baseInterval := yahooIntervalForResolution(resolution)
	if baseInterval == "" {
		return nil, fmt.Errorf("unsupported resolution for yahoo intraday: %s", resolution)
	}

	rangeParam := yahooIntradayRange(resolution, lookbackBars)
	reqURL, err := url.Parse(yahooChartAPI + yahooSymbol)
	if err != nil {
		return nil, fmt.Errorf("parse yahoo intraday url: %w", err)
	}
	q := reqURL.Query()
	q.Set("interval", baseInterval)
	q.Set("range", rangeParam)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build yahoo intraday request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request yahoo intraday api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo intraday api returned status %d", resp.StatusCode)
	}

	var decoded yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode yahoo intraday response: %w", err)
	}
	if decoded.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo intraday error: %s - %s", decoded.Chart.Error.Code, decoded.Chart.Error.Description)
	}
	if len(decoded.Chart.Result) == 0 || len(decoded.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo intraday returned empty result")
	}

	result := decoded.Chart.Result[0]
	quote := result.Indicators.Quote[0]
	raw := make([]OHLCVBar, 0, len(result.Timestamp))
	for i := 0; i < len(result.Timestamp); i++ {
		if i >= len(quote.Close) || quote.Close[i] <= 0 {
			continue
		}
		b := OHLCVBar{Timestamp: time.Unix(result.Timestamp[i], 0).UTC(), Close: quote.Close[i]}
		if i < len(quote.Open) {
			b.Open = quote.Open[i]
		}
		if i < len(quote.High) {
			b.High = quote.High[i]
		}
		if i < len(quote.Low) {
			b.Low = quote.Low[i]
		}
		if i < len(quote.Volume) {
			b.Volume = quote.Volume[i]
		}
		raw = append(raw, b)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("yahoo intraday returned no valid bars")
	}

	baseMinutes := intervalMinutes(baseInterval)
	targetMinutes := resolutionMinutes(resolution)
	if baseMinutes <= 0 || targetMinutes <= 0 {
		return nil, fmt.Errorf("invalid interval mapping")
	}

	bars := raw
	if targetMinutes > baseMinutes {
		factor := targetMinutes / baseMinutes
		if factor > 1 {
			bars = aggregateBars(raw, factor)
		}
	}

	if len(bars) > lookbackBars {
		bars = bars[len(bars)-lookbackBars:]
	}
	return bars, nil
}

func aggregateBars(src []OHLCVBar, factor int) []OHLCVBar {
	if factor <= 1 || len(src) == 0 {
		return src
	}
	out := make([]OHLCVBar, 0, int(math.Ceil(float64(len(src))/float64(factor))))
	for i := 0; i < len(src); i += factor {
		end := i + factor
		if end > len(src) {
			end = len(src)
		}
		chunk := src[i:end]
		if len(chunk) == 0 {
			continue
		}
		bar := OHLCVBar{
			Timestamp: chunk[0].Timestamp,
			Open:      chunk[0].Open,
			High:      chunk[0].High,
			Low:       chunk[0].Low,
			Close:     chunk[len(chunk)-1].Close,
		}
		for _, c := range chunk {
			if c.High > bar.High {
				bar.High = c.High
			}
			if bar.Low == 0 || (c.Low > 0 && c.Low < bar.Low) {
				bar.Low = c.Low
			}
			bar.Volume += c.Volume
		}
		out = append(out, bar)
	}
	return out
}

func yahooIntervalForResolution(resolution string) string {
	switch resolution {
	case "5":
		return "5m"
	case "15":
		return "15m"
	case "30":
		return "30m"
	case "60", "120", "240", "300":
		return "60m"
	default:
		return ""
	}
}

func yahooIntradayRange(resolution string, lookbackBars int) string {
	totalMinutes := resolutionMinutes(resolution) * lookbackBars
	switch {
	case totalMinutes <= 24*60:
		return "1d"
	case totalMinutes <= 5*24*60:
		return "5d"
	case totalMinutes <= 30*24*60:
		return "1mo"
	case totalMinutes <= 90*24*60:
		return "3mo"
	default:
		return "6mo"
	}
}

func intervalMinutes(interval string) int {
	switch interval {
	case "5m":
		return 5
	case "15m":
		return 15
	case "30m":
		return 30
	case "60m":
		return 60
	default:
		return 0
	}
}

func (c *Client) SearchSymbols(query string, limit int) ([]SymbolSearchResult, error) {
	qv := strings.TrimSpace(query)
	if qv == "" {
		return []SymbolSearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}

	reqURL, err := url.Parse(yahooSearchAPI)
	if err != nil {
		return nil, fmt.Errorf("parse yahoo search url: %w", err)
	}
	q := reqURL.Query()
	q.Set("q", qv)
	q.Set("quotesCount", fmt.Sprintf("%d", limit*2))
	q.Set("newsCount", "0")
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build yahoo search request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request yahoo search api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo search api returned status %d", resp.StatusCode)
	}

	var decoded yahooSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode yahoo search response: %w", err)
	}

	results := make([]SymbolSearchResult, 0, limit)
	for _, item := range decoded.Quotes {
		if item.Symbol == "" {
			continue
		}
		name := item.Long
		if name == "" {
			name = item.Short
		}
		results = append(results, SymbolSearchResult{
			Symbol:      strings.ToUpper(item.Symbol),
			Name:        name,
			Exchange:    item.Exchange,
			TypeDisplay: item.TypeDisp,
		})
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

func normalizeResolution(resolution string) string {
	r := strings.TrimSpace(strings.ToLower(resolution))
	switch r {
	case "5", "5m", "m5":
		return "5"
	case "15", "15m", "m15":
		return "15"
	case "30", "30m", "m30":
		return "30"
	case "60", "1h", "h1":
		return "60"
	case "120", "2h", "h2":
		return "120"
	case "240", "4h", "h4":
		return "240"
	case "300", "5h", "h5":
		return "300"
	default:
		return resolution
	}
}

func resolutionMinutes(resolution string) int {
	switch resolution {
	case "5":
		return 5
	case "15":
		return 15
	case "30":
		return 30
	case "60":
		return 60
	case "120":
		return 120
	case "240":
		return 240
	case "300":
		return 300
	default:
		return 0
	}
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
