# Engineering notes

Two silent money bugs. Tests agreed with implementation instead of checking its contract.
These are historical failures; current regression tests pin both fixes.

## 1. The rate limiter that allowed twelve times its limit

The gateway returned plausible quota decisions and `Retry-After` headers while admitting excess traffic.
A sliding window stores accepted requests in a Redis sorted set, scored by arrival time.
The first version used the same nanosecond timestamp for score and member:

```lua
redis.call('ZADD', key, now, now)   -- now = time.Now().UnixNano()
```

Redis Lua numbers use float64. Exact integers stop at 2^53, about 9.0e15.
Nanosecond timestamps are around 1.8e18. They lose precision; Lua's `%.14g` string conversion
turns `1771848291234567890` into `1.7717e+18`. Distinct timestamps collapse to the same member.
`ZADD` updates repeated members instead of inserting requests. The set stays small; quota stays open.

Reading found the suspect timestamp. A portable Redis run confirmed it after the original
Redis-dependent test had silently skipped:

```
window holds 4 of 50 accepted requests
request over the limit was allowed
```

Forty-six of fifty requests vanished. [ADR-0004](adr/0004-millisecond-scores-in-the-lua-script.md)
records the measured Redis 5.0.14 reproduction.

Fix: score in milliseconds, around 1.8e12. Pass a separate random member from Go.
Same-millisecond requests no longer deterministically overwrite each other.
`TestRedisStore_BurstInSameMillisecondCountsIndividually` requires all 50 burst requests to remain.
CI runs Redis Stack and rejects skipped Redis-backed tests.

A skipped test is not a passed test. Mocks that repeat the implementation can repeat its bug.

## 2. The stream that charged nothing

Two faults hid streamed spend.

First, HTTP handlers held duplicate pipelines. `/chat/stream` omitted cache lookup/storage,
usage, cost, and logs. Streamed traffic disappeared from `/usage`.
[ADR-0005](adr/0005-one-module-for-a-chat-request.md) moved shared policy into `internal/completion`.

Second, final provider usage arrives only with `stream_options.include_usage`.
Cancelling the request reaches `http.NewRequestWithContext`, stops the upstream read,
and prevents the usage chunk from arriving. Tokens then stayed zero.
User cancellation is routine; describing it as rare hid the impact.

A slow local HTTP provider and cancellation after the first chunk reproduced it:

```
chunks read before the stream died: 1
scanner error: context canceled
usage chunk received: false
RESULT: usage LOST - tokens would be recorded as zero
```

Fix: estimate consumed work when provider usage is absent. Mark usage and logs as estimated.
Current input estimates include system policy, encoded references, and question.
Output estimates include received content. Cancellation still stops upstream work.
[ADR-0006](adr/0006-estimate-and-bill-abandoned-streams.md) explains why draining the
unwanted answer for exact usage was rejected.

`TestChatStream_AbandonedByClientIsStillBilled` uses the real adapter with a slow local provider.
It requires positive estimated usage. Removing estimation restores these failures:

```
prompt tokens billed as zero; the provider charges the full prompt regardless
cost = 0; an abandoned stream is not free
```

Normal exhaustion settles immediately. Deferred `Close` handles abandonment through the
same idempotent path. Detached settlement has a 5s budget. Partial streams never enter cache.

## Shared lesson

Both fixes were verified by breaking the code, observing the new regression fail, then restoring the fix.
Tests need evidence that they detect the failure. A written caveat does not handle a defect.
