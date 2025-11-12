package camelify

import "testing"

func Test_snakeToCamel(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"simple_test", "simpleTest"},
		{"snake_case_string", "snakeCaseString"},
		{"test", "test"},
		{"alreadyCamel", "alreadyCamel"},
		{"http_server_response", "httpServerResponse"},
		{"user_id", "userId"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := snakeToCamel(tt.input)
			if got != tt.expected {
				t.Errorf("SnakeToCamel(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
