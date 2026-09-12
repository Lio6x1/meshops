package tasks

import "testing"

func TestSourceExpansionOnlyAddsEntityMappings(t *testing.T) {
	old := `{"ID":"source","TenantID":"tenant","Generation":1,"Entities":{"raw-1":"entity-1"}}`
	for _, tc := range []struct {
		name, next string
		allowed    bool
	}{
		{"add", `{"ID":"source","TenantID":"tenant","Generation":1,"Entities":{"raw-1":"entity-1","raw-2":"entity-2"}}`, true},
		{"unchanged", old, true},
		{"remove", `{"ID":"source","TenantID":"tenant","Generation":1,"Entities":{}}`, false},
		{"reassign", `{"ID":"source","TenantID":"tenant","Generation":1,"Entities":{"raw-1":"entity-2"}}`, false},
		{"generation", `{"ID":"source","TenantID":"tenant","Generation":2,"Entities":{"raw-1":"entity-1"}}`, false},
		{"unknown field", `{"ID":"source","TenantID":"tenant","Generation":1,"extra":true,"Entities":{"raw-1":"entity-1"}}`, false},
		{"malformed", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := additiveSourceEntities(old, tc.next); got != tc.allowed {
				t.Fatalf("allowed=%v want=%v", got, tc.allowed)
			}
		})
	}
}
