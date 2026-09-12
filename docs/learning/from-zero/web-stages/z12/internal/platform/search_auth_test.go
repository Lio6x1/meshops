package platform

import "testing"

func TestSearchMethodPermissions(t *testing.T) {
	for _, role := range []string{"operator", "admin", "source", "executor", "task_service", "dispatcher_service", ""} {
		want := role == "operator" || role == "admin"
		if permitted(role, "/meshops.search.v1.SearchService/SearchTasks") != want {
			t.Errorf("search permission for %s", role)
		}
		if permitted(role, "/meshops.search.v1.SearchService/Rebuild") {
			t.Errorf("search must not expose administrative writes to %s", role)
		}
	}
}
