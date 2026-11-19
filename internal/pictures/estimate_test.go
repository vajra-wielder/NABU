package pictures

import "testing"

// The measured totals in the README come from these surveys.
func TestEstimateMatchesSurvey(t *testing.T) {
	lead, all := Estimate("enwiki", 7_266_259, false)
	if gb := lead.Bytes / 1e9; gb < 50 || gb > 60 {
		t.Errorf("English main pictures %d GB", gb)
	}
	if gb := all.Bytes / 1e9; gb < 110 || gb > 130 {
		t.Errorf("English all pictures %d GB", gb)
	}
	alone, _ := Estimate("frwiki", 2_790_246, false)
	after, _ := Estimate("frwiki", 2_790_246, true)
	if after.Bytes >= alone.Bytes || after.Bytes < alone.Bytes/3 {
		t.Errorf("French after English %d of %d", after.Bytes, alone.Bytes)
	}
	if e, _ := Estimate("enwiki", 1000, true); e.Pictures != 453 {
		t.Errorf("English is never reduced for itself: %d", e.Pictures)
	}
}

func TestTiersCoverEveryArticleOnce(t *testing.T) {
	for _, n := range []int{0, 1, 5999, 6000, 19999, 20000, 1 << 20} {
		c := 0
		for p := range Tiers {
			if inTier(n, p) {
				c++
			}
		}
		if c != 1 {
			t.Errorf("an article of %d bytes is in %d passes", n, c)
		}
	}
	if !inTier(50000, 0) || inTier(50000, 1) || !inTier(100, len(Tiers)-1) {
		t.Error("longest articles must come first")
	}
}
