package plugin

import "testing"

func TestWidgetAPIVersionAndPlacements(t *testing.T) {
	for _, test := range []struct {
		name      string
		version   int
		locations []string
		valid     bool
	}{{"legacy", 0, nil, true}, {"v2", 2, []string{"board", "activity"}, true}, {"future", 99, []string{"board"}, true}, {"missing", 2, nil, false}, {"duplicate", 2, []string{"board", "board"}, false}, {"arbitrary", 2, []string{"panel"}, false}, {"negative", -1, nil, false}} {
		t.Run(test.name, func(t *testing.T) {
			manifest := Manifest{Name: "fixture", Version: "1.0.0", UI: UI{APIVersion: test.version, Cards: []Card{{Type: "fixture/card", Locations: test.locations}}}}
			if err := manifest.Validate("dev"); (err == nil) != test.valid {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}
