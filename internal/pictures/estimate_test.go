package pictures

import "testing"

// The measured totals in the README come from these surveys.
func TestEstimateMatchesSurvey(t *testing.T) {
	lead, all := Estimate("enwiki", 7_266_259, false)
	if gb := lead.Bytes / 1e9; gb < 75 || gb > 90 {
		t.Errorf("English main pictures %d GB", gb)
	}
	if gb := all.Bytes / 1e9; gb < 165 || gb > 195 {
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
