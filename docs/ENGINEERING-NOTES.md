# Engineering notes

Two silent bugs in this codebase affected money without failing a test. In both cases,
the tests copied the implementation closely enough to copy its mistake.

These notes cover how the bugs behaved, how they escaped the suite and what now catches
them.

---

## 1. The rate limiter that allowed twelve times its limit

### Symptom

None. That is the problem. The limiter accepted requests, refused requests, returned
sensible looking `Retry-After` headers, and passed its tests. Under load it would have
let roughly twelve times the configured rate through to OpenAI, and nothing anywhere
would have said so.

### Where it came from

The sliding window is a Redis sorted set. Each accepted request is one member, scored by
the time it arrived. Trim everything older than the window, count what is left, compare
to the limit.

The first version scored and identified each request by the same nanosecond timestamp:

```lua
redis.call('ZADD', key, now, now)   -- now = time.Now().UnixNano()
```

Redis runs Lua 5.1. Every number there is a float64, so exact integers stop at 2^53,
about 9.0e15. A nanosecond timestamp is roughly 1.8e18. It does not fit.

Two things then go wrong at once. The value loses precision, and Lua formats it with
`%.14g` when converting to a string, which turns `1771848291234567890` into
`1.7717e+18`. Lots of different timestamps produce that same string.

`ZADD` treats a repeated member as an update rather than an insertion. So requests
overwrote each other. The set stayed almost empty regardless of traffic, the count stayed
under the limit, and the limiter kept saying yes.

### How it was found

By reading, then confirmed by running. There was no Redis or Docker on the machine, so
the script had never executed even once. The test that covered it skipped itself
silently, which is a green suite that proves nothing.

Reading the script with float64 semantics in mind made the timestamp look wrong. That is
a suspicion, not a finding. Getting a portable Redis binary and running it turned it into
one:

```
window holds 4 of 50 accepted requests
request over the limit was allowed
```

Forty six of fifty requests had vanished.

### The fix

Score in milliseconds, which is about 1.8e12 and comfortably inside the exact range. Pass
the sorted set member in from Go as a separate random string so it never derives from the
score and two requests in the same millisecond cannot collide.

Full reasoning in [adr/0004](adr/0004-millisecond-scores-in-the-lua-script.md).

### What pins it now

`TestRedisStore_BurstInSameMillisecondCountsIndividually` sends 50 rapid requests and
requires all 50 to be in the window afterwards. Against the old version it fails with
the message above.

CI runs a Redis service and fails the build if those tests skip, because a skipped test
is how this survived in the first place.

### What it taught me

A test that skips is not a test that passes. The suite was green, the coverage looked
fine, and the only code in the repository that had never run was the code with the bug
in it.

---

## 2. The stream that charged nothing

### Symptom

Streamed requests appeared in `/usage` with zero tokens and zero cost. Not missing.
Zero, which is indistinguishable from a request that never happened.

### Where it came from

This one arrived in two layers, and I only found the second because somebody pushed back
on how I described the first.

The first layer was structural. `/chat` and `/chat/stream` were separate
`http.HandlerFunc`s, each with its own copy of the pipeline. The streaming one had been
written by copying part of the other, and it was missing the cache lookup, the cache
write, the usage recording, the cost calculation and the logging. Nothing named the
sequence, so nothing noticed the gaps. Fixed by moving the whole thing into
`internal/completion` ([adr/0005](adr/0005-one-module-for-a-chat-request.md)).

The second layer was worse and survived that fix. To meter a stream you need OpenAI's
token counts, which only arrive if you set `stream_options.include_usage`, and then only
on the very last chunk. Every chunk before it reports usage as null.

When a client stops reading, its request context is cancelled. That context reaches the
upstream call through `http.NewRequestWithContext`, so cancelling kills our read of
OpenAI's response mid stream. The last chunk never arrives. Tokens stay at zero.

I had written this off in a summary as a rare interruption case. It is not rare. It is
what happens every single time a user hits stop, which on a chat interface is constant.

### How it was found

Somebody read "interrupted streams under-bill" in my own notes and asked whether hiding
costs was acceptable. It was a fair question about a caveat I had written and then moved
past.

Rather than reason about the frequency, I wrote a throwaway test: an HTTP server that
streams slowly and sends usage last, a client that cancels after the first chunk.

```
chunks read before the stream died: 1
scanner error: context canceled
usage chunk received: false
RESULT: usage LOST - tokens would be recorded as zero
```

### The fix

Estimate. The inputs are better than they sound: the prompt is known exactly, and the
partial answer is exactly what arrived before the cut. Charge it, and mark it as an
estimate everywhere it appears, in the usage counters and in the log.

Cancelling still drops the upstream connection, so OpenAI still stops generating. Real
spend is unchanged. The only thing that changed is that it gets recorded.

Full reasoning, including why draining the stream for exact numbers was the wrong answer,
in [adr/0006](adr/0006-estimate-and-bill-abandoned-streams.md).

### What pins it now

`TestChatStream_AbandonedByClientIsStillBilled` runs a real `OpenAIClient` against a
slow test server, cancels the request context partway through, and requires a non-zero
charge marked as estimated. Remove the estimation and it fails with exactly the symptom:

```
prompt tokens billed as zero; the provider charges the full prompt regardless
cost = 0; an abandoned stream is not free
```

### What it taught me

I had already found this bug and then talked myself out of it, by calling it rare in a
summary and moving on. The word "interrupted" made it sound like a network fault instead
of a user clicking a button.

A written caveat is still an unfixed caveat. Calling something "known" can feel like
progress even when nobody has dealt with it.

---

## A pattern across both

Neither bug was subtle in retrospect. Both were invisible because something that looked
like verification was not verification.

The rate limiter had a mock that incremented a counter exactly the way the real store
did, so it reproduced the bug faithfully and asserted it was correct. Writing tests
against your own implementation gets you tests that agree with your mistakes.

Both fixes were checked the same way: break the code deliberately, confirm the new test
fails, then put it back. A test that has never been seen to fail is a guess about what it
covers.
