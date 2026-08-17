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

func pkt(s string) string { return fmt.Sprintf("%04x", len(s)+4) + s }

func advertisement(refLines ...string) string {
	var b strings.Builder
	b.WriteString(pkt("# service=git-upload-pack\n"))
	b.WriteString("0000")
	for _, l := range refLines {
		b.WriteString(pkt(l))
	}
	b.WriteString("0000")
	return b.String()
}

const oid = "2f4a52315b6da2b0a8b23372a75ae5b83db861a3"

func TestParseAdvertisement(t *testing.T) {
	adv := advertisement(
		oid+" HEAD\x00multi_ack thin-pack symref=HEAD:refs/heads/main\n",
		oid+" refs/heads/main\n",
		oid+" refs/tags/jq-1.7.1\n",
		oid+" refs/tags/jq-1.7.1^{}\n",
		oid+" refs/tags/jq-1.8.2\n",
	)
	tags, err := parseAdvertisement(strings.NewReader(adv))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"jq-1.7.1", "jq-1.8.2"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Fatalf("tags = %v, want %v", tags, want)
		}
	}
}

func TestParseAdvertisementBadLength(t *testing.T) {
	if _, err := parseAdvertisement(strings.NewReader("zzzz")); err == nil {
		t.Fatal("want error for malformed pkt length")
	}
}

func TestParseAdvertisementEmpty(t *testing.T) {
	tags, err := parseAdvertisement(strings.NewReader(pkt("# service=git-upload-pack\n") + "00000000"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %v, want none", tags)
	}
}

func TestRespell(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.8.2", "1.8.2"},
		{"25.0.4+7", "25.0.4_7"},
		{"-lead", "_lead"}, // first char must be alnum or _
		{"a b", "a_b"},
	}
	for _, c := range cases {
		if got := respell(c.in); got != c.want {
			t.Errorf("respell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGithubVersions(t *testing.T) {
	adv := advertisement(
		oid+" HEAD\x00symref=HEAD:refs/heads/master\n",
		oid+" refs/tags/go1.25.1\n",
		oid+" refs/tags/go1.26.5\n",
		oid+" refs/tags/go1.27rc1\n",
		oid+" refs/tags/weekly.2012-03-27\n",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/golang/go.git/info/refs" || r.URL.Query().Get("service") != "git-upload-pack" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, adv)
	}))
	defer srv.Close()
	restore := githubBase
	githubBase = srv.URL
	defer func() { githubBase = restore }()

	got, err := githubVersions(context.Background(), srv.Client(),
		&spec.GitHubDiscovery{Repo: "golang/go", Filter: `^go\d+\.\d+\.\d+$`, Prefix: "go"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.25.1", "1.26.5"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("versions = %v, want %v", got, want)
	}
}

func TestGithubVersionsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	restore := githubBase
	githubBase = srv.URL
	defer func() { githubBase = restore }()

	_, err := githubVersions(context.Background(), srv.Client(), &spec.GitHubDiscovery{Repo: "x/y"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want status error", err)
	}
}
