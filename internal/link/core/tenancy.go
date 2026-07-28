package core

import (
	"context"
	"errors"
	"net/http"

	"github.com/joakimcarlsson/wasa-api/pkg/proto"
)

// Org is a tenant on the core, as the CLI needs it: enough to name it and to
// list what is inside it.
type Org struct {
	ID   string
	Slug string
	Name string
}

// Project groups repos inside an org. A repo is created into one, and it is
// addressed by id because its slug is only unique within its org.
type Project struct {
	ID    string
	OrgID string
	Slug  string
	Name  string
}

// Repo is a repository on the core. Slug is the `<org>/<name>` form a git
// remote and a token audience address it by; ID is the ULID a link record
// pins, which survives a rename.
type Repo struct {
	ID        string
	ProjectID string
	OrgID     string
	Slug      string
}

// Orgs lists the orgs the caller belongs to, most useful as the way to reach
// the project a repo is created into.
func (c *Client) Orgs(ctx context.Context, accessToken string) ([]Org, error) {
	out, err := c.bearer(accessToken).GetApiV1Orgs(ctx)
	if err != nil {
		return nil, c.asError(err)
	}
	orgs := make([]Org, 0, len(out.Orgs))
	for _, o := range out.Orgs {
		orgs = append(orgs, Org{ID: o.ID, Slug: o.Slug, Name: o.Name})
	}
	return orgs, nil
}

// Projects lists the projects in one org, named by its slug or its id.
func (c *Client) Projects(
	ctx context.Context,
	accessToken, orgRef string,
) ([]Project, error) {
	out, err := c.bearer(accessToken).GetApiV1OrgsByOrgProjects(
		ctx, proto.GetApiV1OrgsByOrgProjectsParams{Org: orgRef},
	)
	if err != nil {
		return nil, c.asError(err)
	}
	projects := make([]Project, 0, len(out.Projects))
	for _, p := range out.Projects {
		projects = append(projects, Project{
			ID: p.ID, OrgID: p.OrgID, Slug: p.Slug, Name: p.Name,
		})
	}
	return projects, nil
}

// FindRepo resolves a repo by its `<org>/<repo>` slug or its ULID, reporting
// false when the core has no such repo.
//
// It asks the discovery endpoint, which is a routing endpoint: what it is for
// is telling a client which nodes host a repo. It is used here because it is
// the only endpoint that accepts a slug — GET /api/v1/repos/{id} is id-only
// and there is no list-repos-in-project — and because its answers are
// unambiguous for this purpose: 404 is "no such repo", 200 carries the repo's
// id. wasa-api#21 adds a slug-addressed repo read; when it lands, move the
// lookup to it and delete this paragraph.
//
// Only the id comes back from discovery, so the returned Slug is the ref that
// was asked for and is meaningful only when that ref was a slug.
func (c *Client) FindRepo(
	ctx context.Context,
	accessToken, ref string,
) (Repo, bool, error) {
	out, err := c.bearer(accessToken).GetApiV1DiscoveryReposByRef(
		ctx, proto.GetApiV1DiscoveryReposByRefParams{Ref: ref},
	)
	if err != nil {
		if statusOf(err) == http.StatusNotFound {
			return Repo{}, false, nil
		}
		return Repo{}, false, c.asError(err)
	}
	return Repo{ID: out.RepoID, Slug: ref}, true, nil
}

// CreateRepo creates a repo named name inside the project projectRef. name is
// the repo's own name, not a slug: the core prefixes it with the owning org,
// and the stored `<org>/<name>` comes back on the result.
func (c *Client) CreateRepo(
	ctx context.Context,
	accessToken, projectRef, name string,
) (Repo, error) {
	out, err := c.bearer(accessToken).PostApiV1ProjectsByProjectRepos(
		ctx, proto.PostApiV1ProjectsByProjectReposParams{
			Project: projectRef,
			Body:    proto.CreateRepoRequest{Slug: name},
		},
	)
	if err != nil {
		return Repo{}, c.asError(err)
	}
	return Repo{
		ID:        out.ID,
		ProjectID: out.ProjectID,
		OrgID:     out.OrgID,
		Slug:      out.Slug,
	}, nil
}

// statusOf returns the HTTP status a generated-client error carries, or zero
// when it is a transport error with no response behind it.
func statusOf(err error) int {
	var apiErr *proto.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}
