package router

import "testing"

func TestModelCost(t *testing.T) {
	tests := []struct {
		name    string
		model   Model
		in, out int
		want    float64
	}{
		{"cheap", Cheap, 1000, 1000, 0.002},
		{"powerful", Powerful, 1000, 1000, 0.09},
		{"cheap zero tokens", Cheap, 0, 0, 0},
		{"input only", Powerful, 1000, 0, 0.03},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.Cost(tt.in, tt.out); got != tt.want {
				t.Errorf("%s.Cost(%d, %d) = %f, want %f", tt.model.ID, tt.in, tt.out, got, tt.want)
			}
		})
	}
}

// The catalogue is the single source for ids, descriptions and prices.
func TestAll(t *testing.T) {
	all := All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d models, want 2", len(all))
	}

	seen := map[string]bool{}
	for _, m := range all {
		if m.ID == "" {
			t.Error("model with empty ID")
		}
		if m.Description == "" {
			t.Errorf("%s has no description", m.ID)
		}
		if m.PriceIn <= 0 || m.PriceOut <= 0 {
			t.Errorf("%s is unpriced: in=%f out=%f", m.ID, m.PriceIn, m.PriceOut)
		}
		if seen[m.ID] {
			t.Errorf("%s listed twice", m.ID)
		}
		seen[m.ID] = true
	}
}

// Route must only ever return a model that is in the catalogue.
func TestRouteReturnsCatalogueModel(t *testing.T) {
	for _, prompt := range []string{"hi", "analyze this", string(make([]byte, 501))} {
		got := Route(prompt)
		found := false
		for _, m := range All() {
			if m == got {
				found = true
			}
		}
		if !found {
			t.Errorf("Route(%.20q) returned %+v, which is not in All()", prompt, got)
		}
	}
}
