package money

import "testing"

func TestExactMoney(t *testing.T) {
	for value, want := range map[string]int64{"0": 0, "10": 10_000_000_000, "0.00000002": 20, "1.234567891": 1_234_567_891} {
		got, err := ParseUSD(value)
		if err != nil || got != want {
			t.Fatalf("%s: %d %v", value, got, err)
		}
	}
	for _, value := range []string{"-1", "NaN", "0.0000000001", "100000000000"} {
		if _, err := ParseUSD(value); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	got, err := Cost(3, 2, 20, 1_500)
	if err != nil || got != 3060 {
		t.Fatalf("cost: %d %v", got, err)
	}
	if _, err := Cost(MaxExact, 2, 2, 0); err == nil {
		t.Fatal("overflow accepted")
	}
	got, err = LegacyUSD("0.10000000000000001")
	if err != nil || got != 100_000_001 {
		t.Fatalf("legacy: %d %v", got, err)
	}
}
