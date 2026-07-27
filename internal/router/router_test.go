package router

import "testing"

func TestRoute(t *testing.T) {
	tests := []struct {
		prompt string
		want   Model
	}{
		{"What is 2+2?", Cheap},
		{"Hello", Cheap},
		{"analyze this data", Powerful},
		{"please code a function", Powerful},
		{"compare these options", Powerful},
		{"debug this issue", Powerful},
		{"explain step by step how to", Powerful},
		{string(make([]byte, 501)), Powerful}, // long prompt
	}

	for _, tt := range tests {
		got := Route(tt.prompt)
		if got != tt.want {
			t.Errorf("Route(%q) = %v, want %v", tt.prompt, got, tt.want)
		}
	}
}
