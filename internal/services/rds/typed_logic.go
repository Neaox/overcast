package rds

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// ─── Request types ──────────────────────────────────────────────────────────

type createDBInstanceReq struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
	Engine               string `json:"Engine"`
	MasterUsername       string `json:"MasterUsername"`
	MasterUserPassword   string `json:"MasterUserPassword"`
	DBInstanceClass      string `json:"DBInstanceClass"`
	EngineVersion        string `json:"EngineVersion"`
	AllocatedStorage     int    `json:"AllocatedStorage"`
	Port                 int    `json:"Port"`
	StorageType          string `json:"StorageType"`
	MultiAZ              bool   `json:"MultiAZ"`
	DBName               string `json:"DBName"`
	DBClusterIdentifier  string `json:"DBClusterIdentifier"`
	DBSubnetGroupName    string `json:"DBSubnetGroupName"`
}

type describeDBInstancesReq struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
}

type deleteDBInstanceReq struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
}

type describeDBEngineVersionsReq struct {
	Engine string `json:"Engine"`
}

type stopDBInstanceReq struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
}

type startDBInstanceReq struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
}

type modifyDBInstanceReq struct {
	DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
	DBInstanceClass      string `json:"DBInstanceClass"`
	AllocatedStorage     int    `json:"AllocatedStorage"`
	EngineVersion        string `json:"EngineVersion"`
	MultiAZ              bool   `json:"MultiAZ"`
	StorageType          string `json:"StorageType"`
}

type createDBSubnetGroupReq struct {
	DBSubnetGroupName        string   `json:"DBSubnetGroupName"`
	DBSubnetGroupDescription string   `json:"DBSubnetGroupDescription"`
	SubnetIds                []string `json:"SubnetIds"`
	VpcId                    string   `json:"VpcId"`
}

type deleteDBSubnetGroupReq struct {
	DBSubnetGroupName string `json:"DBSubnetGroupName"`
}

type describeDBSubnetGroupsReq struct {
	DBSubnetGroupName string `json:"DBSubnetGroupName"`
}

type createDBParameterGroupReq struct {
	DBParameterGroupName   string `json:"DBParameterGroupName"`
	DBParameterGroupFamily string `json:"DBParameterGroupFamily"`
	Description            string `json:"Description"`
}

type deleteDBParameterGroupReq struct {
	DBParameterGroupName string `json:"DBParameterGroupName"`
}

type describeDBParameterGroupsReq struct {
	DBParameterGroupName string `json:"DBParameterGroupName"`
}

type describeOrderableDBInstanceOptionsReq struct {
	Engine string `json:"Engine"`
}

type createDBClusterReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
	Engine              string `json:"Engine"`
	MasterUsername      string `json:"MasterUsername"`
	MasterUserPassword  string `json:"MasterUserPassword"`
	EngineVersion       string `json:"EngineVersion"`
	StorageType         string `json:"StorageType"`
	MultiAZ             bool   `json:"MultiAZ"`
	DatabaseName        string `json:"DatabaseName"`
}

type describeDBClustersReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
}

type deleteDBClusterReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
}

type modifyDBClusterReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
	EngineVersion       string `json:"EngineVersion"`
}

type startDBClusterReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
}

type stopDBClusterReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
}

// ─── Typed handler functions ────────────────────────────────────────────────

// --- CreateDBInstance ---

func (h *Handler) createDBInstanceTyped(ctx context.Context, req *createDBInstanceReq) (*xmlCreateDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	engine := req.Engine
	if engine == "" {
		return nil, errInvalidParameterValue("Engine is required")
	}
	if !supportedEngines[engine] {
		return nil, errInvalidParameterValue("Engine must be one of: mysql, postgres, mariadb, aurora-mysql, aurora-postgresql")
	}

	masterUser := req.MasterUsername
	if masterUser == "" {
		return nil, errInvalidParameterValue("MasterUsername is required")
	}

	masterPass := req.MasterUserPassword
	if masterPass == "" {
		return nil, errInvalidParameterValue("MasterUserPassword is required")
	}

	if _, aerr := h.store.getDBInstance(ctx, id); aerr == nil {
		return nil, errDBInstanceAlreadyExists(id)
	}

	instanceClass := req.DBInstanceClass
	if instanceClass == "" {
		instanceClass = "db.t3.micro"
	}

	engineVersion := req.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersions[engine]
	}

	allocatedStorage := req.AllocatedStorage
	if allocatedStorage == 0 {
		allocatedStorage = 20
	}

	port := req.Port
	if port == 0 {
		port = defaultPorts[engine]
	}

	storageType := req.StorageType
	if storageType == "" {
		storageType = "gp2"
	}

	multiAZ := req.MultiAZ
	dbName := req.DBName
	clusterID := req.DBClusterIdentifier
	dbSubnetGroupName := req.DBSubnetGroupName
	vpcID := ""

	if clusterID != "" {
		if _, aerr := h.store.getDBCluster(ctx, clusterID); aerr != nil {
			return nil, aerr
		}
	}
	if dbSubnetGroupName != "" {
		sg, aerr := h.store.getDBSubnetGroup(ctx, dbSubnetGroupName)
		if aerr != nil {
			return nil, aerr
		}
		vpcID = sg.VpcId
		if h.vpcResolver != nil && vpcID != "" {
			switch status := h.vpcResolver.VPCNetworkStatus(ctx, vpcID); status {
			case "", "ok", "shared", "remapped":
			case "conflict", "unbacked":
				return nil, &protocol.AWSError{
					Code: "InvalidVPCNetworkStateFault", Message: "VPC '" + vpcID + "' is not launchable for DB instances (network status=" + status + ").",
					HTTPStatus: http.StatusBadRequest,
				}
			default:
				return nil, &protocol.AWSError{
					Code: "InvalidVPCNetworkStateFault", Message: "VPC '" + vpcID + "' is not launchable for DB instances (network status=" + status + ").",
					HTTPStatus: http.StatusBadRequest,
				}
			}
		}
	}

	region := h.store.region(ctx)
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "db:"+id)
	now := h.clk.Now().UTC().Format(time.RFC3339)

	endpoint := &Endpoint{
		Address: id + "." + region + ".rds." + h.cfg.ExternalHostname(),
		Port:    port,
	}

	inst := &DBInstance{
		DBInstanceIdentifier: id,
		DBInstanceClass:      instanceClass,
		Engine:               engine,
		EngineVersion:        engineVersion,
		DBInstanceStatus:     "creating",
		MasterUsername:       masterUser,
		MasterUserPassword:   masterPass,
		DBName:               dbName,
		AllocatedStorage:     allocatedStorage,
		Endpoint:             endpoint,
		DBInstanceArn:        arn,
		InstanceCreateTime:   now,
		MultiAZ:              multiAZ,
		StorageType:          storageType,
		Port:                 port,
		DBClusterIdentifier:  clusterID,
		DBSubnetGroupName:    dbSubnetGroupName,
		VpcID:                vpcID,
	}

	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		return nil, aerr
	}

	if h.dockerReady.Load() {
		if versions, ok := engineImages[inst.Engine]; ok {
			if image, ok := versions[inst.EngineVersion]; ok && h.puller != nil {
				h.puller.Prewarm(image)
			}
		}
		instID := id
		h.dockerWg.Add(1)
		go func() {
			defer h.dockerWg.Done()
			bgCtx := context.Background()
			got, aerr := h.store.getDBInstance(bgCtx, instID)
			if aerr != nil || got == nil {
				return
			}
			if err := h.startDBContainer(bgCtx, got); err != nil {
				h.log.Warn("failed to start Docker container for RDS instance — falling back to metadata-only",
					zap.String("instance", instID), zap.Error(err))
				return
			}
			if aerr := h.store.putDBInstance(bgCtx, got); aerr != nil {
				h.log.Warn("RDS: persist post-start instance",
					zap.String("instance", instID), zap.String("error", aerr.Message))
				return
			}
			h.scheduleHealthCheck(instID, got.Endpoint.Address, got.Endpoint.Port)
		}()
	}

	instID := id
	h.scheduler.After(instID+":available", 0, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBInstance(ctx, instID)
		if aerr != nil {
			return
		}
		if got.DBInstanceStatus == "creating" {
			got.DBInstanceStatus = "available"
			if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
				h.log.Warn("RDS: persist available instance", zap.String("instance", instID), zap.String("error", aerr.Message))
			}
		}
	})

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceCreated, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}

	if clusterID != "" {
		h.addInstanceToCluster(ctx, clusterID, id)
	}

	return &xmlCreateDBInstanceResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBInstanceResult{
			DBInstance: toXMLDBInstance(inst),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DescribeDBInstances ---

func (h *Handler) describeDBInstancesTyped(ctx context.Context, req *describeDBInstancesReq) (*xmlDescribeDBInstancesResponse, *protocol.AWSError) {
	filterID := req.DBInstanceIdentifier

	if filterID != "" {
		inst, aerr := h.store.getDBInstance(ctx, filterID)
		if aerr != nil {
			return nil, aerr
		}
		return &xmlDescribeDBInstancesResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBInstancesResult{
				DBInstances: xmlDBInstances{Items: []xmlDBInstance{toXMLDBInstance(inst)}},
			},
			ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
		}, nil
	}

	all, aerr := h.store.listDBInstances(ctx)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlDBInstance, 0, len(all))
	for _, inst := range all {
		items = append(items, toXMLDBInstance(inst))
	}

	return &xmlDescribeDBInstancesResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBInstancesResult{
			DBInstances: xmlDBInstances{Items: items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DeleteDBInstance ---

func (h *Handler) deleteDBInstanceTyped(ctx context.Context, req *deleteDBInstanceReq) (*xmlDeleteDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	containerID := inst.DockerContainerID
	hostPort := inst.HostPort

	inst.DBInstanceStatus = "deleting"
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceDeleted, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}

	resp := &xmlDeleteDBInstanceResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDeleteDBInstanceResult{
			DBInstance: toXMLDBInstance(inst),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}

	h.scheduler.Cancel(id + ":health")

	// Stop the container immediately (async, non-blocking).
	if h.gc != nil && containerID != "" {
		h.gc.StopNow(containerID)
		h.gc.ScheduleRemove(containerID)
	}
	if hostPort > 0 {
		if aerr := h.store.releasePort(ctx, hostPort); aerr != nil {
			h.log.Warn("RDS cleanup: release port", zap.String("instance", id), zap.Error(aerr))
		}
	}

	h.scheduler.After(id+":delete", 50*time.Millisecond, func() {
		bgCtx := context.Background()
		if aerr := h.store.deleteDBInstance(bgCtx, id); aerr != nil {
			h.log.Warn("failed to delete RDS instance record", zap.String("instance", id), zap.Error(aerr))
		}
	})

	return resp, nil
}

// --- DescribeDBEngineVersions ---

func (h *Handler) describeDBEngineVersionsTyped(ctx context.Context, req *describeDBEngineVersionsReq) (*xmlDescribeDBEngineVersionsResponse, *protocol.AWSError) {
	filterEngine := req.Engine

	items := make([]xmlDBEngineVersion, 0, len(allEngineVersions))
	for _, ev := range allEngineVersions {
		if filterEngine != "" && ev.Engine != filterEngine {
			continue
		}
		items = append(items, xmlDBEngineVersion(ev))
	}

	return &xmlDescribeDBEngineVersionsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBEngineVersionsResult{
			DBEngineVersions: xmlDBEngineVersions{Items: items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- StopDBInstance ---

func (h *Handler) stopDBInstanceTyped(ctx context.Context, req *stopDBInstanceReq) (*xmlStopDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	if inst.DBInstanceStatus != "available" {
		return nil, errInvalidDBInstanceState(id, "must be available to stop")
	}

	inst.DBInstanceStatus = "stopping"
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		return nil, aerr
	}

	if h.dockerReady.Load() && inst.DockerContainerID != "" {
		if h.gc != nil {
			h.gc.StopNow(inst.DockerContainerID)
		} else {
			_ = h.docker.StopContainer(ctx, inst.DockerContainerID, 10)
		}
	}

	instID := id
	h.scheduler.After(instID+":stopped", 0, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBInstance(ctx, instID)
		if aerr != nil {
			return
		}
		if got.DBInstanceStatus == "stopping" {
			got.DBInstanceStatus = "stopped"
			if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
				h.log.Warn("RDS: persist stopped instance", zap.String("instance", instID), zap.String("error", aerr.Message))
			}
		}
	})

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceStopped, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}

	return &xmlStopDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlStopDBInstanceResult{DBInstance: toXMLDBInstance(inst)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- StartDBInstance ---

func (h *Handler) startDBInstanceTyped(ctx context.Context, req *startDBInstanceReq) (*xmlStartDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	if inst.DBInstanceStatus != "stopped" {
		return nil, errInvalidDBInstanceState(id, "must be stopped to start")
	}

	inst.DBInstanceStatus = "starting"
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		return nil, aerr
	}

	if h.dockerReady.Load() && inst.DockerContainerID != "" {
		if err := h.docker.StartContainer(ctx, inst.DockerContainerID); err != nil {
			h.log.Warn("failed to start RDS container", zap.String("instance", id), zap.Error(err))
		}
		h.scheduleHealthCheck(id, inst.Endpoint.Address, inst.Endpoint.Port)
	} else {
		instID2 := id
		h.scheduler.After(instID2+":available", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getDBInstance(ctx, instID2)
			if aerr != nil {
				return
			}
			if got.DBInstanceStatus == "starting" {
				got.DBInstanceStatus = "available"
				if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
					h.log.Warn("RDS: persist started instance", zap.String("instance", instID2), zap.String("error", aerr.Message))
				}
			}
		})
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceStarted, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}

	return &xmlStartDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlStartDBInstanceResult{DBInstance: toXMLDBInstance(inst)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- ModifyDBInstance ---

func (h *Handler) modifyDBInstanceTyped(ctx context.Context, req *modifyDBInstanceReq) (*xmlModifyDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	if req.DBInstanceClass != "" {
		inst.DBInstanceClass = req.DBInstanceClass
	}
	if req.AllocatedStorage != 0 {
		inst.AllocatedStorage = req.AllocatedStorage
	}
	if req.EngineVersion != "" {
		if req.EngineVersion != inst.EngineVersion {
			h.log.Warn("EngineVersion change requested — restart would be needed in production",
				zap.String("instance", id), zap.String("from", inst.EngineVersion), zap.String("to", req.EngineVersion))
		}
		inst.EngineVersion = req.EngineVersion
	}
	if req.MultiAZ {
		inst.MultiAZ = true
	}
	if req.StorageType != "" {
		inst.StorageType = req.StorageType
	}

	prevStatus := inst.DBInstanceStatus
	inst.DBInstanceStatus = "modifying"
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		return nil, aerr
	}

	instID := id
	h.scheduler.After(instID+":modified", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBInstance(ctx, instID)
		if aerr != nil {
			return
		}
		if got.DBInstanceStatus == "modifying" {
			if prevStatus == "available" || prevStatus == "" {
				got.DBInstanceStatus = "available"
			} else {
				got.DBInstanceStatus = prevStatus
			}
			if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
				h.log.Warn("RDS: persist modified instance", zap.String("instance", instID), zap.String("error", aerr.Message))
			}
		}
	})

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceModified, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}

	return &xmlModifyDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlModifyDBInstanceResult{DBInstance: toXMLDBInstance(inst)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- CreateDBSubnetGroup ---

func (h *Handler) createDBSubnetGroupTyped(ctx context.Context, req *createDBSubnetGroupReq) (*xmlCreateDBSubnetGroupResponse, *protocol.AWSError) {
	name := req.DBSubnetGroupName
	if name == "" {
		return nil, errInvalidParameterValue("DBSubnetGroupName is required")
	}

	description := req.DBSubnetGroupDescription
	if description == "" {
		return nil, errInvalidParameterValue("DBSubnetGroupDescription is required")
	}

	if _, aerr := h.store.getDBSubnetGroup(ctx, name); aerr == nil {
		return nil, errDBSubnetGroupAlreadyExists(name)
	}

	subnetIds := req.SubnetIds
	if len(subnetIds) == 0 {
		return nil, errInvalidParameterValue("At least one SubnetId is required")
	}

	vpcId := req.VpcId
	if vpcId == "" && h.vpcResolver != nil && len(subnetIds) > 0 {
		vpcId = h.vpcResolver.VpcIDForSubnet(ctx, subnetIds[0])
	}
	if vpcId == "" {
		vpcId = "vpc-00000000"
	}

	region := h.store.region(ctx)
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "subgrp:"+name)

	sg := &DBSubnetGroup{
		DBSubnetGroupName:        name,
		DBSubnetGroupDescription: description,
		DBSubnetGroupArn:         arn,
		VpcId:                    vpcId,
		SubnetIds:                subnetIds,
		Status:                   "Complete",
	}

	if aerr := h.store.putDBSubnetGroup(ctx, sg); aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSSubnetGroupCreated, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: name}})
	}

	return &xmlCreateDBSubnetGroupResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlCreateDBSubnetGroupResult{DBSubnetGroup: toXMLDBSubnetGroup(sg)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DeleteDBSubnetGroup ---

func (h *Handler) deleteDBSubnetGroupTyped(ctx context.Context, req *deleteDBSubnetGroupReq) (*xmlDeleteDBSubnetGroupResponse, *protocol.AWSError) {
	name := req.DBSubnetGroupName
	if name == "" {
		return nil, errInvalidParameterValue("DBSubnetGroupName is required")
	}

	if _, aerr := h.store.getDBSubnetGroup(ctx, name); aerr != nil {
		return nil, aerr
	}

	if aerr := h.store.deleteDBSubnetGroup(ctx, name); aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSSubnetGroupDeleted, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: name}})
	}

	return &xmlDeleteDBSubnetGroupResponse{
		Xmlns:            rdsXMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DescribeDBSubnetGroups ---

func (h *Handler) describeDBSubnetGroupsTyped(ctx context.Context, req *describeDBSubnetGroupsReq) (*xmlDescribeDBSubnetGroupsResponse, *protocol.AWSError) {
	filterName := req.DBSubnetGroupName

	if filterName != "" {
		sg, aerr := h.store.getDBSubnetGroup(ctx, filterName)
		if aerr != nil {
			return nil, aerr
		}
		return &xmlDescribeDBSubnetGroupsResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBSubnetGroupsResult{
				DBSubnetGroups: xmlDBSubnetGroups{Items: []xmlDBSubnetGroup{toXMLDBSubnetGroup(sg)}},
			},
			ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
		}, nil
	}

	all, aerr := h.store.listDBSubnetGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlDBSubnetGroup, 0, len(all))
	for _, sg := range all {
		items = append(items, toXMLDBSubnetGroup(sg))
	}

	return &xmlDescribeDBSubnetGroupsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBSubnetGroupsResult{
			DBSubnetGroups: xmlDBSubnetGroups{Items: items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- CreateDBParameterGroup ---

func (h *Handler) createDBParameterGroupTyped(ctx context.Context, req *createDBParameterGroupReq) (*xmlCreateDBParameterGroupResponse, *protocol.AWSError) {
	name := req.DBParameterGroupName
	if name == "" {
		return nil, errInvalidParameterValue("DBParameterGroupName is required")
	}

	family := req.DBParameterGroupFamily
	if family == "" {
		return nil, errInvalidParameterValue("DBParameterGroupFamily is required")
	}
	if !knownParameterGroupFamilies[family] {
		return nil, errInvalidParameterValue("Invalid DB parameter group family: " + family)
	}

	if _, aerr := h.store.getDBParameterGroup(ctx, name); aerr == nil {
		return nil, errDBParameterGroupAlreadyExists(name)
	}

	region := h.store.region(ctx)
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "pg:"+name)

	pg := &DBParameterGroup{
		DBParameterGroupName:   name,
		DBParameterGroupFamily: family,
		Description:            req.Description,
		DBParameterGroupArn:    arn,
	}

	if aerr := h.store.putDBParameterGroup(ctx, pg); aerr != nil {
		return nil, aerr
	}

	return &xmlCreateDBParameterGroupResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlCreateDBParameterGroupResult{DBParameterGroup: toXMLDBParameterGroup(pg)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DeleteDBParameterGroup ---

func (h *Handler) deleteDBParameterGroupTyped(ctx context.Context, req *deleteDBParameterGroupReq) (*xmlDeleteDBParameterGroupResponse, *protocol.AWSError) {
	name := req.DBParameterGroupName
	if name == "" {
		return nil, errInvalidParameterValue("DBParameterGroupName is required")
	}

	if _, aerr := h.store.getDBParameterGroup(ctx, name); aerr != nil {
		return nil, aerr
	}

	if aerr := h.store.deleteDBParameterGroup(ctx, name); aerr != nil {
		return nil, aerr
	}

	return &xmlDeleteDBParameterGroupResponse{
		Xmlns:            rdsXMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DescribeDBParameterGroups ---

func (h *Handler) describeDBParameterGroupsTyped(ctx context.Context, req *describeDBParameterGroupsReq) (*xmlDescribeDBParameterGroupsResponse, *protocol.AWSError) {
	filterName := req.DBParameterGroupName

	if filterName != "" {
		pg, aerr := h.store.getDBParameterGroup(ctx, filterName)
		if aerr != nil {
			return nil, aerr
		}
		return &xmlDescribeDBParameterGroupsResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBParameterGroupsResult{
				DBParameterGroups: xmlDBParameterGroups{Items: []xmlDBParameterGroup{toXMLDBParameterGroup(pg)}},
			},
			ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
		}, nil
	}

	all, aerr := h.store.listDBParameterGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlDBParameterGroup, 0, len(all))
	for _, pg := range all {
		items = append(items, toXMLDBParameterGroup(pg))
	}

	return &xmlDescribeDBParameterGroupsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBParameterGroupsResult{
			DBParameterGroups: xmlDBParameterGroups{Items: items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DescribeOrderableDBInstanceOptions ---

func (h *Handler) describeOrderableDBInstanceOptionsTyped(ctx context.Context, req *describeOrderableDBInstanceOptionsReq) (*xmlDescribeOrderableDBInstanceOptionsResponse, *protocol.AWSError) {
	engine := req.Engine
	if engine == "" {
		return nil, errInvalidParameterValue("Engine is required")
	}

	items := make([]xmlOrderableDBInstanceOption, 0)
	for _, opt := range allOrderableOptions {
		if opt.Engine != engine {
			continue
		}
		items = append(items, xmlOrderableDBInstanceOption(opt))
	}

	return &xmlDescribeOrderableDBInstanceOptionsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeOrderableDBInstanceOptionsResult{
			OrderableDBInstanceOptions: xmlOrderableDBInstanceOptions{Items: items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- CreateDBCluster ---

func (h *Handler) createDBClusterTyped(ctx context.Context, req *createDBClusterReq) (*xmlCreateDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	engine := req.Engine
	if !auroraEngines[engine] {
		return nil, errInvalidParameterValue("Engine must be one of: aurora-mysql, aurora-postgresql")
	}

	if req.MasterUsername == "" {
		return nil, errInvalidParameterValue("MasterUsername is required")
	}

	if req.MasterUserPassword == "" {
		return nil, errInvalidParameterValue("MasterUserPassword is required")
	}

	if _, aerr := h.store.getDBCluster(ctx, id); aerr == nil {
		return nil, errDBClusterAlreadyExists(id)
	}

	engineVersion := req.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersions[engine]
	}

	storageType := req.StorageType
	if storageType == "" {
		storageType = "aurora"
	}

	region := h.store.region(ctx)
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "cluster:"+id)
	now := h.clk.Now().UTC().Format(time.RFC3339)

	cluster := &DBCluster{
		DBClusterIdentifier: id,
		DBClusterArn:        arn,
		Engine:              engine,
		EngineVersion:       engineVersion,
		Status:              "creating",
		MasterUsername:      req.MasterUsername,
		DatabaseName:        req.DatabaseName,
		Port:                defaultPorts[engine],
		Endpoint:            id + ".cluster-rw." + region + ".rds." + h.cfg.ExternalHostname(),
		ReaderEndpoint:      id + ".cluster-ro." + region + ".rds." + h.cfg.ExternalHostname(),
		MultiAZ:             req.MultiAZ,
		StorageType:         storageType,
		ClusterCreateTime:   now,
		DBClusterMembers:    []DBClusterMember{},
	}

	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	clID := id
	h.scheduler.After(clID+":available", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBCluster(ctx, clID)
		if aerr != nil {
			return
		}
		if got.Status == "creating" {
			got.Status = "available"
			if aerr := h.store.putDBCluster(ctx, got); aerr != nil {
				h.log.Warn("RDS: persist available cluster", zap.String("cluster", clID), zap.String("error", aerr.Message))
			}
		}
	})

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DescribeDBClusters ---

func (h *Handler) describeDBClustersTyped(ctx context.Context, req *describeDBClustersReq) (*xmlDescribeDBClustersResponse, *protocol.AWSError) {
	filterID := req.DBClusterIdentifier

	if filterID != "" {
		cluster, aerr := h.store.getDBCluster(ctx, filterID)
		if aerr != nil {
			return nil, aerr
		}
		return &xmlDescribeDBClustersResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBClustersResult{
				DBClusters: xmlDBClusters{Items: []xmlDBCluster{toXMLDBCluster(cluster)}},
			},
			ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
		}, nil
	}

	all, aerr := h.store.listDBClusters(ctx)
	if aerr != nil {
		return nil, aerr
	}

	items := make([]xmlDBCluster, 0, len(all))
	for _, c := range all {
		items = append(items, toXMLDBCluster(c))
	}

	return &xmlDescribeDBClustersResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBClustersResult{
			DBClusters: xmlDBClusters{Items: items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- DeleteDBCluster ---

func (h *Handler) deleteDBClusterTyped(ctx context.Context, req *deleteDBClusterReq) (*xmlDeleteDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	cluster, aerr := h.store.getDBCluster(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	cluster.Status = "deleting"
	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	resp := &xmlDeleteDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDeleteDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}

	clID := id
	h.scheduler.After(clID+":delete", 50*time.Millisecond, func() {
		ctx := context.Background()
		if aerr := h.store.deleteDBCluster(ctx, clID); aerr != nil {
			h.log.Warn("failed to delete RDS cluster record",
				zap.String("cluster", clID), zap.Error(aerr))
		}
	})

	return resp, nil
}

// --- ModifyDBCluster ---

func (h *Handler) modifyDBClusterTyped(ctx context.Context, req *modifyDBClusterReq) (*xmlCreateDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	cluster, aerr := h.store.getDBCluster(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	if req.EngineVersion != "" {
		cluster.EngineVersion = req.EngineVersion
	}

	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- StartDBCluster ---

func (h *Handler) startDBClusterTyped(ctx context.Context, req *startDBClusterReq) (*xmlCreateDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	cluster, aerr := h.store.getDBCluster(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	if cluster.Status != "stopped" {
		return nil, &protocol.AWSError{
			Code: "InvalidDBClusterStateFault", Message: "Cluster " + id + " is not in a stopped state.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	cluster.Status = "starting"
	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	clID := id
	h.scheduler.After(clID+":start", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBCluster(ctx, clID)
		if aerr != nil {
			return
		}
		if got.Status == "starting" {
			got.Status = "available"
			if aerr := h.store.putDBCluster(ctx, got); aerr != nil {
				h.log.Warn("RDS: persist started cluster", zap.String("cluster", clID), zap.String("error", aerr.Message))
			}
		}
	})

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- StopDBCluster ---

func (h *Handler) stopDBClusterTyped(ctx context.Context, req *stopDBClusterReq) (*xmlCreateDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	cluster, aerr := h.store.getDBCluster(ctx, id)
	if aerr != nil {
		return nil, aerr
	}

	if cluster.Status != "available" {
		return nil, &protocol.AWSError{
			Code: "InvalidDBClusterStateFault", Message: "Cluster " + id + " is not in an available state.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	cluster.Status = "stopping"
	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	clID := id
	h.scheduler.After(clID+":stop", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBCluster(ctx, clID)
		if aerr != nil {
			return
		}
		if got.Status == "stopping" {
			got.Status = "stopped"
			if aerr := h.store.putDBCluster(ctx, got); aerr != nil {
				h.log.Warn("RDS: persist stopped cluster", zap.String("cluster", clID), zap.String("error", aerr.Message))
			}
		}
	})

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}
