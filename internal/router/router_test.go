package router

import "testing"

func TestRoute(t *testing.T) {
	tests := []struct {
		prompt string
		want   Model
	}{
		{"What is 2+2?", ModelCheap},
		{"Hello", ModelCheap},
		{"analyze this data", ModelPowerful},
		{"please code a function", ModelPowerful},
		{"compare these options", ModelPowerful},
		{"debug this issue", ModelPowerful},
		{"explain step by step how to", ModelPowerful},
		{string(make([]byte, 501)), ModelPowerful}, // long prompt
	}

	for _, tt := range tests {
		got := Route(tt.prompt)
		if got != tt.want {
			t.Errorf("Route(%q) = %v, want %v", tt.prompt, got, tt.want)
		}
	}
}
