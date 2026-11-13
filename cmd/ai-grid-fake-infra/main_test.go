package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// catalogSubscriptionIDs lists the .yaml files in the catalog's subscriptions
// directory as ids, and nothing else.
func TestCatalogSubscriptionIDs(t *testing.T) {
	dir := t.TempDir()
	bare, work := filepath.Join(dir, "catalog.git"), filepath.Join(dir, "work")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--bare", "--initial-branch=main", bare)
	git("init", "--initial-branch=main", work)
	subs := filepath.Join(work, catalogSubscriptionsDir)
	if err := os.MkdirAll(subs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"team-a-abc123.yaml": "id: team-a-abc123\n", "README": "not a subscription\n"} {
		if err := os.WriteFile(filepath.Join(subs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("-C", work, "add", ".")
	git("-C", work, "commit", "-m", "seed")
	git("-C", work, "push", bare, "main")

	ids, err := catalogSubscriptionIDs("file://"+bare, "main")(context.Background())
	if err != nil {
		t.Fatalf("catalogSubscriptionIDs() error = %v", err)
	}
	if want := []string{"team-a-abc123"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("catalogSubscriptionIDs() = %v, want %v", ids, want)
	}
}

// A catalog that can't be opened is an error on that call, not at startup.
func TestCatalogSubscriptionIDsUnreadable(t *testing.T) {
	list := catalogSubscriptionIDs("file://"+filepath.Join(t.TempDir(), "missing.git"), "main")
	if _, err := list(context.Background()); err == nil {
		t.Fatal("catalogSubscriptionIDs() error = nil, want an error for a missing catalog")
	}
}
