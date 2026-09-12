package search

import "testing"

func TestComposeDemoESEndpoint(t *testing.T) {
	t.Setenv("MESHOPS_NETWORK_MODE", "compose-demo")
	if _, err := NewIndex("http://elasticsearch:9200", TaskIndexName, nil); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://elasticsearch:9201", "http://example.com:9200", "http://elasticsearch:9200@evil:9200", "http://elasticsearch:9200/path", "http://elasticsearch:9200?x=y", "https://elasticsearch:9200"} {
		if _, err := NewIndex(endpoint, TaskIndexName, nil); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	t.Setenv("MESHOPS_NETWORK_MODE", "")
	if _, err := NewIndex("http://elasticsearch:9200", TaskIndexName, nil); err == nil {
		t.Fatal("default accepted container hostname")
	}
}
