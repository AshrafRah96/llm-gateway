package observability

import "testing"

func TestParseTokens(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		wantIn  int
		wantOut int
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
