package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
)

type streamResponse struct {
	Choices []streamResponseChoice `json:"choices"`
}

type streamResponseChoice struct {
	Delta streamResponseDelta `json:"delta"`
}

type streamResponseDelta struct {
	Content string `json:"content"`
}

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

	for chunk, ok := stream.Next(); ok; chunk, ok = stream.Next() {
		data := []byte("[DONE]")
		if !chunk.Done {
			data, err = json.Marshal(streamResponse{
				Choices: []streamResponseChoice{{
					Delta: streamResponseDelta{Content: chunk.Content},
				}},
			})
			if err != nil {
				return
			}
		}
		w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
		flusher.Flush()
	}
}
