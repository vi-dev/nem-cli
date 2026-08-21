package discover

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/vi-dev/nem-cli/internal/spec"
)

// parseAdvertisement extracts tag names from a git smart-HTTP ref
// advertisement (pkt-line stream). Peeled "^{}" entries are dropped and
// the "refs/tags/" prefix is stripped.
func parseAdvertisement(r io.Reader) ([]string, error) {
	br := bufio.NewReader(r)
	tags := []string{}
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if errors.Is(err, io.EOF) {
				return tags, nil
			}
			return nil, fmt.Errorf("read pkt length: %w", err)
		}
		n, err := strconv.ParseUint(string(hdr), 16, 32)
		if err != nil {
			return nil, fmt.Errorf("malformed pkt length %q: %w", hdr, err)
		}
		if n == 0 {
			continue // flush packet
		}
		if n < 4 {
			return nil, fmt.Errorf("malformed pkt length %d", n)
		}
		payload := make([]byte, n-4)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, fmt.Errorf("read pkt payload: %w", err)
		}
		line := string(payload)
		if i := strings.IndexByte(line, '\x00'); i >= 0 {
			line = line[:i] // capabilities follow the first ref
		}
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "#") {
			continue // service announcement
		}
		_, ref, ok := strings.Cut(line, " ")
		if !ok || strings.HasSuffix(ref, "^{}") {
			continue
		}
		if name, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
			tags = append(tags, name)
		}
	}
}

// githubBase and gitlabBase are the hosts serving git smart-HTTP; vars so
// tests can point them at a local server.
var (
	githubBase = "https://github.com"
	gitlabBase = "https://gitlab.com"
)

// githubVersions lists g.Repo's tags and derives versions.
func githubVersions(ctx context.Context, client *http.Client, g *spec.GitHubDiscovery) ([]string, error) {
	return gitVersions(ctx, client, githubBase+"/"+g.Repo+".git", g.Filter, g.Prefix, g.Suffix)
}

// gitlabVersions lists g.Repo's tags and derives versions.
func gitlabVersions(ctx context.Context, client *http.Client, g *spec.GitLabDiscovery) ([]string, error) {
	return gitVersions(ctx, client, gitlabBase+"/"+g.Repo+".git", g.Filter, g.Prefix, g.Suffix)
}

// gitVersions lists the tags of the git repository cloned from repoURL
// (smart HTTP) and derives versions: filter regex on raw tag names, strip
// the prefix and suffix, respell to the OCI tag grammar.
func gitVersions(ctx context.Context, client *http.Client, repoURL, filter, prefix, suffix string) ([]string, error) {
	var re *regexp.Regexp
	if filter != "" {
		var err error
		if re, err = regexp.Compile(filter); err != nil {
			return nil, fmt.Errorf("discovery filter: %w", err)
		}
	}
	url := strings.TrimSuffix(repoURL, "/") + "/info/refs?service=git-upload-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", repoURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list tags for %s: unexpected status %s", repoURL, resp.Status)
	}
	tags, err := parseAdvertisement(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", repoURL, err)
	}
	var out []string
	for _, t := range tags {
		if re != nil && !re.MatchString(t) {
			continue
		}
		out = append(out, respell(strings.TrimSuffix(strings.TrimPrefix(t, prefix), suffix)))
	}
	return out, nil
}

// respell maps characters outside the OCI tag grammar
// ([a-zA-Z0-9_][a-zA-Z0-9._-]*) to '_', implementing the catalog's
// version respelling convention (e.g. "25.0.4+7" → "25.0.4_7").
func respell(v string) string {
	out := []byte(v)
	for i, c := range out {
		ok := c == '_' || isAlnum(c) || (i > 0 && (c == '.' || c == '-'))
		if !ok {
			out[i] = '_'
		}
	}
	return string(out)
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c)
}
