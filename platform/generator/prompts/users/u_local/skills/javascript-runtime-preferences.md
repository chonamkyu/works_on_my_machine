## HTML Parsing
- Use cheerio for HTML parsing (preferred over jsdom)
- Lightweight and sufficient for most DOM queries

## Fetch & Network
- Wrap all fetch calls with AbortController
- Set 10s timeout on all network requests
- Example: `AbortSignal.timeout(10000)`

## Logging
- Use `ctx.log.info()` instead of `console.log()`
- Use `ctx.log.error()` for errors
- Avoid console methods entirely