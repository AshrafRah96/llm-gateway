package handler

import (
	"errors"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
)

func (h *Handler) chatStream(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}

	// Checked before the upstream call so a client that cannot be streamed to does not
	// cost us a completion.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	stream, err := h.completion.Stream(r.Context(), req)
	if err != nil {
		// Propagate the upstream status, exactly as /chat does. A client that gets 429
		// can back off; one that gets 502 cannot.
		var upstream *completion.UpstreamError
		if errors.As(err, &upstream) {
			http.Error(w, "upstream error", upstream.Status)
			return
		}
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	// Close is what meters, logs and caches the stream.
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if stream.CacheHit() {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
		w.Header().Set("X-Model", stream.Model())
	}

	for data, ok := stream.Next(); ok; data, ok = stream.Next() {
		w.Write([]byte("data: " + data + "\n\n"))
		flusher.Flush()
	}
}
