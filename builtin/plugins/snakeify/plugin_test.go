package snakeify

import "testing"

func Test_camelToSnake(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"simpleTest", "simple_test"},
		{"camelCaseString", "camel_case_string"},
		{"Test", "test"},
		{"already_snake", "already_snake"},
		{"HTTPServerResponse", "http_server_response"},
		{"userID", "user_id"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := camelToSnake(tt.input)
			if got != tt.expected {
				t.Errorf("CamelToSnake(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
