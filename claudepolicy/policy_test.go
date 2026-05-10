package claudepolicy

import "testing"

func TestNormalizeEffort(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		def     string
		want    string
		wantErr bool
	}{
		{name: "empty empty default", want: ""},
		{name: "empty defaults to high", def: "high", want: "high"},
		{name: "low", input: "low", want: "low"},
		{name: "medium", input: "medium", want: "medium"},
		{name: "high", input: "high", want: "high"},
		{name: "xhigh", input: "xhigh", want: "xhigh"},
		{name: "max", input: "max", want: "max"},
		{name: "very-high hyphen", input: "very-high", want: "xhigh"},
		{name: "very_high underscore", input: "very_high", want: "xhigh"},
		{name: "invalid", input: "turbo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEffort(tt.input, tt.def)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeEffort: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}
