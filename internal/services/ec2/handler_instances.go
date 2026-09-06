package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── XML response types ───────────────────────────────────────────────────────

type xmlRunInstancesResponse struct {
	XMLName       xml.Name      `xml:"RunInstancesResponse"`
	Xmlns         string        `xml:"xmlns,attr"`
	RequestID     string        `xml:"requestId"`
	ReservationID string        `xml:"reservationId"`
	OwnerID       string        `xml:"ownerId"`
	Instances     []xmlInstance `xml:"instancesSet>item"`
}

type xmlDescribeInstancesResponse struct {
	XMLName      xml.Name         `xml:"DescribeInstancesResponse"`
	Xmlns        string           `xml:"xmlns,attr"`
	RequestID    string           `xml:"requestId"`
	Reservations []xmlReservation `xml:"reservationSet>item"`
}

type xmlReservation struct {
	ReservationID string        `xml:"reservationId"`
	OwnerID       string        `xml:"ownerId"`
	Instances     []xmlInstance `xml:"instancesSet>item"`
}

type xmlTerminateInstancesResponse struct {
	XMLName   xml.Name             `xml:"TerminateInstancesResponse"`
	Xmlns     string               `xml:"xmlns,attr"`
	RequestID string               `xml:"requestId"`
	Instances []xmlStateChangeItem `xml:"instancesSet>item"`
}

type xmlStartInstancesResponse struct {
	XMLName   xml.Name             `xml:"StartInstancesResponse"`
	Xmlns     string               `xml:"xmlns,attr"`
	RequestID string               `xml:"requestId"`
	Instances []xmlStateChangeItem `xml:"instancesSet>item"`
}

type xmlStopInstancesResponse struct {
	XMLName   xml.Name             `xml:"StopInstancesResponse"`
	Xmlns     string               `xml:"xmlns,attr"`
	RequestID string               `xml:"requestId"`
	Instances []xmlStateChangeItem `xml:"instancesSet>item"`
}

type xmlInstance struct {
	InstanceID    string           `xml:"instanceId"`
	ImageID       string           `xml:"imageId"`
	InstanceState xmlInstanceState `xml:"instanceState"`
	InstanceType  string           `xml:"instanceType"`
	LaunchTime    string           `xml:"launchTime"`
	SubnetID      string           `xml:"subnetId,omitempty"`
	VpcID         string           `xml:"vpcId,omitempty"`
	PrivateIP     string           `xml:"privateIpAddress,omitempty"`
	Placement     xmlPlacement     `xml:"placement"`
	GroupSet      []xmlSGRef       `xml:"groupSet>item,omitempty"`
	TagSet        []xmlTag         `xml:"tagSet>item,omitempty"`
}

type xmlSGRef struct {
	GroupID   string `xml:"groupId"`
	GroupName string `xml:"groupName"`
}

type xmlInstanceState struct {
	Code int    `xml:"code"`
	Name string `xml:"name"`
}

type xmlPlacement struct {
	AvailabilityZone string `xml:"availabilityZone"`
}

type xmlTag struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type xmlStateChangeItem struct {
	InstanceID    string           `xml:"instanceId"`
	PreviousState xmlInstanceState `xml:"previousState"`
	CurrentState  xmlInstanceState `xml:"currentState"`
}

// ── RunInstances ─────────────────────────────────────────────────────────────

// launchAvailabilityZone resolves the zone a RunInstances launch lands in: the
// zone the named subnet sits in, else an explicit Placement.AvailabilityZone,
// else the region's first zone — and an error when the request names both and
// they disagree. It is called from both dispatch bodies — the legacy one below
// and the typed one in typed_logic.go — so the rule lives in one place rather
// than in two copies that can drift apart (#1722 was exactly that drift: the
// typed body stopped honouring the parameter Auto Scaling's zone-spreading
// reconciler sends on every launch).
//
// Per the RunInstances reference SubnetId is "The ID of the subnet to launch
// the instance into", and Placement.AvailabilityZone is optional — "If you
// specify neither one, Amazon EC2 automatically selects an Availability Zone
// for you" (API_Placement.html). A subnet belongs to exactly one zone, so
// naming a subnet is naming a zone, and an instance whose placement
// contradicts its own subnetId is not a shape real EC2 can produce (#1743).
//
// The conflict is not in the API reference — neither RunInstances nor the EC2
// error-code list documents an error for it — so the wire shape below is taken
// from a reported real-AWS response (hashicorp/terraform-provider-aws#13999,
// launching an instance whose availability_zone disagreed with its subnet):
//
//	InvalidParameterValue: Value (us-east-1c) for parameter availabilityZone is
//	invalid. Subnet 'subnet-aff9fe92' is in the availability zone us-east-1b
//	status code: 400
//
// The generic code matches the error list's description of InvalidParameterValue
// ("A value specified in a parameter is not valid ... The returned message
// provides an explanation of the error value"), and the message names both
// zones so a caller can see which half to change.
//
// subnet is nil when the request named none, or named one this store does not
// hold: an unknown subnet keeps the previous, permissive fallback rather than
// becoming a new failure mode.
func launchAvailabilityZone(region, requested string, subnet *Subnet) (string, *protocol.AWSError) {
	if subnet == nil || subnet.AvailabilityZone == "" {
		if requested != "" {
			return requested, nil
		}
		return region + "a", nil
	}
	if requested != "" && requested != subnet.AvailabilityZone {
		return "", ec2err("InvalidParameterValue", fmt.Sprintf(
			"Value (%s) for parameter availabilityZone is invalid. Subnet '%s' is in the availability zone %s",
			requested, subnet.SubnetID, subnet.AvailabilityZone), http.StatusBadRequest)
	}
	return subnet.AvailabilityZone, nil
}

// RunInstances launches one or more new EC2 instances.
func (h *Handler) RunInstances(w http.ResponseWriter, r *http.Request) {
	// A launch template supplies whatever the request leaves out. Resolving it
	// first is what lets ImageId be required *after* the merge, as on AWS: a
	// template carrying an AMI makes the parameter optional.
	overlay, aerr := h.launchTemplateOverlayFor(r.Context(), launchTemplateRefFromForm(r, "LaunchTemplate."))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	imageID := applyString(r.FormValue("ImageId"), overlay.imageID)
	if imageID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "ImageId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	instanceType := applyString(r.FormValue("InstanceType"), overlay.instanceType)
	if instanceType == "" {
		instanceType = "t3.micro"
	}

	minCount := formInt(r, "MinCount", 1)
	maxCount := formInt(r, "MaxCount", minCount)
	subnetID := applyString(r.FormValue("SubnetId"), overlay.subnetID)
	securityGroups := applyList(parseIndexedParam(r, "SecurityGroupId"), overlay.securityGroupIDs)
	tags := applyTags(parseTagSpecifications(r, "instance"), overlay.instanceTags())
	// Create-time tags are checked before anything is launched, as on AWS: a
	// rejected tag must fail the call rather than leave instances running.
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	// Resolve SG names for the response.
	sgRefs := make([]InstanceSG, 0, len(securityGroups))
	for _, sgID := range securityGroups {
		sg, _ := h.store.getSecurityGroup(r.Context(), sgID)
		name := ""
		if sg != nil {
			name = sg.GroupName
		}
		sgRefs = append(sgRefs, InstanceSG{GroupID: sgID, GroupName: name})
	}

	now := h.clk.Now().UTC().Format(time.RFC3339)
	// The subnet is read once and used twice: it carries the VPC whose network
	// status gates the launch, and it pins the zone the instance lands in.
	var subnet *Subnet
	resolvedVpcID := ""
	if subnetID != "" {
		if sub, aerr := h.store.getSubnet(r.Context(), subnetID); aerr == nil {
			subnet = sub
			resolvedVpcID = sub.VpcID
			if vpc, aerr := h.store.getVPC(r.Context(), sub.VpcID); aerr == nil {
				ns := vpc.NetworkStatus
				if ns == "" {
					ns = vpcNetworkStatusOK
				}
				if ns == vpcNetworkStatusConflict {
					protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
						Code:       "InvalidVpc.NetworkStatus",
						Message:    fmt.Sprintf("VPC %s has network status %q: cannot launch instances", sub.VpcID, ns),
						HTTPStatus: http.StatusBadRequest,
					})
					return
				}
			}
		}
	}
	az, aerr := launchAvailabilityZone(h.cfg.Region, r.FormValue("Placement.AvailabilityZone"), subnet)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	instances := make([]xmlInstance, 0, maxCount)
	for i := 0; i < maxCount; i++ {
		instID := fmt.Sprintf("i-%s", shortID())
		apiPrivateIP, realPrivateIP, vpcID := h.allocatePrivateIPForSubnet(r.Context(), subnetID)
		if resolvedVpcID != "" {
			vpcID = resolvedVpcID
		}

		inst := &Instance{
			InstanceID:       instID,
			ImageID:          imageID,
			InstanceType:     instanceType,
			State:            InstanceState{Code: 0, Name: "pending"},
			LaunchTime:       now,
			SubnetID:         subnetID,
			PrivateIPAddress: realPrivateIP,
			SecurityGroups:   sgRefs,
			Placement:        Placement{AvailabilityZone: az},
			VpcID:            vpcID,
		}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		// Create-time tags go to the tag store, the same place CreateTags
		// writes, so a later describe sees both without reading two sources.
		if aerr := h.putResourceTags(r.Context(), instID, tags); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		// Schedule pending → running transition.  Scheduler runs 0-delay
		// callbacks synchronously with a real clock.
		id := instID // capture for closure
		h.scheduler.AfterScoped(h.store.region(r.Context()), id, "start", 0, func(ctx context.Context) {
			got, aerr := h.store.getInstance(ctx, id)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 { // still pending
				got.State = InstanceState{Code: 16, Name: "running"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r.Context(), events.EC2InstanceLaunched, events.ResourcePayload{Name: instID})

		xmlSGs := make([]xmlSGRef, 0, len(sgRefs))
		for _, sg := range sgRefs {
			xmlSGs = append(xmlSGs, xmlSGRef(sg))
		}

		instances = append(instances, xmlInstance{
			InstanceID:    instID,
			ImageID:       imageID,
			InstanceState: xmlInstanceState{Code: 0, Name: "pending"},
			InstanceType:  instanceType,
			LaunchTime:    now,
			SubnetID:      subnetID,
			VpcID:         inst.VpcID,
			PrivateIP:     apiPrivateIP,
			Placement:     xmlPlacement{AvailabilityZone: az},
			GroupSet:      xmlSGs,
			TagSet:        xmlTagsOf(sortTags(tags)),
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlRunInstancesResponse{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(r.Context()),
		ReservationID: fmt.Sprintf("r-%s", shortID()),
		OwnerID:       h.cfg.AccountID,
		Instances:     instances,
	})
}

// ── DescribeInstances ────────────────────────────────────────────────────────

// DescribeInstances returns instances, optionally filtered by ID or state.
func (h *Handler) DescribeInstances(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeInstances(r.Context(), requestQuery(r, "InstanceId"))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeInstances(ctx context.Context, q describeQuery) (*xmlDescribeInstancesResponse, *protocol.AWSError) {
	filters, aerr := instanceFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}
	requested := q.ids

	all, aerr := h.store.listInstances(ctx)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := resolveIDs(instanceIDScope, requested, all, func(inst *Instance) string { return inst.InstanceID }); aerr != nil {
		return nil, aerr
	}

	tagsView, aerr := h.tagViewFor(ctx, q.filters, true)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlInstance, 0, len(all))
	for _, inst := range all {
		if !requested.has(inst.InstanceID) || !filters.matches(inst) {
			continue
		}
		tags, ok := tagsView.keep(inst.InstanceID)
		if !ok {
			continue
		}
		xmlTags := xmlTagsOf(tags)
		xmlSGs := make([]xmlSGRef, 0, len(inst.SecurityGroups))
		for _, sg := range inst.SecurityGroups {
			xmlSGs = append(xmlSGs, xmlSGRef(sg))
		}
		items = append(items, xmlInstance{
			InstanceID:    inst.InstanceID,
			ImageID:       inst.ImageID,
			InstanceState: xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name},
			InstanceType:  inst.InstanceType,
			LaunchTime:    inst.LaunchTime,
			SubnetID:      inst.SubnetID,
			VpcID:         inst.VpcID,
			PrivateIP:     h.privateIPForAPI(ctx, inst.VpcID, inst.PrivateIPAddress),
			Placement:     xmlPlacement{AvailabilityZone: inst.Placement.AvailabilityZone},
			GroupSet:      xmlSGs,
			TagSet:        xmlTags,
		})
	}

	var reservations []xmlReservation
	if len(items) > 0 {
		reservations = []xmlReservation{
			{
				ReservationID: fmt.Sprintf("r-%s", shortID()),
				OwnerID:       h.cfg.AccountID,
				Instances:     items,
			},
		}
	}

	return &xmlDescribeInstancesResponse{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(ctx),
		Reservations: reservations,
	}, nil
}

// ── TerminateInstances ───────────────────────────────────────────────────────

// TerminateInstances terminates one or more instances.
func (h *Handler) TerminateInstances(w http.ResponseWriter, r *http.Request) {
	ids := parseIndexedParam(r, "InstanceId")
	if len(ids) == 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "InstanceId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	changes := make([]xmlStateChangeItem, 0, len(ids))
	for _, id := range ids {
		inst, aerr := h.store.getInstance(r.Context(), id)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		prev := xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 32, Name: "shutting-down"}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		// Schedule async shutting-down → terminated transition.
		instID := id // capture
		h.scheduler.AfterScoped(h.store.region(r.Context()), instID, "terminate", 500*time.Millisecond, func(ctx context.Context) {
			got, aerr := h.store.getInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 32 {
				got.State = InstanceState{Code: 48, Name: "terminated"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r.Context(), events.EC2InstanceTerminated, events.ResourcePayload{Name: id})

		changes = append(changes, xmlStateChangeItem{
			InstanceID:    id,
			PreviousState: prev,
			CurrentState:  xmlInstanceState{Code: 32, Name: "shutting-down"},
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlTerminateInstancesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Instances: changes,
	})
}

// ── StopInstances ────────────────────────────────────────────────────────────

// StopInstances stops one or more running instances.
func (h *Handler) StopInstances(w http.ResponseWriter, r *http.Request) {
	ids := parseIndexedParam(r, "InstanceId")
	if len(ids) == 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "InstanceId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	changes := make([]xmlStateChangeItem, 0, len(ids))
	for _, id := range ids {
		inst, aerr := h.store.getInstance(r.Context(), id)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		if inst.State.Code != 16 && inst.State.Code != 0 {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code:       "IncorrectInstanceState",
				Message:    fmt.Sprintf("Instance %s is not in the 'running' state", id),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}

		prev := xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 64, Name: "stopping"}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		// Schedule stopping → stopped transition.  Scheduler runs 0-delay
		// callbacks synchronously with a real clock.
		instID := id
		h.scheduler.AfterScoped(h.store.region(r.Context()), instID, "stop", 0, func(ctx context.Context) {
			got, aerr := h.store.getInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 64 {
				got.State = InstanceState{Code: 80, Name: "stopped"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r.Context(), events.EC2InstanceStopped, events.ResourcePayload{Name: id})

		changes = append(changes, xmlStateChangeItem{
			InstanceID:    id,
			PreviousState: prev,
			CurrentState:  xmlInstanceState{Code: 64, Name: "stopping"},
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlStopInstancesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Instances: changes,
	})
}

// ── StartInstances ───────────────────────────────────────────────────────────

// StartInstances starts one or more stopped instances.
func (h *Handler) StartInstances(w http.ResponseWriter, r *http.Request) {
	ids := parseIndexedParam(r, "InstanceId")
	if len(ids) == 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "InstanceId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	changes := make([]xmlStateChangeItem, 0, len(ids))
	for _, id := range ids {
		inst, aerr := h.store.getInstance(r.Context(), id)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		if inst.State.Code != 80 {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code:       "IncorrectInstanceState",
				Message:    fmt.Sprintf("Instance %s is not in the 'stopped' state", id),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}

		prev := xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 0, Name: "pending"}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}

		// Schedule pending → running transition.  Scheduler runs 0-delay
		// callbacks synchronously with a real clock.
		instID := id
		h.scheduler.AfterScoped(h.store.region(r.Context()), instID, "start", 0, func(ctx context.Context) {
			got, aerr := h.store.getInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 {
				got.State = InstanceState{Code: 16, Name: "running"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r.Context(), events.EC2InstanceStarted, events.ResourcePayload{Name: id})

		changes = append(changes, xmlStateChangeItem{
			InstanceID:    id,
			PreviousState: prev,
			CurrentState:  xmlInstanceState{Code: 0, Name: "pending"},
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlStartInstancesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Instances: changes,
	})
}

// ── Form-parsing helpers ─────────────────────────────────────────────────────

// formInt reads a form value as an integer with a fallback default.
func formInt(r *http.Request, key string, def int) int {
	v := r.FormValue(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// parseIndexedParam collects form values like "Prefix.1", "Prefix.2", etc.
func parseIndexedParam(r *http.Request, prefix string) []string {
	var result []string
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s.%d", prefix, i))
		if v == "" {
			break
		}
		result = append(result, v)
	}
	return result
}

// ── ModifyInstanceAttribute ───────────────────────────────────────────────────

type xmlModifyInstanceAttributeResponse struct {
	XMLName   xml.Name `xml:"ModifyInstanceAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// ModifyInstanceAttribute handles Action=ModifyInstanceAttribute.
// Supports InstanceType.Value; all other attributes are accepted and ignored.
func (h *Handler) ModifyInstanceAttribute(w http.ResponseWriter, r *http.Request) {
	instanceID := r.FormValue("InstanceId")
	if instanceID == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "The request must contain InstanceId.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	inst, aerr := h.store.getInstance(r.Context(), instanceID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	if newType := r.FormValue("InstanceType.Value"); newType != "" {
		inst.InstanceType = newType
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyInstanceAttributeResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}
