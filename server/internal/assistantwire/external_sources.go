package assistantwire

import (
	jsonv2 "encoding/json/v2"
	"errors"
)

const (
	ExternalBindingsPath = "/api/v1/assistant/external-source-bindings"
	ExternalPoliciesPath = "/api/v1/assistant/external-source-policies"

	ExternalBindingsListRoute    = "GET " + ExternalBindingsPath
	ExternalBindingsCreateRoute  = "POST " + ExternalBindingsPath
	ExternalBindingsUpdateRoute  = "PUT " + ExternalBindingsPath + "/{id}"
	ExternalBindingsDeleteRoute  = "DELETE " + ExternalBindingsPath + "/{id}"
	ExternalBindingsLookupRoute  = "GET " + ExternalBindingsPath + "/lookup"
	ExternalBindingsSummaryRoute = "GET " + ExternalBindingsPath + "/summary"
	ExternalPoliciesReadRoute    = "GET " + ExternalPoliciesPath
	ExternalPoliciesWriteRoute   = "PUT " + ExternalPoliciesPath
	ExternalPolicyEffectiveRoute = "GET " + ExternalPoliciesPath + "/effective"
)

func init() {
	Routes = append(Routes,
		ExternalBindingsListRoute, ExternalBindingsCreateRoute, ExternalBindingsUpdateRoute, ExternalBindingsDeleteRoute,
		ExternalBindingsLookupRoute, ExternalBindingsSummaryRoute,
		ExternalPoliciesReadRoute, ExternalPoliciesWriteRoute, ExternalPolicyEffectiveRoute)
}

// ExternalRef identifies a node in an external source. A channel is the provider's
// default channel when Channel is empty. Container and Item are optional path segments.
type ExternalRef struct {
	Provider  ExternalProvider  `json:"provider"`
	Channel   ExternalChannelID `json:"channel,omitempty"`
	Container ExternalRefNode   `json:"container,omitzero"`
	Item      ExternalRefNode   `json:"item,omitzero"`
}

type ExternalProvider string
type ExternalChannelID string
type ExternalRefKind string
type ExternalRefID string

const (
	ExternalProviderGitHub ExternalProvider = "github"
	ExternalKindRepository ExternalRefKind  = "repository"
	ExternalKindIssue      ExternalRefKind  = "issue"
	ExternalKindPull       ExternalRefKind  = "pull_request"
)

type ExternalRefNode struct {
	Kind ExternalRefKind `json:"kind"`
	ID   ExternalRefID   `json:"id"`
}

// UnmarshalJSON distinguishes omitted nodes from explicit empty nodes so a malformed item cannot select a parent.
func (n *ExternalRefNode) UnmarshalJSON(data []byte) error {
	type node ExternalRefNode
	var in node
	if err := jsonv2.Unmarshal(data, &in, jsonv2.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if in == (node{}) {
		return errors.New("empty external reference node")
	}
	*n = ExternalRefNode(in)
	return nil
}

type ExternalTargetKind string
type ExternalTargetID string
type ExternalBindingID string

const (
	ExternalTargetDirection ExternalTargetKind = "direction"
	ExternalTargetTodo      ExternalTargetKind = "todo"
)

type ExternalForwardMode string
type ExternalActionRole string
type ExternalNewItemPolicy string

// ExternalPolicy is a sparse override. A nil field inherits, and an empty PreFilter
// explicitly clears the inherited filter.
type ExternalPolicy struct {
	Mode                  *ExternalForwardMode   `json:"mode,omitempty"`
	DigestIntervalMinutes *int                   `json:"digest_interval_minutes,omitempty"`
	ActionRole            *ExternalActionRole    `json:"action_role,omitempty"`
	NewItemPolicy         *ExternalNewItemPolicy `json:"new_item_policy,omitempty"`
	PreFilter             *ExternalPreFilter     `json:"pre_filter,omitempty"`
}

type ExternalPreFilter struct {
	Keywords []string `json:"keywords,omitempty"`
	Mentions []string `json:"mentions,omitempty"`
	Senders  []string `json:"senders,omitempty"`
}

type ExternalScopedPolicy struct {
	ExternalRef ExternalRef    `json:"external_ref"`
	Policy      ExternalPolicy `json:"policy"`
}

type ExternalPolicySource string

const (
	ExternalPolicyDefault   ExternalPolicySource = "default"
	ExternalPolicyChannel   ExternalPolicySource = "channel"
	ExternalPolicyContainer ExternalPolicySource = "container"
	ExternalPolicyItem      ExternalPolicySource = "item"
	ExternalPolicyBinding   ExternalPolicySource = "binding"
)

type ExternalPolicyOrigin struct {
	Source      ExternalPolicySource `json:"source"`
	ExternalRef *ExternalRef         `json:"external_ref,omitempty"`
	BindingID   ExternalBindingID    `json:"binding_id,omitempty"`
}

type ExternalPolicyProvenance struct {
	Mode                  ExternalPolicyOrigin  `json:"mode"`
	DigestIntervalMinutes *ExternalPolicyOrigin `json:"digest_interval_minutes,omitempty"`
	ActionRole            ExternalPolicyOrigin  `json:"action_role"`
	NewItemPolicy         ExternalPolicyOrigin  `json:"new_item_policy"`
	PreFilter             ExternalPolicyOrigin  `json:"pre_filter"`
}

type ExternalEffectivePolicy struct {
	ExternalRef ExternalRef              `json:"external_ref"`
	Policy      ExternalPolicy           `json:"policy"`
	Provenance  ExternalPolicyProvenance `json:"provenance"`
}

type ExternalBindingInput struct {
	ExternalRef ExternalRef        `json:"external_ref"`
	TargetKind  ExternalTargetKind `json:"target_kind"`
	TargetID    ExternalTargetID   `json:"target_id"`
	Policy      ExternalPolicy     `json:"policy"`
}

type ExternalBinding struct {
	ID ExternalBindingID `json:"id"`
	ExternalBindingInput
}

type ExternalBindingList struct {
	Bindings []ExternalBinding `json:"bindings"`
}

type ExternalBindingSource string
type ExternalRepositoryRole string

const (
	ExternalBindingExplicit       ExternalBindingSource = "explicit"
	ExternalBindingRepositoryLink ExternalBindingSource = "repository_link"
)

type ExternalBindingMatch struct {
	ExternalRef    ExternalRef            `json:"external_ref"`
	TargetKind     ExternalTargetKind     `json:"target_kind"`
	TargetID       ExternalTargetID       `json:"target_id"`
	Source         ExternalBindingSource  `json:"source"`
	BindingID      ExternalBindingID      `json:"binding_id,omitempty"`
	RepositoryRole ExternalRepositoryRole `json:"repository_role,omitempty"`
}

type ExternalBindingLookup struct {
	Path    []ExternalRef          `json:"path"`
	Matches []ExternalBindingMatch `json:"matches"`
}

type ExternalSourceSummary struct {
	Bindings []ExternalBindingMatch `json:"bindings"`
}
