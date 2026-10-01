package main

import "testing"

func TestEmbeddedExtraParses(t *testing.T) {
	ports, err := parseExtra(embeddedExtra, map[string]*Recipe{})
	if err != nil || len(ports) == 0 {
		t.Fatalf("extra catalog: %v, %d ports", err, len(ports))
	}
	for _, p := range ports {
		if p.Repo == "" {
			t.Errorf("%s: upstream is not a GitHub repo", p.Name)
		}
	}
}

func TestMergeExtra(t *testing.T) {
	recipes := map[string]*Recipe{}
	main, err := parseCatalog([]byte(`{"ports":[
		{"name":"Alpha","upstream":"https://github.com/a/alpha"},
		{"name":"Zulu","upstream":"https://github.com/z/zulu"}]}`), recipes)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := parseExtra([]byte(`{"ports":[
		{"name":"Mid","upstream":"https://github.com/m/mid/releases","recipe":{"asset":"miyoo","keep":["data/*"]}},
		{"name":"Alpha copy","upstream":"https://github.com/A/Alpha"},
		{"name":"zulu"}]}`), recipes)
	if err != nil {
		t.Fatal(err)
	}
	got := mergeExtra(main, extra)
	if len(got) != 3 {
		t.Fatalf("want 3 ports after merge, got %d", len(got))
	}
	if got[1].Name != "Mid" || got[1].Index != 1 {
		t.Fatalf("extra port not sorted in: %+v", got[1])
	}
	if got[1].Repo != "m/mid" || got[1].Recipe == nil || got[1].Recipe.Asset != "miyoo" || len(got[1].Recipe.Keep) != 1 {
		t.Fatalf("inline recipe not applied: %+v", got[1])
	}
}

func TestMergeExtraKeepsInlineRecipeForDuplicates(t *testing.T) {
	recipes := map[string]*Recipe{}
	main, _ := parseCatalog([]byte(`{"ports":[
		{"name":"Panel Attack","notes":"from main","upstream":"https://github.com/snyderman3000/panelattack-miyoo/releases"},
		{"name":"Other","upstream":"https://github.com/o/other"}]}`), recipes)
	extra, _ := parseExtra([]byte(`{"ports":[
		{"name":"Panel Attack (my list)","notes":"from extra","upstream":"https://github.com/SnyderMan3000/PanelAttack-Miyoo/releases",
		 "recipe":{"asset":"miyoo","keep":["data/*"]}},
		{"name":"other","upstream":"https://example.com/not-github"}]}`), recipes)
	got := mergeExtra(main, extra)
	if len(got) != 2 {
		t.Fatalf("duplicates were added: %d ports", len(got))
	}
	var pa *Port
	for _, p := range got {
		if p.Repo == "snyderman3000/panelattack-miyoo" {
			pa = p
		}
	}
	if pa == nil || pa.Notes != "from main" {
		t.Fatalf("main catalog entry should be the one shown: %+v", pa)
	}
	if pa.Recipe == nil || pa.Recipe.Asset != "miyoo" || len(pa.Recipe.Keep) != 1 {
		t.Fatalf("inline recipe from extra.json was lost: %+v", pa.Recipe)
	}
}
