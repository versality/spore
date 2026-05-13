package gh

import "testing"

func TestParseViewJSON(t *testing.T) {
	in := []byte(`{"mergeable":"MERGEABLE","number":9,"state":"OPEN","statusCheckRollup":[{"name":"Validate","conclusion":"FAILURE","status":"COMPLETED","detailsUrl":"https://example/run/1","workflowName":"CI"}]}`)
	pr, found, err := ParseViewJSON(in)
	if err != nil {
		t.Fatalf("ParseViewJSON: %v", err)
	}
	if !found {
		t.Fatal("found = false; want true")
	}
	if pr.Number != 9 || pr.State != "OPEN" || pr.Mergeable != "MERGEABLE" {
		t.Errorf("pr fields wrong: %+v", pr)
	}
	if len(pr.Checks) != 1 {
		t.Fatalf("len(Checks) = %d, want 1", len(pr.Checks))
	}
	c := pr.Checks[0]
	if c.Name != "CI / Validate" {
		t.Errorf("Name = %q, want CI / Validate", c.Name)
	}
	if c.Conclusion != "FAILURE" || c.Status != "COMPLETED" {
		t.Errorf("conclusion/status wrong: %+v", c)
	}
	if c.URL != "https://example/run/1" {
		t.Errorf("URL = %q", c.URL)
	}
}

func TestParseViewJSONNoWorkflowPrefix(t *testing.T) {
	// When workflowName matches name, no prefix is added.
	in := []byte(`{"number":1,"state":"OPEN","mergeable":"MERGEABLE","statusCheckRollup":[{"name":"Validate","workflowName":"Validate","conclusion":"SUCCESS","status":"COMPLETED"}]}`)
	pr, _, err := ParseViewJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Checks[0].Name != "Validate" {
		t.Errorf("Name = %q, want Validate", pr.Checks[0].Name)
	}
}

func TestParseViewJSONMalformed(t *testing.T) {
	if _, _, err := ParseViewJSON([]byte("not json")); err == nil {
		t.Fatal("want error on malformed json")
	}
}

func TestParseCreateOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
		err  bool
	}{
		{"single URL line", "https://github.com/owner/repo/pull/42\n", 42, false},
		{"with preamble", "Creating pull request for wt/x into main in owner/repo\n\nhttps://github.com/owner/repo/pull/7\n", 7, false},
		{"trailing whitespace", "  https://github.com/o/r/pull/100  \n", 100, false},
		{"no number", "https://github.com/owner/repo/pull/\n", 0, true},
		{"empty", "", 0, true},
		{"no slash", "garbage\n", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := parseCreateOutput([]byte(tc.in))
			if tc.err {
				if err == nil {
					t.Fatalf("want error, got n=%d", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tc.want {
				t.Errorf("n = %d, want %d", n, tc.want)
			}
		})
	}
}
