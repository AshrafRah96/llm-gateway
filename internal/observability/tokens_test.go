package observability

import "testing"

func TestParseTokens(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		wantIn    int
		wantOut   int
	}{
		{
			name:    "valid response",
			body:    []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":20}}`),
			wantIn:  10,
			wantOut: 20,
		},
		{
			name:    "empty response",
			body:    []byte(`{}`),
			wantIn:  0,
			wantOut: 0,
		},
		{
			name:    "invalid json",
			body:    []byte(`not json`),
			wantIn:  0,
			wantOut: 0,
		},
		{
			name:    "nil body",
			body:    nil,
			wantIn:  0,
			wantOut: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIn, gotOut := ParseTokens(tt.body)
			if gotIn != tt.wantIn || gotOut != tt.wantOut {
				t.Errorf("ParseTokens() = (%d, %d), want (%d, %d)", gotIn, gotOut, tt.wantIn, tt.wantOut)
			}
		})
	}
}

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		model   string
		in, out int
		want    float64
	}{
		{"gpt-3.5-turbo", 1000, 1000, 0.002},
		{"gpt-4", 1000, 1000, 0.09},
		{"unknown", 1000, 1000, 0},
		{"gpt-3.5-turbo", 0, 0, 0},
	}

	for _, tt := range tests {
		got := CalculateCost(tt.model, tt.in, tt.out)
		if got != tt.want {
			t.Errorf("CalculateCost(%s, %d, %d) = %f, want %f", tt.model, tt.in, tt.out, got, tt.want)
		}
	}
}
