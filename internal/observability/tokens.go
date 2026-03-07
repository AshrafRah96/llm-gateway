package observability

import "encoding/json"

type openAIResponse struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func ParseTokens(body []byte) (tokensIn, tokensOut int) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
}
