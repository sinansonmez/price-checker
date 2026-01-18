# Price Checker

Terminal application to monitor washing machine prices daily, track changes in a CSV table, and send a Telegram summary.

## Requirements

- Go 1.22+
- Optional: Telegram bot token and chat ID for notifications

## Usage

```bash
go run . --run-once
```

Use custom URLs:

```bash
go run . --urls "https://example.com/item1,https://example.com/item2" --run-once
```

Or provide a file with one URL per line:

```bash
go run . --urls-file urls.txt --run-once
```

Run continuously (default 24h interval):

```bash
go run .
```

### Telegram configuration

Set the following environment variables to enable daily summaries:

```bash
export TELEGRAM_BOT_TOKEN="<bot_token>"
export TELEGRAM_CHAT_ID="<chat_id>"
```

#### Getting your Telegram bot token

1. Open Telegram and start a chat with **@BotFather**.
2. Send `/newbot` and follow the prompts to name your bot.
3. BotFather will return a token that looks like `123456789:ABCDEF...`. Use this as `TELEGRAM_BOT_TOKEN`.

#### Getting your chat ID

1. Start a chat with your bot (send any message so the bot can see you).
2. Open the following URL in a browser, replacing `<bot_token>` with your token:
   `https://api.telegram.org/bot<bot_token>/getUpdates`
3. Look for `"chat":{"id":...}` in the JSON response. The numeric value is your `TELEGRAM_CHAT_ID`.
   - For a group chat, add the bot to the group, send a message, then call `getUpdates` again.

## Data table

The application stores checks in `prices.csv` by default. Each run records:

- Timestamp
- URL
- Price and currency
- Delta from previous check
- Decrease flag

## Notes

- The scraper looks for price metadata and JSON-LD data; if the site layout changes, update the selectors in `main.go`.
