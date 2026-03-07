package handler

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ashrafrah96/llm-gateway/internal/router"
)

func (h *Handler) chatStream(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	model := router.Route(req.Prompt)

	stream, status, err := h.client.CallStream(req.Prompt, string(model))
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	if status != http.StatusOK {
		http.Error(w, "upstream error", status)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Model", string(model))

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				break
			}
			w.Write([]byte("data: " + data + "\n\n"))
			flusher.Flush()
		}
	}
}
