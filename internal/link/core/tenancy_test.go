package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindRepoResolvesASlug(t *testing.T) {
	var seen string
	c := tenancyServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		if got := r.Header.Get("Authorization"); got != "Bearer jwt" {
			t.Errorf("Authorization = %q, want the bearer token", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"repo_id": "01J000000000000000000000AB",
			"nodes":   []any{},
		})
	})

	repo, ok, err := c.FindRepo(context.Background(), "jwt", "acme/widgets")
	if err != nil || !ok {
		t.Fatalf("FindRepo = (%+v, %v, %v)", repo, ok, err)
	}
	if repo.ID != "01J000000000000000000000AB" {
		t.Errorf("repo id = %q", repo.ID)
	}
	if repo.Slug != "acme/widgets" {
		t.Errorf("repo slug = %q, want the ref asked for", repo.Slug)
	}
	if want := "/api/v1/discovery/repos/acme/widgets"; seen != want {
		t.Errorf("path = %q, want %q", seen, want)
	}
}

// TestFindRepoReportsAbsenceRatherThanFailing is what makes the lookup usable
// as an existence check: a 404 is an answer, not an error.
func TestFindRepoReportsAbsenceRatherThanFailing(t *testing.T) {
	c := tenancyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status": 404, "detail": "no such repo",
		})
	})

	repo, ok, err := c.FindRepo(context.Background(), "jwt", "acme/widgets")
	if err != nil {
		t.Fatalf("FindRepo on a 404 = %v, want no error", err)
	}
	if ok {
		t.Errorf("FindRepo reported %+v present", repo)
	}
}

// TestFindRepoKeepsADenialADenial separates "not there" from "not yours": a
// 403 means the repo exists and creating a second one would be wrong.
func TestFindRepoKeepsADenialADenial(t *testing.T) {
	c := tenancyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"status": 403, "detail": "no access",
		})
	})

	_, ok, err := c.FindRepo(context.Background(), "jwt", "acme/widgets")
	if ok || !errors.Is(err, ErrForbidden) {
		t.Fatalf("FindRepo on a 403 = (%v, %v), want ErrForbidden", ok, err)
	}
}

func TestCreateRepoSendsTheBareName(t *testing.T) {
	var body map[string]any
	var path string
	c := tenancyServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":         "01J000000000000000000000AB",
			"project_id": "01J0000000000000000000PROJ",
			"org_id":     "01J00000000000000000000ORG",
			"slug":       "acme/widgets",
			"created_at": "2026-01-01T00:00:00Z",
		})
	})

	repo, err := c.CreateRepo(
		context.Background(), "jwt", "01J0000000000000000000PROJ", "widgets",
	)
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if body["slug"] != "widgets" {
		t.Errorf("sent slug = %v, want the bare name", body["slug"])
	}
	if want := "/api/v1/projects/01J0000000000000000000PROJ/repos"; path !=
		want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if repo.Slug != "acme/widgets" {
		t.Errorf("repo slug = %q, want the core's own", repo.Slug)
	}
}

func TestOrgsAndProjects(t *testing.T) {
	c := tenancyServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs":
			writeJSON(w, http.StatusOK, map[string]any{
				"orgs": []any{map[string]any{
					"id": "01J00000000000000000000ORG", "slug": "acme",
					"name": "Acme", "created_at": "2026-01-01T00:00:00Z",
				}},
			})
		case "/api/v1/orgs/01J00000000000000000000ORG/projects":
			writeJSON(w, http.StatusOK, map[string]any{
				"projects": []any{map[string]any{
					"id": "01J0000000000000000000PROJ", "slug": "default",
					"name": "Default", "org_id": "01J00000000000000000000ORG",
					"created_at": "2026-01-01T00:00:00Z",
				}},
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	orgs, err := c.Orgs(context.Background(), "jwt")
	if err != nil || len(orgs) != 1 || orgs[0].Slug != "acme" {
		t.Fatalf("Orgs = (%+v, %v)", orgs, err)
	}
	projects, err := c.Projects(context.Background(), "jwt", orgs[0].ID)
	if err != nil || len(projects) != 1 || projects[0].Slug != "default" {
		t.Fatalf("Projects = (%+v, %v)", projects, err)
	}
	if projects[0].OrgID != orgs[0].ID {
		t.Errorf("project org = %q, want %q", projects[0].OrgID, orgs[0].ID)
	}
}

func tenancyServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}
