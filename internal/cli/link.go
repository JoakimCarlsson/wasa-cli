package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/link/auth"
	"github.com/joakimcarlsson/wasa-cli/internal/link/core"
	"github.com/joakimcarlsson/wasa-cli/internal/link/gitremote"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
	"github.com/joakimcarlsson/wasa-cli/internal/repo"
)

func init() {
	commands = append(commands,
		&Command{
			Name:    "link",
			Summary: "sync this repository's record through a wasa-api core",
			Run:     runLink,
		},
		&Command{
			Name:    "unlink",
			Summary: "stop syncing this repository through a core",
			Run:     runUnlink,
		},
	)
}

const linkUsage = "usage: wasa link " +
	"[--core <url>] [--slug <owner>/<repo>] [--project <id>] " +
	"[--checkpoints <origin|wasa>]"

const linkHelp = `usage: wasa link [--core <url>] [--slug <owner>/<repo>] [--project <id>]
                 [--checkpoints <origin|wasa>]

Connect this repository to the wasa-api core you are logged in to, so its
refs/wasa record syncs through the control plane instead of staying local.
wasa resolves the repo on the core — creating it when it is not there yet —
records the association on the workspace, and configures a "wasa" git remote
with that core pinned on it.

The core is pinned on the remote rather than read from whichever login context
happens to be current, so a workspace linked to one core never follows a
context switch to another.

Linking is opt-in and reversible: until it is run, and again after
"wasa unlink", this repository behaves exactly as it does with no control
plane at all. Running it twice is not an error — the second run re-resolves
the repo and rewrites the record.

Checkpoints go to origin by default whether or not this repository is linked:
the core reads refs/wasa/* from the git host, so a checkpoint rides along with
the remote you already have credentials for. Pass --checkpoints wasa to send
them over the control-plane remote instead, which is the choice to make when
transcripts should not live in the code repository. Omitting the flag leaves
whichever destination is already recorded untouched.

Flags:
  --core <url>              the core to link to (default: the current context's)
  --slug <owner>/<repo>     the repo on the core (default: derived from origin)
  --project <id>            the project to create the repo in, as a ULID
  --checkpoints <dest>      where checkpoints sync: origin (default) or wasa
`

func runLink(args []string) error {
	fs := newFlagSet("wasa link")
	coreURL := fs.String("core", "", "the core to link to")
	slug := fs.String("slug", "", "the repo on the core")
	project := fs.String("project", "", "project to create the repo in")
	checkpoints := fs.String(
		"checkpoints", "", "where checkpoints sync: origin or wasa",
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, linkHelp)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(linkUsage)
	}
	req := linkRequest{core: *coreURL, slug: *slug, project: *project}
	if *checkpoints != "" {
		dest, err := registry.ParseCheckpointSync(*checkpoints)
		if err != nil {
			return err
		}
		req.checkpoints = &dest
	}
	return link(context.Background(), req, os.Stdout)
}

// linkRequest is what the user asked for, before any of it is resolved. A nil
// checkpoints means the flag was not passed, which leaves whichever destination
// the workspace already records alone — re-linking never silently moves a
// deliberate privacy choice back to origin.
type linkRequest struct {
	core        string
	slug        string
	project     string
	checkpoints *string
}

// link resolves the repo this workspace should sync through, records it and
// configures the remote that carries it. Every step is idempotent, so a
// repeated link re-resolves and rewrites rather than failing.
func link(ctx context.Context, req linkRequest, out io.Writer) error {
	repoPath, remoteURL, err := currentRepo()
	if err != nil {
		return err
	}

	store, err := auth.Default()
	if err != nil {
		return err
	}
	current, access, err := store.Access(ctx, time.Now())
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return errNotLoggedIn
	}
	if err != nil {
		return err
	}
	coreURL, err := linkCore(req.core, current.CoreURL)
	if err != nil {
		return err
	}
	client, err := core.New(coreURL)
	if err != nil {
		return err
	}

	reg, err := registry.Open(wasaHome())
	if err != nil {
		return err
	}
	ws, _ := registerRepo(reg, repoPath, remoteURL)

	slug := strings.Trim(req.slug, "/")
	if slug == "" {
		slug = repo.Slug(remoteURL)
	}
	if slug == "" {
		return errors.New(
			"this repository has no origin remote to derive a name from — " +
				"pass --slug <owner>/<repo>",
		)
	}
	name, err := repoName(slug)
	if err != nil {
		return err
	}

	found, err := resolveLinkRepo(ctx, client, access.Value, resolution{
		slug:    slug,
		name:    name,
		project: req.project,
		known:   linkedSlug(ws, coreURL),
	})
	if err != nil {
		return err
	}

	ws.Link = &registry.Link{
		CoreURL: coreURL, RepoID: found.ID, Slug: found.Slug,
	}
	if req.checkpoints != nil {
		ws.CheckpointSync = *req.checkpoints
	}
	if err := reg.Save(); err != nil {
		return err
	}
	if err := gitremote.Configure(
		repoPath, gitremote.RemoteName, found.Slug, coreURL,
	); err != nil {
		return err
	}

	fmt.Fprintf(
		out, "linked %s (%s) on %s\n", found.Slug, found.ID, coreURL,
	)
	fmt.Fprintf(
		out, "checkpoints sync to %s\n",
		registry.CheckpointSyncName(ws.CheckpointSync),
	)
	return nil
}

// linkCore picks the core to link to. A --core naming something other than
// the current context's core is refused rather than silently linking with a
// credential that core never issued.
func linkCore(requested, currentCore string) (string, error) {
	if requested == "" {
		return currentCore, nil
	}
	normalized, err := core.NormalizeURL(requested)
	if err != nil {
		return "", err
	}
	if normalized != currentCore {
		return "", fmt.Errorf(
			"not logged in to %s — run `%s login --core %s`",
			normalized, programName, normalized,
		)
	}
	return normalized, nil
}

// linkedSlug returns the slug a workspace is already linked under on coreURL,
// so a repeated link finds the repo it created last time even when the core
// stored it under a slug that differs from the one derived here.
func linkedSlug(ws *registry.Workspace, coreURL string) string {
	if ws.Link == nil || ws.Link.CoreURL != coreURL {
		return ""
	}
	return ws.Link.Slug
}

// resolution is the repo lookup a link performs, and the fallback that
// creates one when none of it resolves.
type resolution struct {
	// slug is the `<owner>/<repo>` asked for or derived from origin.
	slug string
	// name is the repo's own name, which is what a create is given.
	name string
	// project is an explicit --project, or empty to use the default.
	project string
	// known is the slug a previous link recorded, if any.
	known string
}

// resolveRepo finds the repo on the core or creates it, which is the whole of
// what a link needs from the control plane.
//
// The candidate slugs are tried in order of confidence: what a previous link
// recorded, then what origin says, then the `<org>/<name>` the core would
// store this repo under — the last of which is what makes a second link find
// the repo the first one created, since the core owns the org half of a slug
// and it need not match the git host's.
func resolveLinkRepo(
	ctx context.Context,
	client *core.Client,
	accessToken string,
	r resolution,
) (core.Repo, error) {
	project, org, err := targetProject(ctx, client, accessToken, r.project)
	if err != nil {
		return core.Repo{}, err
	}

	var candidates []string
	for _, slug := range []string{r.known, r.slug, org.Slug + "/" + r.name} {
		if org.Slug == "" && slug == "/"+r.name {
			continue
		}
		if slug != "" && !slices.Contains(candidates, slug) {
			candidates = append(candidates, slug)
		}
	}
	for _, slug := range candidates {
		found, ok, err := client.FindRepo(ctx, accessToken, slug)
		if err != nil {
			return core.Repo{}, err
		}
		if ok {
			return found, nil
		}
	}

	return client.CreateRepo(ctx, accessToken, project.ID, r.name)
}

// targetProject resolves the project a repo would be created into: the one
// --project names, or the caller's default. The org comes back with it
// because its slug is the half of a repo slug the core owns.
func targetProject(
	ctx context.Context,
	client *core.Client,
	accessToken, requested string,
) (core.Project, core.Org, error) {
	orgs, err := client.Orgs(ctx, accessToken)
	if err != nil {
		return core.Project{}, core.Org{}, err
	}
	for _, org := range orgs {
		projects, err := client.Projects(ctx, accessToken, org.ID)
		if err != nil {
			return core.Project{}, core.Org{}, err
		}
		for _, p := range projects {
			if requested == "" || p.ID == requested {
				return p, org, nil
			}
		}
	}
	if requested != "" {
		return core.Project{}, core.Org{}, fmt.Errorf(
			"no project %s in any org you belong to", requested,
		)
	}
	return core.Project{}, core.Org{}, errors.New(
		"you have no project to create this repo in — " +
			"pass --project <id> once one exists",
	)
}

// repoName returns the repo's own name from a slug, rejecting anything that is
// not the two-segment form the core addresses a repo by.
func repoName(slug string) (string, error) {
	owner, name, ok := strings.Cut(strings.Trim(slug, "/"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf(
			"%q is not a repo slug — use <owner>/<repo>", slug,
		)
	}
	return strings.TrimSuffix(name, ".git"), nil
}

const unlinkUsage = "usage: wasa unlink"

// runUnlink drops the link record and the remote that carried it, which
// returns the workspace to syncing locally exactly as an unlinked one does.
// A workspace that had selected the control plane for its checkpoints goes back
// to origin with it, since the remote that carried them is being removed.
// Nothing on the core is touched: the repo and everything pushed to it stay.
func runUnlink(args []string) error {
	fs := newFlagSet("wasa unlink")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(unlinkUsage)
	}

	repoPath, remoteURL, err := currentRepo()
	if err != nil {
		return err
	}
	reg, err := registry.Open(wasaHome())
	if err != nil {
		return err
	}
	ws, ok := reg.Workspace(registry.WorkspaceID(repoPath, remoteURL))
	linked := ok && ws.Link != nil
	if linked {
		ws.Link = nil
		ws.CheckpointSync = registry.CheckpointSyncOrigin
		if err := reg.Save(); err != nil {
			return err
		}
	}
	removed := gitremote.Unconfigure(repoPath, gitremote.RemoteName)

	if !linked && !removed {
		fmt.Fprintln(os.Stdout, "not linked")
		return nil
	}
	fmt.Fprintln(os.Stdout, "unlinked — the record stays local from here")
	return nil
}
