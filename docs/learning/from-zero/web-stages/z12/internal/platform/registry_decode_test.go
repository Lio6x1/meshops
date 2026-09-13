package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestDecoderStrictFormats(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"json", `{"tenant_id":"demo","sources":[{"source_id":"s","entities":{"raw":"entity"}}]}`, true},
		{"yaml", "tenant_id: demo\nsources:\n - source_id: s\n   entities: {raw: entity}\n", true},
		{"flow_yaml", "{tenant_id: demo, sources: [{source_id: s, entities: {raw: entity}}]}", true},
		{"duplicate", `{"tenant_id":"one","tenant_id":"two"}`, false},
		{"nested_duplicate", `{"sources":[{"entities":{"raw":"one","raw":"two"}}]}`, false},
		{"escaped_duplicate", `{"tenant_id":"one","tenant_\u0069d":"two"}`, false},
		{"unknown", `{"tenant_id":"demo","typo":1}`, false},
		{"case_alias", `{"tenant_id":"demo","TENANT_ID":"other"}`, false},
		{"extra_json", `{"tenant_id":"demo"} {"tenant_id":"other"}`, false},
		{"extra_yaml", "tenant_id: demo\n---\ntenant_id: other\n", false},
		{"deep", strings.Repeat("[", 70) + strings.Repeat("]", 70), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "manifest.json"
			if strings.Contains(tc.name, "yaml") {
				name = "manifest.yaml"
			}
			p := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(p, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			var m manifest
			err = decodeManifest(f, &m)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && (m.Tenant != "demo" || m.Sources[0].Entities["raw"] != "entity") {
				t.Fatal("field mapping changed")
			}
		})
	}
}
