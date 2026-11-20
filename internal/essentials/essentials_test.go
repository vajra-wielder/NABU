package essentials

import "testing"

func TestGroups(t *testing.T) {
	g := Groups()
	if len(g) != 10 || Count() < 250 {
		t.Fatalf("%d groups, %d articles", len(g), Count())
	}
	seen := map[string]bool{}
	for _, x := range g {
		if len(x.Titles) < 10 {
			t.Errorf("%s: only %d", x.Name, len(x.Titles))
		}
		for _, title := range x.Titles {
			if seen[title] {
				t.Errorf("%s twice", title)
			}
			seen[title] = true
		}
	}
	if g[0].Name != "First aid and emergencies" || g[0].Titles[0] != "First aid" {
		t.Fatalf("%+v", g[0])
	}
}
