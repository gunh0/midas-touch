# midas-touch

Go CLI for Telegram notifications and hourly NVDA trading guidance.

## Quick Start

1. Create a bot with [@BotFather](https://t.me/BotFather).
2. Add the bot to your channel as admin.
3. Copy env template and fill values.

```bash
cp .env.example .env
```

```env
TELEGRAM_BOT_TOKEN=your_bot_token
TELEGRAM_CHAT_ID=your_channel_chat_id
FINNHUB_API_KEY=your_finnhub_api_key
```

## Commands

```bash
# Telegram send test
make test

# Trading advisor: one-time forced send (test)
make advisor-test

# Trading advisor: run every hour
make advisor
```

## Advisor Output (Current)

- Target: NVDA
- Frequency: hourly
- Decision: Buy / Sell / Hold percentages
- Horizons: Daily, Weekly, Monthly, Quarterly, Yearly
- Indicators: momentum, SMA20/50, RSI14, macro proxies
- FX display: all USD prices include KRW converted value in parentheses with comma formatting

## Data Sources

- Real-time quotes: Finnhub
- Historical daily closes: Finnhub candle API, with Stooq fallback
- USD/KRW spot: Frankfurter API

## Symbol Mapping (Current)

- `^VIX` -> `VIXY`
- `NQ=F` -> `QQQ`
- `USDKRW=X` -> `UUP`
