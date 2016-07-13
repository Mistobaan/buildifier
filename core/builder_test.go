package build

import "testing"

func TestCanCreateRules(t *testing.T) {
	//attrs := map[string][]string{}

	//b.AddRule("go_library", attrs)
	file, err := NewBuildFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range file.Rules("") {
		t.Log(r.Name())
	}
}
