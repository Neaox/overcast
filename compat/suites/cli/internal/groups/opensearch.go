package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// OpenSearch returns the Amazon OpenSearch Service group.
//
// OpenSearch is REST-JSON under /2021-01-01, with several distinct collection
// URIs — GET /2021-01-01/domain for ListDomainNames, POST
// /2021-01-01/opensearch/domain-info for DescribeDomains, GET
// /2021-01-01/tags/ for ListTags — and each is bound somewhere an author would
// not guess from the operation name. That is the shape of bug issue #963
// caught in Backup, and it is why every collection operation below is
// exercised through the AWS CLI rather than a hand-built request: the CLI
// takes the URI from the pinned AWS model, so a route bound at a spelling no
// client sends fails here and nowhere else.
//
// Error statuses are asserted explicitly rather than by error code alone,
// because OpenSearch does not use the statuses an author would assume:
// ResourceNotFoundException and ResourceAlreadyExistsException are both
// **HTTP 409** in the model (opensearch-2021-01-01), not 404 or 400, while
// ValidationException is 400. awscli.RunStatus reads the status out of the
// CLI's own --debug wire log, which is the only place a CLI-driven test can
// see it: the CLI prints a modeled error's code and never its status.
func OpenSearch() ServiceGroup {
	g := &opensearchGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// opensearch-domains
			"opensearch-domains:CreateDomain":                     g.CreateDomain,
			"opensearch-domains:DescribeDomain":                   g.DescribeDomain,
			"opensearch-domains:DescribeDomains":                  g.DescribeDomains,
			"opensearch-domains:ListDomainNames":                  g.ListDomainNames,
			"opensearch-domains:ListDomainNamesByEngineType":      g.ListDomainNamesByEngineType,
			"opensearch-domains:ListDomainNamesInvalidEngineType": g.ListDomainNamesInvalidEngineType,
			"opensearch-domains:DescribeDomainNotFound":           g.DescribeDomainNotFound,
			"opensearch-domains:CreateDomainAlreadyExists":        g.CreateDomainAlreadyExists,
			"opensearch-domains:DeleteDomain":                     g.DeleteDomain,
			"opensearch-domains:DeleteDomainNotFound":             g.DeleteDomainNotFound,
			// opensearch-tags
			"opensearch-tags:AddTags":                 g.AddTags,
			"opensearch-tags:ListTags":                g.ListTags,
			"opensearch-tags:RemoveTags":              g.RemoveTags,
			"opensearch-tags:CreateDomainWithTagList": g.CreateDomainWithTagList,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"opensearch-domains": g.setupDomains,
			"opensearch-tags":    g.setupTags,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"opensearch-domains": g.teardownDomains,
			"opensearch-tags":    g.teardownTags,
		},
	}
}

type opensearchGroup struct{}

// Each group owns its own domains, named exactly and torn down by name. A
// domain name is 3–28 characters of [a-z][a-z0-9-]*, which "{runID}-{tag}"
// satisfies for every run ID the runner mints ("oc-a3f9b12c-cl").
var (
	openSearchDomainNamer   = harness.NewNamer("osd")
	openSearchDeleteNamer   = harness.NewNamer("osdel")
	openSearchTagNamer      = harness.NewNamer("ost")
	openSearchInlineTagName = harness.NewNamer("ostc")
)

// engineVersion is pinned rather than defaulted so DescribeDomain has
// something the create request actually asked for to assert against.
const openSearchEngineVersion = "OpenSearch_2.11"

// ─── shared helpers ───────────────────────────────────────────────────────────

// createDomain creates a domain and returns its DomainStatus.
func (g *opensearchGroup) createDomain(t *harness.TestContext, name string, extra ...string) (map[string]any, error) {
	args := append([]string{
		"opensearch", "create-domain",
		"--domain-name", name,
		"--engine-version", openSearchEngineVersion,
	}, extra...)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, err
	}
	status, _ := out["DomainStatus"].(map[string]any)
	if status == nil {
		return nil, fmt.Errorf("opensearch create-domain %q: no DomainStatus in response: %v", name, out)
	}
	return status, nil
}

// deleteDomainQuietly is the teardown form: best effort, never returns.
func (g *opensearchGroup) deleteDomainQuietly(t *harness.TestContext, key string) {
	if name := t.GetString(key); name != "" {
		awscli.Run(t.Endpoint, t.Region, "opensearch", "delete-domain", "--domain-name", name) //nolint:errcheck
	}
}

// domainNames returns the DomainName of every domain ListDomainNames reports,
// optionally filtered by the modeled engineType query parameter.
func (g *opensearchGroup) domainNames(t *harness.TestContext, engineType string) ([]string, map[string]string, error) {
	args := []string{"opensearch", "list-domain-names"}
	if engineType != "" {
		args = append(args, "--engine-type", engineType)
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, args...)
	if err != nil {
		return nil, nil, err
	}
	entries, ok := out["DomainNames"].([]any)
	if !ok {
		return nil, nil, fmt.Errorf("opensearch list-domain-names: DomainNames is %T, want a list: %v", out["DomainNames"], out)
	}
	names := make([]string, 0, len(entries))
	engines := make(map[string]string, len(entries))
	for _, e := range entries {
		info, _ := e.(map[string]any)
		name, _ := info["DomainName"].(string)
		if name == "" {
			return nil, nil, fmt.Errorf("opensearch list-domain-names: entry with no DomainName: %v", info)
		}
		names = append(names, name)
		engines[name], _ = info["EngineType"].(string)
	}
	return names, engines, nil
}

// tagsOf returns a resource's tags as a map, through ListTags.
//
// ListTags is the operation AWS binds to GET /2021-01-01/tags/ — the one
// trailing-slash URI in the OpenSearch model — so every tag assertion in this
// file goes through here and would catch that route being registered at the
// slash-less spelling alone.
func (g *opensearchGroup) tagsOf(t *harness.TestContext, arn string) (map[string]string, error) {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "opensearch", "list-tags", "--arn", arn)
	if err != nil {
		return nil, err
	}
	list, ok := out["TagList"].([]any)
	if !ok && out["TagList"] != nil {
		return nil, fmt.Errorf("opensearch list-tags: TagList is %T, want a list: %v", out["TagList"], out)
	}
	tags := make(map[string]string, len(list))
	for _, e := range list {
		pair, _ := e.(map[string]any)
		key, _ := pair["Key"].(string)
		tags[key], _ = pair["Value"].(string)
	}
	return tags, nil
}

// assertAWSError asserts the CLI reported the named modeled error code and
// that it arrived with the HTTP status the AWS model declares for it.
//
// Both halves matter. The code alone passes against a service answering the
// right name with the wrong status, which is the mistake OpenSearch invites:
// ResourceNotFoundException is 409 here, and 404 is the obvious wrong guess.
func assertOpenSearchError(status int, err error, op, wantCode string, wantStatus int) error {
	if err == nil {
		return fmt.Errorf("opensearch %s: expected %s, got a successful response", op, wantCode)
	}
	if !strings.Contains(err.Error(), wantCode) {
		return fmt.Errorf("opensearch %s: expected error code %s, got: %v", op, wantCode, err)
	}
	if status != wantStatus {
		return fmt.Errorf("opensearch %s: %s arrived with HTTP %d, want HTTP %d — the AWS model declares this status",
			op, wantCode, status, wantStatus)
	}
	return nil
}

// ─── opensearch-domains ───────────────────────────────────────────────────────

func (g *opensearchGroup) setupDomains(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *opensearchGroup) teardownDomains(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order. Every domain this group can create is named
	// exactly, so nothing outside it is touched.
	g.deleteDomainQuietly(t, "os_delete_domain")
	g.deleteDomainQuietly(t, "os_domain")
	return nil
}

// CreateDomain creates the domain the rest of the group describes and lists.
func (g *opensearchGroup) CreateDomain(_ context.Context, t *harness.TestContext) error {
	name := openSearchDomainNamer.Name(t)
	t.Set("os_domain", name)

	status, err := g.createDomain(t, name)
	if err != nil {
		return err
	}
	if got, _ := status["DomainName"].(string); got != name {
		return fmt.Errorf("opensearch CreateDomain: DomainName = %q, want %q", got, name)
	}
	arn, _ := status["ARN"].(string)
	if arn == "" {
		return fmt.Errorf("opensearch CreateDomain: ARN is empty: %v", status)
	}
	if !strings.HasSuffix(arn, ":domain/"+name) {
		return fmt.Errorf("opensearch CreateDomain: ARN = %q, want one ending in :domain/%s", arn, name)
	}
	if got, _ := status["EngineVersion"].(string); got != openSearchEngineVersion {
		return fmt.Errorf("opensearch CreateDomain: EngineVersion = %q, want %q", got, openSearchEngineVersion)
	}
	if created, _ := status["Created"].(bool); !created {
		return fmt.Errorf("opensearch CreateDomain: Created = %v, want true", status["Created"])
	}
	t.Set("os_domain_arn", arn)

	// Roundtrip: the create must be visible to the matching describe.
	described, err := awscli.RunOutput(t.Endpoint, t.Region,
		"opensearch", "describe-domain", "--domain-name", name)
	if err != nil {
		return fmt.Errorf("opensearch CreateDomain: describe-domain after create failed: %w", err)
	}
	ds, _ := described["DomainStatus"].(map[string]any)
	if got, _ := ds["DomainName"].(string); got != name {
		return fmt.Errorf("opensearch CreateDomain: describe-domain returned DomainName %q, want %q", got, name)
	}
	if got, _ := ds["ARN"].(string); got != arn {
		return fmt.Errorf("opensearch CreateDomain: describe-domain returned ARN %q, want the created %q", got, arn)
	}
	return nil
}

// DescribeDomain reads the domain back at GET
// /2021-01-01/opensearch/domain/{DomainName} and pins the HTTP status the CLI's
// own wire log recorded, which is what proves the request reached OpenSearch
// rather than a wildcard route that happened to answer.
func (g *opensearchGroup) DescribeDomain(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("os_domain")
	if name == "" {
		return fmt.Errorf("opensearch DescribeDomain: no os_domain from CreateDomain")
	}
	httpStatus, err := awscli.RunStatus(t.Endpoint, t.Region,
		"opensearch", "describe-domain", "--domain-name", name)
	if err != nil {
		return err
	}
	if httpStatus != 200 {
		return fmt.Errorf("opensearch DescribeDomain: the modeled URI answered HTTP %d, want 200", httpStatus)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"opensearch", "describe-domain", "--domain-name", name)
	if err != nil {
		return err
	}
	domain, _ := out["DomainStatus"].(map[string]any)
	if domain == nil {
		return fmt.Errorf("opensearch DescribeDomain: no DomainStatus in response: %v", out)
	}
	if got, _ := domain["DomainName"].(string); got != name {
		return fmt.Errorf("opensearch DescribeDomain: DomainName = %q, want %q", got, name)
	}
	if got, _ := domain["EngineVersion"].(string); got != openSearchEngineVersion {
		return fmt.Errorf("opensearch DescribeDomain: EngineVersion = %q, want %q", got, openSearchEngineVersion)
	}
	if deleted, _ := domain["Deleted"].(bool); deleted {
		return fmt.Errorf("opensearch DescribeDomain: Deleted = true on a live domain")
	}
	return nil
}

// DescribeDomains is the batch collection form, POST
// /2021-01-01/opensearch/domain-info — a different URI from DescribeDomain's
// and one no caller would reach by guessing. A name matching nothing is asked
// for alongside the real one: AWS models no not-found error on this operation
// and DomainStatusList is required, so the unknown name must simply be absent.
func (g *opensearchGroup) DescribeDomains(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("os_domain")
	if name == "" {
		return fmt.Errorf("opensearch DescribeDomains: no os_domain from CreateDomain")
	}
	absent := name + "-absent"

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"opensearch", "describe-domains", "--domain-names", name, absent)
	if err != nil {
		return err
	}
	list, ok := out["DomainStatusList"].([]any)
	if !ok {
		return fmt.Errorf("opensearch DescribeDomains: DomainStatusList is %T, want a list: %v", out["DomainStatusList"], out)
	}
	var found map[string]any
	for _, e := range list {
		status, _ := e.(map[string]any)
		switch got, _ := status["DomainName"].(string); got {
		case name:
			found = status
		case absent:
			return fmt.Errorf("opensearch DescribeDomains: %q was returned, but no domain by that name exists", absent)
		}
	}
	if found == nil {
		return fmt.Errorf("opensearch DescribeDomains: %q missing from DomainStatusList (%d entries)", name, len(list))
	}
	if got, _ := found["EngineVersion"].(string); got != openSearchEngineVersion {
		return fmt.Errorf("opensearch DescribeDomains: EngineVersion = %q, want %q", got, openSearchEngineVersion)
	}
	if got, _ := found["ARN"].(string); got != t.GetString("os_domain_arn") {
		return fmt.Errorf("opensearch DescribeDomains: ARN = %q, want the created %q", got, t.GetString("os_domain_arn"))
	}
	return nil
}

// ListDomainNames is the collection operation AWS binds to GET
// /2021-01-01/domain — directly under the version prefix, not under
// /opensearch like the rest of the domain surface. Getting that wrong is
// exactly the #963 failure, so the wire log is asserted as well as the body.
func (g *opensearchGroup) ListDomainNames(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("os_domain")
	if name == "" {
		return fmt.Errorf("opensearch ListDomainNames: no os_domain from CreateDomain")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region, "opensearch", "list-domain-names")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("opensearch ListDomainNames: the collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	names, engines, err := g.domainNames(t, "")
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("opensearch ListDomainNames: no domains listed, want at least %q", name)
	}
	if !containsString(names, name) {
		return fmt.Errorf("opensearch ListDomainNames: %q missing from %v", name, names)
	}
	if engines[name] != "OpenSearch" {
		return fmt.Errorf("opensearch ListDomainNames: EngineType for %q = %q, want OpenSearch (the domain runs %s)",
			name, engines[name], openSearchEngineVersion)
	}
	return nil
}

// ListDomainNamesByEngineType exercises the modeled engineType query
// parameter. ListDomainNames is a GET with no body, so engineType can only
// travel in the query string — a service that reads it from anywhere else
// answers with the domains it was asked to exclude.
func (g *opensearchGroup) ListDomainNamesByEngineType(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("os_domain")
	if name == "" {
		return fmt.Errorf("opensearch ListDomainNamesByEngineType: no os_domain from CreateDomain")
	}

	matching, _, err := g.domainNames(t, "OpenSearch")
	if err != nil {
		return err
	}
	if !containsString(matching, name) {
		return fmt.Errorf("opensearch ListDomainNames --engine-type OpenSearch: %q missing from %v", name, matching)
	}

	other, _, err := g.domainNames(t, "Elasticsearch")
	if err != nil {
		return err
	}
	if containsString(other, name) {
		return fmt.Errorf("opensearch ListDomainNames --engine-type Elasticsearch: %q listed, but it runs %s — the engineType filter is not being applied",
			name, openSearchEngineVersion)
	}
	return nil
}

// ListDomainNamesInvalidEngineType pins the modeled ValidationException, which
// OpenSearch answers with HTTP 400 — unlike its not-found and already-exists
// errors, which are 409.
func (g *opensearchGroup) ListDomainNamesInvalidEngineType(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"opensearch", "list-domain-names", "--engine-type", "NotAnEngine")
	return assertOpenSearchError(status, err, "ListDomainNames", "ValidationException", 400)
}

// DescribeDomainNotFound pins the status that alpha.35 changed and that is
// easy to get wrong: OpenSearch's ResourceNotFoundException is HTTP 409.
func (g *opensearchGroup) DescribeDomainNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"opensearch", "describe-domain", "--domain-name", openSearchDomainNamer.Name(t)+"-nope")
	return assertOpenSearchError(status, err, "DescribeDomain", "ResourceNotFoundException", 409)
}

// CreateDomainAlreadyExists pins ResourceAlreadyExistsException, also HTTP 409.
func (g *opensearchGroup) CreateDomainAlreadyExists(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("os_domain")
	if name == "" {
		return fmt.Errorf("opensearch CreateDomainAlreadyExists: no os_domain from CreateDomain")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"opensearch", "create-domain", "--domain-name", name, "--engine-version", openSearchEngineVersion)
	return assertOpenSearchError(status, err, "CreateDomain", "ResourceAlreadyExistsException", 409)
}

// DeleteDomain deletes its own domain rather than the one the rest of the
// group depends on, then polls until the domain is gone.
//
// The poll is bounded and has no sleep in it: each attempt is a fresh CLI
// invocation, which paces it. Overcast deletes synchronously, so the first
// attempt normally settles it; the loop is here because AWS's DeleteDomain is
// asynchronous and reports Deleted=true while the domain drains, and a future
// Overcast that models that must not turn this test into a flake.
func (g *opensearchGroup) DeleteDomain(_ context.Context, t *harness.TestContext) error {
	name := openSearchDeleteNamer.Name(t)
	t.Set("os_delete_domain", name)
	if _, err := g.createDomain(t, name); err != nil {
		return fmt.Errorf("opensearch DeleteDomain: create failed: %w", err)
	}

	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"opensearch", "delete-domain", "--domain-name", name)
	if err != nil {
		return err
	}
	status, _ := out["DomainStatus"].(map[string]any)
	if status == nil {
		return fmt.Errorf("opensearch DeleteDomain: no DomainStatus in response: %v", out)
	}
	if got, _ := status["DomainName"].(string); got != name {
		return fmt.Errorf("opensearch DeleteDomain: DomainName = %q, want %q", got, name)
	}
	if deleted, _ := status["Deleted"].(bool); !deleted {
		return fmt.Errorf("opensearch DeleteDomain: Deleted = %v, want true", status["Deleted"])
	}

	const maxPolls = 10
	var lastStatus int
	var lastErr error
	gone := false
	for attempt := 0; attempt < maxPolls && !gone; attempt++ {
		lastStatus, lastErr = awscli.RunStatus(t.Endpoint, t.Region,
			"opensearch", "describe-domain", "--domain-name", name)
		gone = lastErr != nil
	}
	if !gone {
		return fmt.Errorf("opensearch DeleteDomain: describe-domain still returns %q after %d polls", name, maxPolls)
	}
	if err := assertOpenSearchError(lastStatus, lastErr, "DescribeDomain after delete", "ResourceNotFoundException", 409); err != nil {
		return err
	}

	names, _, err := g.domainNames(t, "")
	if err != nil {
		return fmt.Errorf("opensearch DeleteDomain: list-domain-names after delete failed: %w", err)
	}
	if containsString(names, name) {
		return fmt.Errorf("opensearch DeleteDomain: %q still listed after delete: %v", name, names)
	}
	return nil
}

// DeleteDomainNotFound pins the not-found status on the delete path too — the
// handler is a different one from DescribeDomain's.
func (g *opensearchGroup) DeleteDomainNotFound(_ context.Context, t *harness.TestContext) error {
	status, err := awscli.RunStatus(t.Endpoint, t.Region,
		"opensearch", "delete-domain", "--domain-name", openSearchDeleteNamer.Name(t)+"-nope")
	return assertOpenSearchError(status, err, "DeleteDomain", "ResourceNotFoundException", 409)
}

// ─── opensearch-tags ──────────────────────────────────────────────────────────

// setupTags creates the domain the tag operations address. OpenSearch's tag
// operations are named unlike every other AWS service's — AddTags / ListTags /
// RemoveTags rather than TagResource / ListTagsForResource / UntagResource —
// and they take an --arn rather than a resource name.
func (g *opensearchGroup) setupTags(_ context.Context, t *harness.TestContext) error {
	name := openSearchTagNamer.Name(t)
	t.Set("os_tag_domain", name)
	status, err := g.createDomain(t, name)
	if err != nil {
		return fmt.Errorf("opensearch tags setup: create-domain %q: %w", name, err)
	}
	arn, _ := status["ARN"].(string)
	if arn == "" {
		return fmt.Errorf("opensearch tags setup: create-domain %q returned no ARN", name)
	}
	t.Set("os_tag_arn", arn)
	return nil
}

func (g *opensearchGroup) teardownTags(_ context.Context, t *harness.TestContext) error {
	// Reverse creation order; DeleteDomain drops the domain's tags with it.
	g.deleteDomainQuietly(t, "os_inline_tag_domain")
	g.deleteDomainQuietly(t, "os_tag_domain")
	return nil
}

// AddTags tags the domain and reads the tags back. The roundtrip goes through
// ListTags, which is the operation AWS binds to the trailing-slash collection
// URI, so this test fails when that route is registered slash-less only.
func (g *opensearchGroup) AddTags(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("os_tag_arn")
	if arn == "" {
		return fmt.Errorf("opensearch AddTags: no os_tag_arn from setup")
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"opensearch", "add-tags",
		"--arn", arn,
		"--tag-list", "Key=team,Value=search", "Key=env,Value=compat",
	); err != nil {
		return err
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("opensearch AddTags: list-tags after add failed: %w", err)
	}
	if tags["team"] != "search" {
		return fmt.Errorf("opensearch AddTags: tag team = %q, want search (tags: %v)", tags["team"], tags)
	}
	if tags["env"] != "compat" {
		return fmt.Errorf("opensearch AddTags: tag env = %q, want compat (tags: %v)", tags["env"], tags)
	}
	return nil
}

// ListTags is the collection operation the AWS model binds to
// GET /2021-01-01/tags/ — with a trailing slash, the only URI in the whole
// OpenSearch model that carries one, and the exact shape of issue #963. The
// status the CLI's wire log recorded is asserted rather than only the body,
// because an unsigned request to an unbound OpenSearch path falls through to
// S3's wildcard and comes back 404 NoSuchKey — an answer that is not
// OpenSearch's at all. Which spelling the CLI sends depends on the model its
// release carries; the test is about the emulator answering whichever one it
// chose.
func (g *opensearchGroup) ListTags(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("os_tag_arn")
	if arn == "" {
		return fmt.Errorf("opensearch ListTags: no os_tag_arn from setup")
	}
	status, err := awscli.RunStatus(t.Endpoint, t.Region, "opensearch", "list-tags", "--arn", arn)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("opensearch ListTags: the tags collection URI answered HTTP %d, want 200 — the URI the AWS model binds is not being served", status)
	}

	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return err
	}
	if len(tags) != 2 || tags["team"] != "search" || tags["env"] != "compat" {
		return fmt.Errorf("opensearch ListTags: tags = %v, want team=search and env=compat from AddTags", tags)
	}
	return nil
}

// RemoveTags drops one key and leaves the other, so a handler that clears the
// whole tag set fails here rather than passing on "no error".
func (g *opensearchGroup) RemoveTags(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("os_tag_arn")
	if arn == "" {
		return fmt.Errorf("opensearch RemoveTags: no os_tag_arn from setup")
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"opensearch", "remove-tags", "--arn", arn, "--tag-keys", "env",
	); err != nil {
		return err
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("opensearch RemoveTags: list-tags after remove failed: %w", err)
	}
	if _, still := tags["env"]; still {
		return fmt.Errorf("opensearch RemoveTags: tag env is still present after removal (tags: %v)", tags)
	}
	if tags["team"] != "search" {
		return fmt.Errorf("opensearch RemoveTags: tag team = %q, want search — removing env took the other tag with it (tags: %v)",
			tags["team"], tags)
	}
	return nil
}

// CreateDomainWithTagList covers the TagList member CreateDomain accepts
// inline, which is a second, separate path into the tag store from AddTags.
func (g *opensearchGroup) CreateDomainWithTagList(_ context.Context, t *harness.TestContext) error {
	name := openSearchInlineTagName.Name(t)
	t.Set("os_inline_tag_domain", name)

	status, err := g.createDomain(t, name, "--tag-list", "Key=owner,Value=compat")
	if err != nil {
		return err
	}
	arn, _ := status["ARN"].(string)
	if arn == "" {
		return fmt.Errorf("opensearch CreateDomainWithTagList: no ARN in response: %v", status)
	}
	tags, err := g.tagsOf(t, arn)
	if err != nil {
		return fmt.Errorf("opensearch CreateDomainWithTagList: list-tags failed: %w", err)
	}
	if tags["owner"] != "compat" {
		return fmt.Errorf("opensearch CreateDomainWithTagList: tag owner = %q, want compat — the creation-time TagList was not applied (tags: %v)",
			tags["owner"], tags)
	}
	return nil
}

// containsString reports whether want is in list.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
