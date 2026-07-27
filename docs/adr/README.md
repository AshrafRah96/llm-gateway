# Decision records

Short notes on the decisions in this codebase that had a real tradeoff, and what got
rejected along the way. Written so that somebody reading the code later does not have to
guess why it looks like this.

| # | Decision | Why it was interesting |
|---|----------|------------------------|
| [0001](0001-keyword-routing-instead-of-a-classifier.md) | Route with keywords, not a classifier model | Paying per request to decide how much to spend is a bad trade |
| [0002](0002-cache-on-meaning-not-exact-text.md) | Cache on meaning, not exact text | Exact-match caching misses almost everything people actually send |
| [0003](0003-the-store-decides-the-rate-limit.md) | The rate limit store decides | The old interface forced record-before-decide, so refused requests consumed budget |
| [0004](0004-millisecond-scores-in-the-lua-script.md) | Millisecond scores in the Lua script | Redis Lua numbers are float64; nanosecond timestamps collapsed and let 12x through |
| [0005](0005-one-module-for-a-chat-request.md) | One module for a chat request | Two handlers with their own copies drifted, and streaming stopped billing |
| [0006](0006-estimate-and-bill-abandoned-streams.md) | Estimate and bill abandoned streams | Cancelling kills the usage chunk, so every abandoned stream was free |
| [0007](0007-isolate-semantic-cache-by-tenant-and-model.md) | Isolate the semantic cache | Similarity is not authorization; tenant and model are hard filters |

Numbers 3, 4 and 6 came out of finding bugs. Those are written up at greater length in
[../ENGINEERING-NOTES.md](../ENGINEERING-NOTES.md).
