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
// resource per Smithy resource shape where the model has them, else one per
// Create/Get|Describe/List/Update|Set/Delete name cluster; every required
// member pre-listed as a `$todo` placeholder that the recipe schema rejects,
// so the skeleton cannot be mistaken for a finished recipe. A human replaces
// the placeholders and reviews. Scaffolding is a time-saver, never an
// authority.

// scaffoldTodo is the placeholder key. It is not one of the five expression
// forms, so recipe.schema.json refuses it until it is replaced.
const scaffoldTodo = "$todo"

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
type scaffoldResource struct {
	Comment  string         `json:"$comment"`
	ID       string         `json:"id"`
	Create   map[string]any `json:"create"`
	Exports  map[string]any `json:"exports"`
	Binds    map[string]any `json:"binds"`
	Read     map[string]any `json:"read,omitempty"`
	List     map[string]any `json:"list,omitempty"`
	Mutable  []any          `json:"mutable,omitempty"`
	Delete   map[string]any `json:"delete,omitempty"`
	NotFound map[string]any `json:"notFound,omitempty"`
}

func scaffold(model *serviceModel, service string) scaffoldRecipe {
	out := scaffoldRecipe{
		Comment: "Scaffolded by `go run -tags dev ./cmd/compatgen -scaffold " + model.Service + "`. Replace every $todo with a value expression, fill exports/binds/identity paths, delete what does not apply, then remove this comment. See compat/model/README.md.",
		Service: service,
		// A service the clustering finds nothing in still scaffolds to a
		// recipe-shaped document: `"resources": []` is an empty list to fill
		// in, where `null` reads as a bug in the scaffolder.
		Resources: []scaffoldResource{},
	}
	if service != model.Service {
		out.Model = model.Service
	}
	clusters := resourceClusters(model)
	for _, cluster := range clusters {
		out.Resources = append(out.Resources, scaffoldOne(model, cluster))
	}
	return out
}

// resourceCluster is a proposed resource: its lifecycle operations by role.
type resourceCluster struct {
	id                                 string
	create, read, update, delete, list string
	identifiers                        []string
	source                             string // "resource shape" or "name clustering"
}

// resourceClusters proposes resources from Smithy resource shapes, else from
// operation-name clustering.
func resourceClusters(model *serviceModel) []resourceCluster {
	if shapes := model.Resources(); len(shapes) > 0 {
		var clusters []resourceCluster
		for _, name := range shapes {
			shape := model.Shapes[name]
			cluster := resourceCluster{
				id:     strings.ToLower(name),
				create: firstNonEmpty(shape.Create, shape.Put),
				read:   shape.Read,
				update: shape.Update,
				delete: shape.Delete,
				list:   shape.List,
				source: "resource shape",
			}
			for identifier := range shape.Identifiers {
				cluster.identifiers = append(cluster.identifiers, identifier)
			}
			sort.Strings(cluster.identifiers)
			clusters = append(clusters, cluster)
		}
		return clusters
	}
	byNoun := make(map[string]*resourceCluster)
	for _, op := range model.Operations() {
		role, noun := classifyOperation(op)
		if role == "" {
			continue
		}
		cluster, ok := byNoun[noun]
		if !ok {
			cluster = &resourceCluster{id: strings.ToLower(noun), source: "name clustering"}
			byNoun[noun] = cluster
		}
		switch role {
		case "create":
			cluster.create = firstNonEmpty(cluster.create, op)
		case "read":
			cluster.read = firstNonEmpty(cluster.read, op)
		case "update":
			cluster.update = firstNonEmpty(cluster.update, op)
		case "delete":
			cluster.delete = firstNonEmpty(cluster.delete, op)
		case "list":
			cluster.list = firstNonEmpty(cluster.list, op)
		}
	}
	var clusters []resourceCluster
	for _, cluster := range byNoun {
		if cluster.create == "" {
			continue // a resource nothing creates is not a lifecycle
		}
		clusters = append(clusters, *cluster)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].id < clusters[j].id })
	return clusters
}

// classifyOperation splits an operation name into a lifecycle role and the
// noun it acts on: CreateQueue → (create, Queue), ListQueues → (list, Queue).
func classifyOperation(op string) (role, noun string) {
	for _, prefix := range []struct{ prefix, role string }{
		{"Create", "create"}, {"Describe", "read"}, {"Get", "read"},
		{"Update", "update"}, {"Set", "update"}, {"Delete", "delete"}, {"List", "list"},
	} {
		rest, ok := strings.CutPrefix(op, prefix.prefix)
		if !ok || rest == "" {
			continue
		}
		if prefix.role == "list" {
			rest = singular(rest)
		}
		return prefix.role, rest
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

// scaffoldOne renders one cluster as a recipe resource with every required
// member of every lifecycle operation pre-listed.
func scaffoldOne(model *serviceModel, cluster resourceCluster) scaffoldResource {
	res := scaffoldResource{
		Comment: fmt.Sprintf("proposed from %s; identifiers: %s", cluster.source, orNone(cluster.identifiers)),
		ID:      cluster.id,
		Create:  scaffoldCall(model, cluster.create, true),
		Exports: map[string]any{"id": scaffoldIdentityPath(model, cluster.create)},
		Binds:   map[string]any{},
	}
	if cluster.read != "" {
		res.Read = merge(scaffoldCall(model, cluster.read, false), map[string]any{
			"identityPath": scaffoldIdentityPath(model, cluster.read),
			"identity":     "id",
		})
	}
	if cluster.list != "" {
		res.List = merge(scaffoldCall(model, cluster.list, false), map[string]any{
			"itemsPath":    scaffoldItemsPath(model, cluster.list),
			"identityPath": "$.<todo: item field carrying the identity>",
			"identity":     "id",
		})
	}
	if cluster.update != "" {
		res.Mutable = []any{map[string]any{
			"op":       cluster.update,
			"member":   "<todo: dotted input member to change>",
			"from":     map[string]any{scaffoldTodo: "initial value, set at create"},
			"to":       map[string]any{scaffoldTodo: "new value"},
			"readPath": "$.<todo: read response path that reflects the change>",
		}}
	}
	if cluster.delete != "" {
		res.Delete = scaffoldCall(model, cluster.delete, false)
		if errs := notFoundCandidates(model, cluster.read); len(errs) > 0 {
			res.NotFound = map[string]any{"error": errs[0], "$comment": "candidates: " + strings.Join(errs, ", ")}
		}
	}
	// Required members of every lifecycle op, as binds to fill.
	for _, op := range []string{cluster.read, cluster.update, cluster.delete} {
		if op == "" {
			continue
		}
		for _, member := range model.RequiredMembers(model.InputShape(op)) {
			if _, present := res.Binds[member]; !present {
				res.Binds[member] = cluster.id + ".<todo: export carrying " + member + ">"
			}
		}
	}
	return res
}

// scaffoldCall lists an operation's required members as placeholders,
// describing each member's shape so the human knows what to supply. Only the
// create lists them all; for reads and deletes the members are expected to
// bind, so they are listed in `binds` instead.
func scaffoldCall(model *serviceModel, op string, withParams bool) map[string]any {
	out := map[string]any{"op": op}
	if !withParams {
		return out
	}
	params := map[string]any{}
	input := model.InputShape(op)
	for _, member := range model.RequiredMembers(input) {
		target, _ := model.MemberTarget(input, member)
		params[member] = map[string]any{scaffoldTodo: describeForScaffold(model, target)}
	}
	out["params"] = params
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

// scaffoldIdentityPath proposes an identity path from the output's members.
func scaffoldIdentityPath(model *serviceModel, op string) string {
	output := model.OutputShape(op)
	if output == "" {
		return "$.<todo: " + op + " returns nothing>"
	}
	if member := identityMember(model, output); member != "" {
		return "$." + member
	}
	return "$.<todo>"
}

// scaffoldItemsPath proposes the list member of a list operation's output.
func scaffoldItemsPath(model *serviceModel, op string) string {
	output := model.OutputShape(op)
	for _, member := range model.Members(output) {
		if target, _ := model.MemberTarget(output, member); model.Kind(target) == "list" {
			return "$." + member
		}
	}
	return "$.<todo: the list member>"
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

func merge(a, b map[string]any) map[string]any {
	for k, v := range b {
		a[k] = v
	}
	return a
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
