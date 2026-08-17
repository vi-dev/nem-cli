package discover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOCITags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/archives/openssl/tags/list" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"name": "archives/openssl",
			"tags": []string{"v3.4.0", "v3.4.1"},
		})
	}))
	defer srv.Close()

	repo := strings.TrimPrefix(srv.URL, "http://") + "/archives/openssl"
	got, err := ociTags(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "v3.4.0" || got[1] != "v3.4.1" {
		t.Fatalf("tags = %v, want [v3.4.0 v3.4.1]", got)
	}
}
