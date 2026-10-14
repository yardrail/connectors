package main

import (
	"testing"
)

func TestConfigParamFunc(t *testing.T) {
	t.Parallel()

	fn, ok := tmplFuncs["configParam"].(func(map[string]string, string) string)
	if !ok {
		t.Fatal("configParam not found or wrong type in tmplFuncs")
	}

	tests := []struct {
		name     string
		params   map[string]string
		jsonName string
		want     string
	}{
		{"found", map[string]string{"companyid": "realm_id"}, "companyid", "realm_id"},
		{"not found", map[string]string{"companyid": "realm_id"}, "otherparam", ""},
		{"nil map", nil, "companyid", ""},
		{"empty map", map[string]string{}, "companyid", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := fn(tt.params, tt.jsonName)
			if got != tt.want {
				t.Errorf("configParam(%v, %q) = %q, want %q",
					tt.params, tt.jsonName, got, tt.want)
			}
		})
	}
}
