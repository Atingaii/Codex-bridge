package main

import "testing"

func TestIsQuotedEmptyPassword(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "double quoted empty", value: `""`, want: true},
		{name: "single quoted empty", value: `''`, want: true},
		{name: "space padded double quoted empty", value: `  ""  `, want: true},
		{name: "real password", value: `change-me-123`, want: false},
		{name: "empty password", value: ``, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQuotedEmptyPassword(tt.value); got != tt.want {
				t.Fatalf("isQuotedEmptyPassword(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
