package main

import "testing"

func TestLdflagToken(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "plain",
			value: "YourPM",
			want:  `-X 'sym=YourPM'`,
		},
		{
			name:  "space",
			value: "native client",
			want:  `-X 'sym=native client'`,
		},
		{
			name:  "apostrophe switches delimiter",
			value: "Acme's workspace",
			want:  `-X \"sym=Acme's workspace\"`,
		},
		{
			name:  "double quote is shell escaped",
			value: `Say "hi"`,
			want:  `-X 'sym=Say \"hi\"'`,
		},
		{
			name:  "shell metacharacters are escaped",
			value: "cost $5 `now`",
			want:  "-X 'sym=cost \\$5 \\`now\\`'",
		},
		{
			name:  "both quote kinds normalized",
			value: `Acme's "best"`,
			want:  `-X 'sym=Acme’s \"best\"'`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ldflagToken("sym", tc.value); got != tc.want {
				t.Fatalf("ldflagToken(%q) = %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}
