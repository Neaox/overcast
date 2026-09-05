//go:build dev

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/overcast-sh/overcast/internal/awsapi"
)

// Scaffolding — docs/plans/compat-coverage-modelgen.md §3.3.
//
// `-scaffold <service>` proposes a recipe skeleton from the model: one
// resource per Smithy resource shape where the model has them — no service in
// scope has any, so in practice one per Create/Get|Describe/List/Update|Set/
// Delete name cluster. Scaffolding is a time-saver, never an authority, so
// the skeleton is written to make the generated-vs-curated line visible on
// the page rather than to be as complete as it can be. Three markers do that,
// and every one of them is refused by recipe.schema.json, so a skeleton can
// never be mistaken for a finished recipe:
//
//   - `$comment` on a block names the trait or rule that produced each derived
//     value in it. A wrong derivation and a right one then look different.
//   - `$todo` stands where only a human can supply the value, with a one-line
//     hint. Every field of the recipe vocabulary a human must fill gets one —
//     including the ones that do not apply to this service — so the reader
//     sees what is expected rather than what happened to be optional.
//   - `$review` marks a lifecycle whose create or delete the default-deny verb
//     rule cannot call safe. Such a lifecycle is never proposed bare.
const (
	// scaffoldTodo is the placeholder key. It is not one of the five
	// expression forms, so recipe.schema.json refuses it until it is replaced.
	scaffoldTodo = "$todo"
	// scaffoldReview is the key a lifecycle carries when the model cannot show
	// that the run undoes its own create. `resources` items are
	// `additionalProperties: false`, so the schema refuses it too.
	scaffoldReview = "$review"
	// scaffoldReviewMarker is that key's fixed value. It is the same sentence
	// for every marked lifecycle: the reason differs and goes in `$comment`,
	// but the instruction to the reader never does.
	scaffoldReviewMarker = "irreversible on AWS — confirm before keeping"
)

// scaffoldHeader is the top-level comment. It says what each marker means,
// because a reader with one scaffold in front of them has nowhere else to
// learn it.
const scaffoldHeader = "Scaffolded by `go run -tags dev ./cmd/compatgen -scaffold %s`. " +
	"A `$comment` names the trait or rule that produced the values beside it — check it rather than trusting it. " +
	"A `$todo` is a value only a human can supply: replace every one, or delete the field where it does not apply. " +
	"A `$review` is a lifecycle whose create or delete is not read-only-safe by the verb rule: confirm the noun " +
	"against real AWS, then delete the marker or delete the resource. " +
	"Remove this comment last. See compat/model/README.md."

func runScaffold(opts options, stdout io.Writer) error {
	service := opts.scaffold
	model, err := loadModel(filepath.Join(opts.root, filepath.FromSlash(shapesDir)), service)
	if err != nil {
		return err
	}
	skeleton := scaffold(model, awsapi.ServiceKey(service))
	contents, err := encodeDocument(skeleton)
	if err != nil {
		return err
	}
	_, err = stdout.Write(contents)
	return err
}

// scaffoldRecipe is the skeleton's shape: the recipe's fields, in its
// order, with a leading comment. It is encoded with map values so a $todo
// placeholder can sit where an expression will go.
type scaffoldRecipe struct {
	Comment   string             `json:"$comment"`
	Service   string             `json:"service"`
	Model     string             `json:"model,omitempty"`
	Resources []scaffoldResource `json:"resources"`
}

// scaffoldResource keeps the recipe's field order so the skeleton reads like
// a finished recipe; values are maps so a $todo placeholder can sit where an
// expression will go.
//
// Unlike a recipe's own resource, every field a human supplies is present.
// Somebody reading the skeleton is deciding what this service needs, and a
// key that is simply absent tells them nothing; a `$todo` naming what would
// go there tells them whether to fill it in or delete it.
type scaffoldResource struct {
	Comment    string         `json:"$comment"`
	Review     string         `json:"$review,omitempty"`
	ID         string         `json:"id"`
	Requires   map[string]any `json:"requires"`
	Create     map[string]any `json:"create"`
	Exports    map[string]any `json:"exports"`
	Derived    map[string]any `json:"derived"`
	Binds      map[string]any `json:"binds"`
	Read       map[string]any `json:"read,omitempty"`
	List       map[string]any `json:"list,omitempty"`
	Mutable    []any          `json:"mutable,omitempty"`
	Tags       map[string]any `json:"tags"`
	Delete     map[string]any `json:"delete,omitempty"`
	NotFound   map[string]any `json:"notFound,omitempty"`
	Async      map[string]any `json:"async"`
	Operations map[string]any `json:"operations"`
}

// placeholder is a $todo with its hint. The hint is one line: it says what
// belongs there and, where the model knows, what the value looks like.
func placeholder(format string, args ...any) map[string]any {
	return map[string]any{scaffoldTodo: fmt.Sprintf(format, args...)}
}

func scaffold(model *serviceModel, service string) scaffoldRecipe {
	out := scaffoldRecipe{
		Comment: fmt.Sprintf(scaffoldHeader, model.Service),
		Service: service,
		// A service the clustering finds nothing in still scaffolds to a
		// recipe-shaped document: `"resources": []` is an empty list to fill
		// in, where `null` reads as a bug in the scaffolder.
		Resources: []scaffoldResource{},
	}
	if service != model.Service {
		out.Model = model.Service
	}
	for _, cluster := range resourceClusters(model) {
		out.Resources = append(out.Resources, scaffoldOne(model, cluster))
	}
	return out
}

// resourceCluster is a proposed resource: its lifecycle operations by role.
type resourceCluster struct {
	id, noun                           string
	create, read, update, delete, list string
	tag, untag, tagList                string
	// attached records, per role, the sub-noun an operation was clustered
	// from when it did not name the create's noun directly
	// (GetQueueAttributes → "QueueAttributes", on the noun "Queue").
	attached    map[string]string
	identifiers []string
	source      string // "name clustering", or the resource shape
}

// lifecycle is the cluster's operations in lifecycle order, for its comment.
func (c resourceCluster) lifecycle() []string {
	return presentOps(c.create, c.read, c.list, c.update, c.delete)
}

// provenance describes how a role's operation reached this cluster, for the
// block's `$comment`.
func (c resourceCluster) provenance(role, op string) string {
	if c.source != nameClustering {
		return fmt.Sprintf("op from the %s: its `%s` binding is %s", c.source, role, op)
	}
	if sub, ok := c.attached[role]; ok {
		return fmt.Sprintf("op from name clustering: %s, on %s — a noun under the create's noun %s", op, sub, c.noun)
	}
	return fmt.Sprintf("op from name clustering: %s names the noun %s", op, c.noun)
}

// nameClustering is the source of a cluster the model's resource shapes did
// not give us — which, at the pinned revision, is every cluster: no service
// in scope declares a single Smithy `resource` shape.
const nameClustering = "name clustering"

// resourceClusters proposes resources from Smithy resource shapes, else from
// operation-name clustering.
//
// Clustering runs in two passes, and the order matters. The first assigns an
// operation to the noun its own name carries; only then does the second
// attach an operation whose noun sits *under* a noun some create names, into
// a role the first pass left empty. Without that ordering `ListQueueTags`
// ("QueueTag") would take the queue's `list` before `ListQueues` ("Queue")
// could, because the sub-noun sorts first.
func resourceClusters(model *serviceModel) []resourceCluster {
	if shapes := model.Resources(); len(shapes) > 0 {
		return resourceShapeClusters(model, shapes)
	}
	byNoun := make(map[string]*resourceCluster)
	cluster := func(noun string) *resourceCluster {
		if existing, ok := byNoun[noun]; ok {
			return existing
		}
		fresh := &resourceCluster{
			id:       strings.ToLower(noun),
			noun:     noun,
			attached: map[string]string{},
			source:   nameClustering,
		}
		byNoun[noun] = fresh
		return fresh
	}
	// Pass one: the noun the operation's own name carries.
	for _, op := range model.Operations() {
		role, noun := classifyOperation(op)
		if role == "" {
			continue
		}
		assignRole(cluster(noun), role, op)
	}
	// Pass two: a noun nothing creates, attached to the longest created noun
	// it extends, filling only roles pass one left empty. Only the
	// Get/Set/Update/Tag families attach — a Create or a Delete on a sub-noun
	// is a lifecycle of its own, not a step in this one's.
	var created []string
	for noun, c := range byNoun {
		if c.create != "" {
			created = append(created, noun)
		}
	}
	sort.Strings(created)
	for _, noun := range sortedNouns(byNoun) {
		sub := byNoun[noun]
		if sub.create != "" {
			continue
		}
		parent := byNoun[longestPrefixNoun(created, noun)]
		if parent == nil {
			continue
		}
		for _, role := range []string{"read", "update", "tag", "untag", "tagList"} {
			op := roleOp(sub, role)
			if op == "" || roleOp(parent, role) != "" {
				continue
			}
			assignRole(parent, role, op)
			parent.attached[role] = noun
		}
	}
	var clusters []resourceCluster
	for _, noun := range sortedNouns(byNoun) {
		c := byNoun[noun]
		if c.create == "" {
			continue // a resource nothing creates is not a lifecycle
		}
		clusters = append(clusters, *c)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].id < clusters[j].id })
	return clusters
}

func resourceShapeClusters(model *serviceModel, shapes []string) []resourceCluster {
	var clusters []resourceCluster
	for _, name := range shapes {
		shape := model.Shapes[name]
		bare := bareShapeName(name)
		c := resourceCluster{
			id:       strings.ToLower(bare),
			noun:     bare,
			create:   firstNonEmpty(shape.Create, shape.Put),
			read:     shape.Read,
			update:   shape.Update,
			delete:   shape.Delete,
			list:     shape.List,
			attached: map[string]string{},
			source:   "Smithy resource shape " + bare,
		}
		for identifier := range shape.Identifiers {
			c.identifiers = append(c.identifiers, identifier)
		}
		sort.Strings(c.identifiers)
		clusters = append(clusters, c)
	}
	return clusters
}

func sortedNouns(byNoun map[string]*resourceCluster) []string {
	nouns := make([]string, 0, len(byNoun))
	for noun := range byNoun {
		nouns = append(nouns, noun)
	}
	sort.Strings(nouns)
	return nouns
}

// longestPrefixNoun returns the longest created noun that `noun` extends, or
// "" for a noun that extends none. `QueueAttributes` extends `Queue`; `Queue`
// does not extend itself.
func longestPrefixNoun(created []string, noun string) string {
	best := ""
	for _, candidate := range created {
		if candidate != noun && strings.HasPrefix(noun, candidate) && len(candidate) > len(best) {
			best = candidate
		}
	}
	return best
}

func roleOp(c *resourceCluster, role string) string {
	switch role {
	case "create":
		return c.create
	case "read":
		return c.read
	case "update":
		return c.update
	case "delete":
		return c.delete
	case "list":
		return c.list
	case "tag":
		return c.tag
	case "untag":
		return c.untag
	case "tagList":
		return c.tagList
	}
	return ""
}

// assignRole fills a role, keeping the first operation to claim it. The
// operations arrive sorted, so "first" is the model's name order and nothing
// else.
func assignRole(c *resourceCluster, role, op string) {
	switch role {
	case "create":
		c.create = firstNonEmpty(c.create, op)
	case "read":
		c.read = firstNonEmpty(c.read, op)
	case "update":
		c.update = firstNonEmpty(c.update, op)
	case "delete":
		c.delete = firstNonEmpty(c.delete, op)
	case "list":
		c.list = firstNonEmpty(c.list, op)
	case "tag":
		c.tag = firstNonEmpty(c.tag, op)
	case "untag":
		c.untag = firstNonEmpty(c.untag, op)
	case "tagList":
		c.tagList = firstNonEmpty(c.tagList, op)
	}
}

// classifyOperation splits an operation name into a lifecycle role and the
// noun it acts on: CreateQueue → (create, Queue), ListQueues → (list, Queue),
// ListQueueTags → (tagList, Queue).
func classifyOperation(op string) (role, noun string) {
	for _, prefix := range []struct{ prefix, role string }{
		{"Create", "create"}, {"Describe", "read"}, {"Get", "read"},
		{"Update", "update"}, {"Set", "update"}, {"Delete", "delete"},
		{"Untag", "untag"}, {"Tag", "tag"}, {"List", "list"},
	} {
		rest, ok := strings.CutPrefix(op, prefix.prefix)
		if !ok || rest == "" {
			continue
		}
		if prefix.role != "list" {
			return prefix.role, rest
		}
		rest = singular(rest)
		// `List<Noun>Tags` reads the tag lifecycle, not the noun's own page.
		if tagged, ok := strings.CutSuffix(rest, "Tag"); ok && tagged != "" {
			return "tagList", tagged
		}
		return "list", rest
	}
	return "", ""
}

// singular strips a trailing plural: Queues → Queue, Policies → Policy.
func singular(noun string) string {
	switch {
	case strings.HasSuffix(noun, "ies"):
		return strings.TrimSuffix(noun, "ies") + "y"
	case strings.HasSuffix(noun, "ses"):
		return strings.TrimSuffix(noun, "es")
	case strings.HasSuffix(noun, "s"):
		return strings.TrimSuffix(noun, "s")
	}
	return noun
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// scaffoldOne renders one cluster as a recipe resource: every derived value
// with the rule that produced it, every human-supplied field as a $todo, and
// the `$review` marker where the lifecycle needs confirming.
func scaffoldOne(model *serviceModel, cluster resourceCluster) scaffoldResource {
	comment := fmt.Sprintf("from %s: %s", cluster.source, strings.Join(cluster.lifecycle(), "/"))
	if len(cluster.identifiers) > 0 {
		comment += "; identifiers: " + strings.Join(cluster.identifiers, ", ")
	}
	res := scaffoldResource{
		ID:     cluster.id,
		Create: scaffoldCall(model, cluster.provenance("create", cluster.create), cluster.create, true),
		Requires: placeholder("resource ids this one needs created first, in dependency order (`[\"dlq\"]`); "+
			"delete the field where %s needs nothing", cluster.id),
		Derived: placeholder("exports that need a second call after create: " +
			"`[{\"export\": \"arn\", \"op\": \"GetThingAttributes\", \"params\": {…}, \"path\": \"$.Attributes.Arn\"}]`; " +
			"delete the field where create returns everything"),
		Async: placeholder("`{\"maxAttempts\": 5, \"delayMs\": 200}` where the resource is eventually consistent, " +
			"which wraps every clause that verifies by calling again in `eventually`; delete the field where it is not"),
		Operations: placeholder("authored coverage, in the IR's own assertion vocabulary, for operations no lifecycle " +
			"role reaches (a purge, the batch calls); delete the field where there are none"),
		Binds:   map[string]any{},
		Exports: scaffoldExports(model, cluster),
		Tags:    scaffoldTags(model, cluster),
	}
	if cluster.read != "" {
		res.Read = scaffoldRead(model, cluster)
	}
	if cluster.list != "" {
		res.List = scaffoldList(model, cluster)
	}
	if cluster.update != "" {
		res.Mutable = []any{map[string]any{
			"$comment": cluster.provenance("update", cluster.update),
			"op":       cluster.update,
			"member": placeholder("dotted input member of %s this mutation changes "+
				"(`Attributes.VisibilityTimeout`)", cluster.update),
			"from":     placeholder("value expression for what create leaves the member at; omit where create does not set it"),
			"to":       placeholder("value expression for the value %s sets", cluster.update),
			"readPath": placeholder("path in the read's response that reflects the change (`$.Attributes.VisibilityTimeout`)"),
		}}
	}
	if cluster.delete != "" {
		res.Delete = scaffoldCall(model, cluster.provenance("delete", cluster.delete), cluster.delete, false)
		res.NotFound = scaffoldNotFound(model, cluster)
	}
	// Required members of every lifecycle op, as binds to fill.
	for _, op := range []string{cluster.read, cluster.update, cluster.delete} {
		if op == "" {
			continue
		}
		for _, member := range model.RequiredMembers(model.InputShape(op)) {
			if _, present := res.Binds[member]; !present {
				res.Binds[member] = placeholder("context path `<resource>.<export>` supplying %s (`%s.id`)", member, cluster.id)
			}
		}
	}
	if len(res.Binds) > 0 {
		res.Binds["$comment"] = "one key per @required input member of " +
			strings.Join(presentOps(cluster.read, cluster.update, cluster.delete), "/") +
			"; each needs the export that supplies it, and a member a `params` or `mutable` entry supplies " +
			"instead is deleted from here"
	} else {
		res.Binds = placeholder("input member name → context path, for a member a sibling resource supplies " +
			"(`{\"QueueUrl\": \"queue.id\"}`); none of this lifecycle's calls has a @required member")
	}
	if reason := irreversibleReason(model, cluster); reason != "" {
		res.Review = scaffoldReviewMarker
		comment += "; " + reason
	}
	res.Comment = comment
	return res
}

// irreversibleReason says why a proposed lifecycle may not be kept without a
// human confirming the noun, or "" for one that may.
//
// This is the probe rule's default-deny applied to a lifecycle. `Create*` and
// `Delete*` are never read-only-safe against a live account, so a proposal
// earns its place only where the model shows the run undoing its own create:
// a delete operation, *and* a modeled not-found error that proves the delete
// took effect. Organizations is the worked example — `CreateAccount` and
// `CreateGovCloudAccount` have no delete at all, because an AWS account
// cannot be deleted, and `DeleteOrganization` has no not-found error to
// verify it with, so all three are marked; `organizationalunit` and `policy`,
// which have a delete and a `*NotFoundException`, are not.
func irreversibleReason(model *serviceModel, cluster resourceCluster) string {
	switch {
	case cluster.delete == "":
		return fmt.Sprintf("marked %s because the model gives the noun %s no delete, so nothing undoes %s",
			scaffoldReview, cluster.noun, cluster.create)
	case cluster.read == "":
		return fmt.Sprintf("marked %s because the noun %s has no read, so nothing proves %s took effect",
			scaffoldReview, cluster.noun, cluster.delete)
	case len(notFoundCandidates(model, cluster.read)) == 0:
		return fmt.Sprintf("marked %s because %s declares no not-found error, so nothing proves %s took effect",
			scaffoldReview, cluster.read, cluster.delete)
	}
	return ""
}

// scaffoldExports proposes the identity export from the create's output.
func scaffoldExports(model *serviceModel, cluster resourceCluster) map[string]any {
	value, provenance := scaffoldIdentity(model, cluster.create, "create")
	out := map[string]any{"id": value}
	if provenance != "" {
		out["$comment"] = provenance
	}
	return out
}

func scaffoldRead(model *serviceModel, cluster resourceCluster) map[string]any {
	out := scaffoldCall(model, cluster.provenance("read", cluster.read), cluster.read, false)
	value, provenance := scaffoldIdentity(model, cluster.read, "read")
	out["identityPath"] = value
	out["identity"] = "id"
	if provenance != "" {
		out["$comment"] = out["$comment"].(string) + "; " + provenance
	}
	return out
}

func scaffoldList(model *serviceModel, cluster resourceCluster) map[string]any {
	out := scaffoldCall(model, cluster.provenance("list", cluster.list), cluster.list, false)
	comments := []string{out["$comment"].(string)}
	items, itemsFrom := scaffoldItems(model, cluster.list)
	out["itemsPath"] = items
	if itemsFrom != "" {
		comments = append(comments, itemsFrom)
	}
	identity, identityFrom := scaffoldListIdentity(model, cluster.list)
	out["identityPath"] = identity
	if identityFrom != "" {
		comments = append(comments, identityFrom)
	}
	out["identity"] = "id"
	out["$comment"] = strings.Join(comments, "; ")
	return out
}

// scaffoldTags proposes the tag lifecycle. No service in scope carries
// `aws.api#taggable` — the trait hangs off resource shapes, and there are
// none — so the operations come from name clustering, and the member names
// and the read-back path stay a $todo.
func scaffoldTags(model *serviceModel, cluster resourceCluster) map[string]any {
	if cluster.tag == "" && cluster.untag == "" && cluster.tagList == "" {
		return placeholder("the tag lifecycle: `{\"tag\": {\"op\": …, \"member\": …}, \"untag\": {…}, "+
			"\"list\": {\"op\": …, \"path\": …}}`. Name clustering found no Tag/Untag/List*Tags operation on "+
			"the noun %s; delete the field unless the service spells them differently", cluster.noun)
	}
	source := "name clustering"
	if hasTaggableTrait(model) {
		source = "name clustering (the model declares @taggable, which the scaffolder does not read)"
	}
	return placeholder("the tag lifecycle, from %s: `{\"tag\": {\"op\": \"%s\", \"member\": …}, "+
		"\"untag\": {\"op\": \"%s\", \"member\": …}, \"list\": {\"op\": \"%s\", \"path\": …}}`. "+
		"Supply each member name and the path the tags read back at",
		source, orTodo(cluster.tag), orTodo(cluster.untag), orTodo(cluster.tagList))
}

// hasTaggableTrait reports whether the snapshot carries `aws.api#taggable`
// anywhere. The pruner allowlists the trait, so the question is answerable;
// the answer is simply "no" for every service in scope today.
func hasTaggableTrait(model *serviceModel) bool {
	for _, shape := range model.Shapes {
		if hasTrait(shape.Traits, "aws.api#taggable") {
			return true
		}
	}
	return false
}

// scaffoldCall names an operation and, for the create, lists its required
// members as placeholders describing each member's shape. For reads and
// deletes the members are expected to bind, so they are listed in `binds`.
func scaffoldCall(model *serviceModel, provenance, op string, withParams bool) map[string]any {
	out := map[string]any{"$comment": provenance, "op": op}
	if !withParams {
		return out
	}
	params := map[string]any{}
	input := model.InputShape(op)
	required := model.RequiredMembers(input)
	for _, member := range required {
		target, _ := model.MemberTarget(input, member)
		params[member] = placeholder("value expression for %s — %s", member, describeForScaffold(model, target))
	}
	out["params"] = params
	if len(required) > 0 {
		out["$comment"] = provenance + "; params from its @required members: " + strings.Join(required, ", ")
	} else {
		out["$comment"] = provenance + "; the model marks none of its input members @required"
	}
	return out
}

func describeForScaffold(model *serviceModel, target string) string {
	kind := model.Kind(target)
	name := target
	if strings.HasPrefix(target, "smithy.api#") {
		name = kind
	}
	var notes []string
	if kind == "enum" {
		values := model.EnumValues(target)
		if len(values) > 4 {
			values = append(values[:4], "…")
		}
		notes = append(notes, "one of "+strings.Join(values, ", "))
	}
	c := model.Constraints(target)
	if c.Pattern != "" {
		notes = append(notes, "pattern "+c.Pattern)
	}
	if c.LengthMin != nil || c.LengthMax != nil {
		notes = append(notes, fmt.Sprintf("length %s..%s", optInt(c.LengthMin), optInt(c.LengthMax)))
	}
	if len(notes) == 0 {
		return fmt.Sprintf("%s (%s)", name, kind)
	}
	return fmt.Sprintf("%s (%s; %s)", name, kind, strings.Join(notes, "; "))
}

func optInt(v *int64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(*v)
}

// scaffoldIdentity proposes an identity path from an operation's output, and
// says which rule produced it.
//
// A path that lands on a structure, list or map is *not* an identity, and
// proposing one there is the mistake that reads as a finished value:
// `$.Organization` and `$.CreateAccountStatus` are envelopes, and which
// member inside identifies the resource is a choice only a human makes. Those
// come back as a $todo whose hint lists what the envelope holds.
func scaffoldIdentity(model *serviceModel, op, role string) (value any, provenance string) {
	output := model.OutputShape(op)
	if output == "" {
		return placeholder("%s returns nothing, so it exports no identity; give the %s another source, "+
			"or make this a resource with no create", op, role), ""
	}
	member := identityMember(model, op, output)
	if member == "" {
		return placeholder("path in the %s response carrying the identity; its members are %s",
			op, membersOf(model, output)), ""
	}
	target, _ := model.MemberTarget(output, member)
	if !isScalarKind(model.Kind(target)) {
		return placeholder("`$.%s` is not an identity: it is %s. Pick the value inside it that identifies the "+
			"resource (`$.%s.<member>`)", member, describeContainer(model, target), member), ""
	}
	return "$." + member, fmt.Sprintf("identity path from the identity-member rule over the %s response: %s", op, member)
}

// scaffoldItems proposes the list member of a list operation's output.
func scaffoldItems(model *serviceModel, op string) (value any, provenance string) {
	output := model.OutputShape(op)
	if items := model.Pagination(op).Items; items != "" {
		if _, ok := model.MemberTarget(output, items); ok {
			return "$." + items, "itemsPath from @paginated.items"
		}
	}
	var lists []string
	for _, member := range model.Members(output) {
		if target, _ := model.MemberTarget(output, member); model.Kind(target) == "list" {
			lists = append(lists, member)
		}
	}
	switch len(lists) {
	case 0:
		return placeholder("path to the page of items in the %s response; its members are %s", op, membersOf(model, output)), ""
	case 1:
		return "$." + lists[0], fmt.Sprintf("itemsPath from the sole list member of the %s response", op)
	default:
		return placeholder("path to the page of items in the %s response; it has more than one list: %s",
			op, strings.Join(lists, ", ")), ""
	}
}

// scaffoldListIdentity proposes the item-relative identity path from the
// element type of the list scaffoldItems picked.
func scaffoldListIdentity(model *serviceModel, op string) (value any, provenance string) {
	items, from := scaffoldItems(model, op)
	path, ok := items.(string)
	if !ok || from == "" {
		return placeholder("item-relative path carrying the identity, once itemsPath is settled; " +
			"`$` for a list of strings"), ""
	}
	target, _ := model.MemberTarget(model.OutputShape(op), strings.TrimPrefix(path, "$."))
	element := model.Shapes[target].Member
	if isScalarKind(model.Kind(element)) {
		return "$", fmt.Sprintf("identityPath `$` because %s is a list of %s", path, model.Kind(element))
	}
	return placeholder("item-relative path carrying the identity; each item is %s", describeContainer(model, element)), ""
}

// scaffoldNotFound proposes the error a missing resource raises, from the
// read's own modeled errors.
func scaffoldNotFound(model *serviceModel, cluster resourceCluster) map[string]any {
	candidates := notFoundCandidates(model, cluster.read)
	if len(candidates) == 0 {
		return placeholder("the error a read of the deleted %s raises, which is what the delete's absence clause "+
			"asserts; %s declares none the name rule recognises", cluster.id, firstNonEmpty(cluster.read, "the noun's read"))
	}
	comment := "from the read's modeled errors: " + candidates[0]
	if len(candidates) > 1 {
		comment += fmt.Sprintf(" (also %s — confirm which one a missing resource raises)",
			strings.Join(candidates[1:], ", "))
	}
	return map[string]any{"$comment": comment, "error": candidates[0]}
}

func notFoundCandidates(model *serviceModel, readOp string) []string {
	if readOp == "" {
		return nil
	}
	var out []string
	for _, e := range model.OperationErrors(readOp) {
		if strings.Contains(e, "NotFound") || strings.Contains(e, "DoesNotExist") || strings.Contains(e, "NonExistent") {
			out = append(out, e)
		}
	}
	return out
}

func isScalarKind(kind string) bool {
	switch kind {
	case "string", "enum", "integer", "float", "boolean", "timestamp":
		return true
	}
	return false
}

// describeContainer names a non-scalar shape the way a hint should read: a
// structure by its members, a list by its element, a map by its value.
func describeContainer(model *serviceModel, target string) string {
	switch kind := model.Kind(target); kind {
	case "structure", "union":
		return fmt.Sprintf("the %s %s, holding %s", kind, bareShapeName(target), membersOf(model, target))
	case "list":
		return fmt.Sprintf("a list of %s", bareShapeName(model.Shapes[target].Member))
	case "map":
		return fmt.Sprintf("a map of %s to %s",
			bareShapeName(model.Shapes[target].Key), bareShapeName(model.Shapes[target].Value))
	default:
		return fmt.Sprintf("a %s (%s)", kind, bareShapeName(target))
	}
}

// membersOf lists a structure's members for a hint, capped so one line stays
// one line.
func membersOf(model *serviceModel, structure string) string {
	members := model.Members(structure)
	if len(members) == 0 {
		return "none"
	}
	if len(members) > 10 {
		members = append(members[:10:10], "…")
	}
	return strings.Join(members, ", ")
}

func presentOps(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func orTodo(op string) string {
	if op == "" {
		return "…"
	}
	return op
}
