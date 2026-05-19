package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
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

// RunInstances launches one or more new EC2 instances.
func (h *Handler) RunInstances(w http.ResponseWriter, r *http.Request) {
	imageID := r.FormValue("ImageId")
	if imageID == "" {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "ImageId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	instanceType := r.FormValue("InstanceType")
	if instanceType == "" {
		instanceType = "t3.micro"
	}

	minCount := formInt(r, "MinCount", 1)
	maxCount := formInt(r, "MaxCount", minCount)
	subnetID := r.FormValue("SubnetId")
	securityGroups := parseIndexedParam(r, "SecurityGroupId")
	tags := parseTagSpecifications(r)

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
	az := h.cfg.Region + "a"
	resolvedVpcID := ""
	if subnetID != "" {
		if sub, aerr := h.store.getSubnet(r.Context(), subnetID); aerr == nil {
			resolvedVpcID = sub.VpcID
			if vpc, aerr := h.store.getVPC(r.Context(), sub.VpcID); aerr == nil {
				ns := vpc.NetworkStatus
				if ns == "" {
					ns = vpcNetworkStatusOK
				}
				if ns == vpcNetworkStatusConflict {
					protocol.WriteXMLError(w, r, &protocol.AWSError{
						Code:       "InvalidVpc.NetworkStatus",
						Message:    fmt.Sprintf("VPC %s has network status %q: cannot launch instances", sub.VpcID, ns),
						HTTPStatus: http.StatusBadRequest,
					})
					return
				}
			}
		}
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
			Tags:             tags,
			VpcID:            vpcID,
		}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		// Schedule pending → running transition.  Scheduler runs 0-delay
		// callbacks synchronously with a real clock.
		id := instID // capture for closure
		h.scheduler.After(id+":start", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getInstance(ctx, id)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 { // still pending
				got.State = InstanceState{Code: 16, Name: "running"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r, events.EC2InstanceLaunched, events.ResourcePayload{Name: instID})

		xmlTags := make([]xmlTag, 0, len(tags))
		for _, tag := range tags {
			xmlTags = append(xmlTags, xmlTag(tag))
		}

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
			TagSet:        xmlTags,
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlRunInstancesResponse{
		Xmlns:         ec2XMLNS,
		RequestID:     protocol.RequestIDFromContext(r.Context()),
		ReservationID: fmt.Sprintf("r-%s", shortID()),
		OwnerID:       "123456789012",
		Instances:     instances,
	})
}

// ── DescribeInstances ────────────────────────────────────────────────────────

// DescribeInstances returns instances, optionally filtered by ID or state.
func (h *Handler) DescribeInstances(w http.ResponseWriter, r *http.Request) {
	filterIDs := parseIndexedParam(r, "InstanceId")
	filterIDSet := make(map[string]bool, len(filterIDs))
	for _, id := range filterIDs {
		filterIDSet[id] = true
	}

	// Parse Filter.N.Name / Filter.N.Value.M parameters.
	stateFilter := parseFilterValues(r, "instance-state-name")

	all, aerr := h.store.listInstances(r.Context())
	if aerr != nil {
		protocol.WriteXMLError(w, r, aerr)
		return
	}

	items := make([]xmlInstance, 0, len(all))
	for _, inst := range all {
		if len(filterIDSet) > 0 && !filterIDSet[inst.InstanceID] {
			continue
		}
		if len(stateFilter) > 0 && !stateFilter[inst.State.Name] {
			continue
		}
		xmlTags := make([]xmlTag, 0, len(inst.Tags))
		for _, tag := range inst.Tags {
			xmlTags = append(xmlTags, xmlTag(tag))
		}
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
			PrivateIP:     h.privateIPForAPI(r.Context(), inst.VpcID, inst.PrivateIPAddress),
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
				OwnerID:       "123456789012",
				Instances:     items,
			},
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeInstancesResponse{
		Xmlns:        ec2XMLNS,
		RequestID:    protocol.RequestIDFromContext(r.Context()),
		Reservations: reservations,
	})
}

// ── TerminateInstances ───────────────────────────────────────────────────────

// TerminateInstances terminates one or more instances.
func (h *Handler) TerminateInstances(w http.ResponseWriter, r *http.Request) {
	ids := parseIndexedParam(r, "InstanceId")
	if len(ids) == 0 {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
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
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		prev := xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 32, Name: "shutting-down"}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		// Schedule async shutting-down → terminated transition.
		instID := id // capture
		h.scheduler.After(instID+":terminate", 500*time.Millisecond, func() {
			ctx := context.Background()
			got, aerr := h.store.getInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 32 {
				got.State = InstanceState{Code: 48, Name: "terminated"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r, events.EC2InstanceTerminated, events.ResourcePayload{Name: id})

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
		protocol.WriteXMLError(w, r, &protocol.AWSError{
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
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		if inst.State.Code != 16 && inst.State.Code != 0 {
			protocol.WriteXMLError(w, r, &protocol.AWSError{
				Code:       "IncorrectInstanceState",
				Message:    fmt.Sprintf("Instance %s is not in the 'running' state", id),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}

		prev := xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 64, Name: "stopping"}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		// Schedule stopping → stopped transition.  Scheduler runs 0-delay
		// callbacks synchronously with a real clock.
		instID := id
		h.scheduler.After(instID+":stop", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 64 {
				got.State = InstanceState{Code: 80, Name: "stopped"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r, events.EC2InstanceStopped, events.ResourcePayload{Name: id})

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
		protocol.WriteXMLError(w, r, &protocol.AWSError{
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
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		if inst.State.Code != 80 {
			protocol.WriteXMLError(w, r, &protocol.AWSError{
				Code:       "IncorrectInstanceState",
				Message:    fmt.Sprintf("Instance %s is not in the 'stopped' state", id),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}

		prev := xmlInstanceState{Code: inst.State.Code, Name: inst.State.Name}
		inst.State = InstanceState{Code: 0, Name: "pending"}
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteXMLError(w, r, aerr)
			return
		}

		// Schedule pending → running transition.  Scheduler runs 0-delay
		// callbacks synchronously with a real clock.
		instID := id
		h.scheduler.After(instID+":start", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getInstance(ctx, instID)
			if aerr != nil {
				return
			}
			if got.State.Code == 0 {
				got.State = InstanceState{Code: 16, Name: "running"}
				h.store.putInstance(ctx, got) //nolint:errcheck
			}
		})

		h.publish(r, events.EC2InstanceStarted, events.ResourcePayload{Name: id})

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

// parseTagSpecifications parses TagSpecification.N.Tag.M.{Key,Value} form params
// for ResourceType=instance.
func parseTagSpecifications(r *http.Request) []Tag {
	var tags []Tag
	for i := 1; ; i++ {
		rt := r.FormValue(fmt.Sprintf("TagSpecification.%d.ResourceType", i))
		if rt == "" {
			break
		}
		if rt != "instance" {
			continue
		}
		for j := 1; ; j++ {
			key := r.FormValue(fmt.Sprintf("TagSpecification.%d.Tag.%d.Key", i, j))
			val := r.FormValue(fmt.Sprintf("TagSpecification.%d.Tag.%d.Value", i, j))
			if key == "" {
				break
			}
			tags = append(tags, Tag{Key: key, Value: val})
		}
	}
	return tags
}

// parseFilterValues extracts values for a named filter from Filter.N.Name / Filter.N.Value.M params.
func parseFilterValues(r *http.Request, filterName string) map[string]bool {
	result := map[string]bool{}
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("Filter.%d.Name", i))
		if name == "" {
			break
		}
		if !strings.EqualFold(name, filterName) {
			continue
		}
		for j := 1; ; j++ {
			val := r.FormValue(fmt.Sprintf("Filter.%d.Value.%d", i, j))
			if val == "" {
				break
			}
			result[val] = true
		}
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
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "The request must contain InstanceId.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	inst, aerr := h.store.getInstance(r.Context(), instanceID)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if newType := r.FormValue("InstanceType.Value"); newType != "" {
		inst.InstanceType = newType
		if aerr := h.store.putInstance(r.Context(), inst); aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyInstanceAttributeResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}
