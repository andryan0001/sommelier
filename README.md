# sommelier

An AI agent harness that searches nhentai's catalog and ranks galleries by narrative quality rather than tag density — returning a shortlist for a natural-language request like *"slow-burn romance, English, completed, under 60 pages."*

## Status

Early. API v2 access request pending with nhentai support; no implementation yet.

## Approach

1. Parse the request into keywords, tag filters, and numeric constraints (`pages:`, `uploaded:`, `language:`).
2. Query `/api/v2/search` and `/api/v2/galleries`, then open candidates via `/api/v2/galleries/{gallery_id}` for metadata.
3. Score candidates on story signals — page count, artist consistency, tag coherence, completion status — and return a ranked shortlist.

## API notes

Read-only against the [nhentai API v2](https://nhentai.net/api/v2/docs). Descriptive `User-Agent`, client-side token bucket per endpoint, exponential backoff on 429, metadata cached, no media mirrored.
