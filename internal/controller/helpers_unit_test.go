/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import "testing"

// TestEqualProperties pins the nil-vs-empty behaviour. A nil map and a
// non-nil empty map must compare equal: Polaris commonly returns `{}` while
// an unset CRD map is nil, and treating that as drift would cause an endless
// PATCH + requeue hot-loop against the Polaris API.
func TestEqualProperties(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, map[string]string{}, true},
		{"empty vs nil", map[string]string{}, nil, true},
		{"both empty", map[string]string{}, map[string]string{}, true},
		{"equal single", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"equal multi", map[string]string{"a": "1", "b": "2"}, map[string]string{"b": "2", "a": "1"}, true},
		{"value differs", map[string]string{"k": "v"}, map[string]string{"k": "w"}, false},
		{"key added", map[string]string{"k": "v"}, map[string]string{"k": "v", "x": "y"}, false},
		{"key removed", map[string]string{"k": "v", "x": "y"}, map[string]string{"k": "v"}, false},
		{"nil vs single", nil, map[string]string{"k": "v"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := equalProperties(tc.a, tc.b); got != tc.want {
				t.Errorf("equalProperties(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
