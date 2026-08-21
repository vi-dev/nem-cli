package discover

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vi-dev/nem-cli/internal/spec"
)

func TestListRequiresDiscovery(t *testing.T) {
	_, err := List(context.Background(), &spec.Package{Name: "jq"})
	if err == nil || !strings.Contains(err.Error(), "versionDiscovery") {
		t.Fatalf("err = %v, want missing-versionDiscovery error", err)
	}
}

func TestListDispatchesGitAndGitLab(t *testing.T) {
	adv := advertisement(
		oid+" refs/tags/v2.0.0\n",
		oid+" refs/tags/v2.1.0\n",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, adv)
	}))
	defer srv.Close()
	restore := gitlabBase
	gitlabBase = srv.URL
	defer func() { gitlabBase = restore }()

	discoveries := []*spec.Discovery{
		{Git: &spec.GitDiscovery{URL: srv.URL + "/a/b.git", Prefix: "v"}},
		{GitLab: &spec.GitLabDiscovery{Repo: "a/b", Prefix: "v"}},
	}
	for _, d := range discoveries {
		got, err := List(context.Background(), &spec.Package{Name: "x", VersionDiscovery: d})
		if err != nil {
			t.Fatalf("List(%+v): %v", d, err)
		}
		if len(got) != 2 || got[0] != "2.0.0" || got[1] != "2.1.0" {
			t.Fatalf("List(%+v) = %v, want [2.0.0 2.1.0]", d, got)
		}
	}
}

func TestLatestPicksMax(t *testing.T) {
	adv := advertisement(
		oid+" refs/tags/jq-1.7.1\n",
		oid+" refs/tags/jq-1.8.2\n",
		oid+" refs/tags/jq-1.8.10\n",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, adv)
	}))
	defer srv.Close()
	restore := githubBase
	githubBase = srv.URL
	defer func() { githubBase = restore }()

	pkg := &spec.Package{Name: "jq", VersionDiscovery: &spec.Discovery{
		GitHub: &spec.GitHubDiscovery{Repo: "jqlang/jq", Prefix: "jq-"},
	}}
	got, err := Latest(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.8.10" {
		t.Fatalf("latest = %q, want 1.8.10", got)
	}
}
