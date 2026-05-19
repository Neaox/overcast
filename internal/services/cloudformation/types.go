package cloudformation

import "time"

// ── Stack ──────────────────────────────────────────────────────────────────

// Stack represents a CloudFormation stack.
// Events are stored separately (see cfnStore.appendStackEvent / getStackEvents)
// so that stack metadata reads never load the full event history.
type Stack struct {
	StackName       string            `json:"StackName"`
	StackID         string            `json:"StackId"`
	Region          string            `json:"Region,omitempty"`
	ParentStackID   string            `json:"ParentId,omitempty"`
	RootID          string            `json:"RootId,omitempty"`
	TemplateBody    string            `json:"TemplateBody"`
	Parameters      []Parameter       `json:"Parameters,omitempty"`
	Tags            []Tag             `json:"Tags,omitempty"`
	Outputs         []Output          `json:"Outputs,omitempty"`
	Resources       []StackResource   `json:"Resources,omitempty"`
	Status          string            `json:"StackStatus"`
	StatusReason    string            `json:"StackStatusReason,omitempty"`
	Capabilities    []string          `json:"Capabilities,omitempty"`
	RoleARN         string            `json:"RoleARN,omitempty"`
	DisableRollback bool              `json:"DisableRollback,omitempty"`
	CreatedAt       time.Time         `json:"CreationTime"`
	UpdatedAt       *time.Time        `json:"LastUpdatedTimestamp,omitempty"`
	DeletedAt       *time.Time        `json:"DeletionTime,omitempty"`
	Metadata        map[string]string `json:"Metadata,omitempty"`
}

// Parameter is a key-value pair for a stack parameter.
type Parameter struct {
	Key   string `json:"ParameterKey"`
	Value string `json:"ParameterValue"`
}

// Tag is a key-value pair for tagging.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// Output is a stack output value.
type Output struct {
	Key         string `json:"OutputKey"`
	Value       string `json:"OutputValue"`
	Description string `json:"Description,omitempty"`
	ExportName  string `json:"ExportName,omitempty"`
}

// StackResource tracks a single provisioned resource within a stack.
type StackResource struct {
	LogicalID    string            `json:"LogicalResourceId"`
	PhysicalID   string            `json:"PhysicalResourceId,omitempty"`
	Type         string            `json:"ResourceType"`
	Status       string            `json:"ResourceStatus"`
	StatusReason string            `json:"ResourceStatusReason,omitempty"`
	Timestamp    time.Time         `json:"Timestamp"`
	Attributes   map[string]string `json:"Attributes,omitempty"`
	// PropertiesHash is a sha256 of the resolved Properties at provisioning
	// time. UpdateStack uses it to detect property drift and re-provision
	// only resources whose properties actually changed (e.g. Lambda code).
	PropertiesHash string         `json:"PropertiesHash,omitempty"`
	Properties     map[string]any `json:"-"`
	// DeletionPolicy / UpdateReplacePolicy are copied from the template at
	// provisioning time so DeleteStack and UpdateStack can honour Retain /
	// Snapshot semantics without re-parsing the template.
	DeletionPolicy      string `json:"DeletionPolicy,omitempty"`
	UpdateReplacePolicy string `json:"UpdateReplacePolicy,omitempty"`
}

// shouldRetainOnDelete reports whether DeleteStack should leave this resource
// in place. Snapshot is treated as Retain because Overcast does not snapshot.
func (r *StackResource) shouldRetainOnDelete() bool {
	switch r.DeletionPolicy {
	case "Retain", "Snapshot":
		return true
	}
	return false
}

// shouldRetainOnReplace reports whether an UpdateStack replacement should
// orphan the old resource (skip its delete) instead of deleting it.
func (r *StackResource) shouldRetainOnReplace() bool {
	switch r.UpdateReplacePolicy {
	case "Retain", "Snapshot":
		return true
	}
	return false
}

// StackEvent is an immutable record of a lifecycle state transition.
// Events are appended as provisioning progresses and are never mutated.
// The order in which events are appended matches the order AWS emits them;
// DescribeStackEvents returns them newest-first.
type StackEvent struct {
	EventID              string    `json:"EventId"`
	StackID              string    `json:"StackId"`
	StackName            string    `json:"StackName"`
	LogicalResourceID    string    `json:"LogicalResourceId"`
	PhysicalResourceID   string    `json:"PhysicalResourceId,omitempty"`
	ResourceType         string    `json:"ResourceType"`
	ResourceStatus       string    `json:"ResourceStatus"`
	ResourceStatusReason string    `json:"ResourceStatusReason,omitempty"`
	Timestamp            time.Time `json:"Timestamp"`
}

// ── Change Set ─────────────────────────────────────────────────────────────

// ChangeSet represents a CloudFormation change set.
type ChangeSet struct {
	ChangeSetName   string      `json:"ChangeSetName"`
	ChangeSetID     string      `json:"ChangeSetId"`
	StackID         string      `json:"StackId"`
	StackName       string      `json:"StackName"`
	TemplateBody    string      `json:"TemplateBody"`
	Parameters      []Parameter `json:"Parameters,omitempty"`
	Tags            []Tag       `json:"Tags,omitempty"`
	Capabilities    []string    `json:"Capabilities,omitempty"`
	Status          string      `json:"Status"`
	StatusReason    string      `json:"StatusReason,omitempty"`
	ChangeSetType   string      `json:"ChangeSetType"` // CREATE or UPDATE
	Changes         []Change    `json:"Changes,omitempty"`
	CreatedAt       time.Time   `json:"CreationTime"`
	ExecutionStatus string      `json:"ExecutionStatus"`
}

// Change describes a single resource change in a change set.
type Change struct {
	Type           string         `json:"Type"` // always "Resource"
	ResourceChange ResourceChange `json:"ResourceChange"`
}

// ResourceChange describes how a resource will be modified.
type ResourceChange struct {
	Action             string `json:"Action"` // Add, Modify, Remove
	LogicalResourceID  string `json:"LogicalResourceId"`
	PhysicalResourceID string `json:"PhysicalResourceId,omitempty"`
	ResourceType       string `json:"ResourceType"`
	Replacement        string `json:"Replacement,omitempty"` // True, False, Conditional
}

// ── Stack statuses ─────────────────────────────────────────────────────────

const (
	StatusCreateInProgress   = "CREATE_IN_PROGRESS"
	StatusCreateComplete     = "CREATE_COMPLETE"
	StatusCreateFailed       = "CREATE_FAILED"
	StatusUpdateInProgress   = "UPDATE_IN_PROGRESS"
	StatusUpdateComplete     = "UPDATE_COMPLETE"
	StatusUpdateFailed       = "UPDATE_FAILED"
	StatusDeleteInProgress   = "DELETE_IN_PROGRESS"
	StatusDeleteComplete     = "DELETE_COMPLETE"
	StatusDeleteFailed       = "DELETE_FAILED"
	StatusRollbackInProgress = "ROLLBACK_IN_PROGRESS"
	StatusRollbackComplete   = "ROLLBACK_COMPLETE"
	StatusRollbackFailed     = "ROLLBACK_FAILED"

	StatusUpdateRollbackInProgress = "UPDATE_ROLLBACK_IN_PROGRESS"
	StatusUpdateRollbackComplete   = "UPDATE_ROLLBACK_COMPLETE"
	StatusUpdateRollbackFailed     = "UPDATE_ROLLBACK_FAILED"

	ChangeSetStatusCreateComplete = "CREATE_COMPLETE"
	ChangeSetStatusFailed         = "FAILED"

	ExecStatusAvailable         = "AVAILABLE"
	ExecStatusUnavailable       = "UNAVAILABLE"
	ExecStatusExecuteComplete   = "EXECUTE_COMPLETE"
	ExecStatusExecuteFailed     = "EXECUTE_FAILED"
	ExecStatusExecuteInProgress = "EXECUTE_IN_PROGRESS"

	ResourceCreateInProgress = "CREATE_IN_PROGRESS"
	ResourceCreateComplete   = "CREATE_COMPLETE"
	ResourceCreateFailed     = "CREATE_FAILED"
	ResourceUpdateInProgress = "UPDATE_IN_PROGRESS"
	ResourceUpdateComplete   = "UPDATE_COMPLETE"
	ResourceUpdateFailed     = "UPDATE_FAILED"
	ResourceDeleteInProgress = "DELETE_IN_PROGRESS"
	ResourceDeleteComplete   = "DELETE_COMPLETE"
	ResourceDeleteFailed     = "DELETE_FAILED"
	ResourceDeleteSkipped    = "DELETE_SKIPPED"
)

// ── Template model ─────────────────────────────────────────────────────────

// Template is a parsed CloudFormation template.
type Template struct {
	AWSTemplateFormatVersion string                       `json:"AWSTemplateFormatVersion"`
	Description              string                       `json:"Description"`
	Parameters               map[string]TemplateParameter `json:"Parameters"`
	Resources                map[string]TemplateResource  `json:"Resources"`
	Outputs                  map[string]TemplateOutput    `json:"Outputs"`
	Conditions               map[string]any               `json:"Conditions"`
	Mappings                 map[string]any               `json:"Mappings"`
}

// TemplateParameter describes a declared parameter.
type TemplateParameter struct {
	Type          string   `json:"Type"`
	Default       string   `json:"Default"`
	Description   string   `json:"Description"`
	AllowedValues []string `json:"AllowedValues"`
}

// TemplateResource describes a declared resource.
type TemplateResource struct {
	Type       string         `json:"Type"`
	Properties map[string]any `json:"Properties"`
	DependsOn  any            `json:"DependsOn"` // string or []string
	Condition  string         `json:"Condition"`
	// DeletionPolicy controls what happens on stack delete: "Delete" (default),
	// "Retain" (leave the resource in place), or "Snapshot" (treated as Retain
	// in Overcast — we don't snapshot).
	DeletionPolicy string `json:"DeletionPolicy,omitempty"`
	// UpdateReplacePolicy controls what happens when an UpdateStack requires
	// the resource to be replaced (delete + create). "Delete" (default),
	// "Retain" (orphan the old resource), or "Snapshot" (treated as Retain).
	UpdateReplacePolicy string `json:"UpdateReplacePolicy,omitempty"`
}

// TemplateOutput describes a declared output.
type TemplateOutput struct {
	Value       any    `json:"Value"`
	Description string `json:"Description"`
	Export      *struct {
		Name any `json:"Name"`
	} `json:"Export"`
	Condition string `json:"Condition"`
}
