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
	// MultiAZ is a pointer because "absent" and "false" are different
	// requests: absent leaves the instance alone, `MultiAZ=false` turns
	// Multi-AZ off. A plain bool cannot tell them apart, which is why this
	// operation used to ignore a request to disable Multi-AZ.
	MultiAZ     *bool  `json:"MultiAZ"`
	StorageType string `json:"StorageType"`
	// MasterUserPassword rotates the master password. Unlike every other field
	// here it is not applied to the record alone — see password.go.
	MasterUserPassword string `json:"MasterUserPassword"`
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

// createDBClusterReq accepts the same settings ModifyDBCluster can change.
// What can be updated has to be creatable too: with Port and the rest readable
// only on the modify path, a template that set them at create and never
// touched them again produced a cluster that had never had them applied, and
// made the first no-op update look like a change.
type createDBClusterReq struct {
	DBClusterIdentifier               string                             `json:"DBClusterIdentifier"`
	Engine                            string                             `json:"Engine"`
	MasterUsername                    string                             `json:"MasterUsername"`
	MasterUserPassword                string                             `json:"MasterUserPassword"`
	EngineVersion                     string                             `json:"EngineVersion"`
	StorageType                       string                             `json:"StorageType"`
	MultiAZ                           bool                               `json:"MultiAZ"`
	DatabaseName                      string                             `json:"DatabaseName"`
	Port                              int                                `json:"Port"`
	DBSubnetGroupName                 string                             `json:"DBSubnetGroupName"`
	PreferredBackupWindow             string                             `json:"PreferredBackupWindow"`
	PreferredMaintenanceWindow        string                             `json:"PreferredMaintenanceWindow"`
	DBClusterParameterGroupName       string                             `json:"DBClusterParameterGroupName"`
	VpcSecurityGroupIds               []string                           `json:"VpcSecurityGroupIds"`
	BackupRetentionPeriod             *int                               `json:"BackupRetentionPeriod"`
	DeletionProtection                *bool                              `json:"DeletionProtection"`
	EnableCloudwatchLogsExports       []string                           `json:"EnableCloudwatchLogsExports"`
	CloudwatchLogsExportConfiguration *cloudwatchLogsExportConfiguration `json:"CloudwatchLogsExportConfiguration"`
}

type describeDBClustersReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
}

type deleteDBClusterReq struct {
	DBClusterIdentifier string `json:"DBClusterIdentifier"`
}

// cloudwatchLogsExportConfiguration is ModifyDBCluster's nested log-export
// parameter. AWS spells the request and the response differently: this goes
// in, EnabledCloudwatchLogsExports comes back.
type cloudwatchLogsExportConfiguration struct {
	EnableLogTypes  []string `json:"EnableLogTypes"`
	DisableLogTypes []string `json:"DisableLogTypes"`
}

// modifyDBClusterReq carried DBClusterIdentifier and EngineVersion, and the
// CloudFormation handler for AWS::RDS::DBCluster sent nine other parameters
// that decoded into nothing at all — so a stack update that changed any of
// them reported UPDATE_COMPLETE having changed nothing.
//
// BackupRetentionPeriod and DeletionProtection are pointers for the reason
// modifyDBInstanceReq.MultiAZ is: "absent" and "zero" are different requests.
// `BackupRetentionPeriod=0` turns automated backups off and
// `DeletionProtection=false` turns protection off, and a plain int or bool
// cannot tell either of those from a parameter that was not sent.
type modifyDBClusterReq struct {
	DBClusterIdentifier               string                             `json:"DBClusterIdentifier"`
	EngineVersion                     string                             `json:"EngineVersion"`
	MasterUserPassword                string                             `json:"MasterUserPassword"`
	Port                              int                                `json:"Port"`
	PreferredBackupWindow             string                             `json:"PreferredBackupWindow"`
	PreferredMaintenanceWindow        string                             `json:"PreferredMaintenanceWindow"`
	DBClusterParameterGroupName       string                             `json:"DBClusterParameterGroupName"`
	VpcSecurityGroupIds               []string                           `json:"VpcSecurityGroupIds"`
	BackupRetentionPeriod             *int                               `json:"BackupRetentionPeriod"`
	DeletionProtection                *bool                              `json:"DeletionProtection"`
	CloudwatchLogsExportConfiguration *cloudwatchLogsExportConfiguration `json:"CloudwatchLogsExportConfiguration"`
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
	// Validated at create as well as on modify. Accepting a password here that
	// ModifyDBInstance would refuse leaves an instance that can never change
	// it, and RDS rejects it at this point too.
	if aerr := validateMasterUserPassword(masterPass); aerr != nil {
		return nil, aerr
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

	// Stored canonical, re-minted for whoever reads it — see endpoint.go.
	endpoint := &Endpoint{
		Address: instanceEndpointHostname(id, region, h.externalHostname()),
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

	launchingContainer := h.dockerReady.Load()
	if launchingContainer {
		if image, _, ok := resolveEngineImage(inst.Engine, inst.EngineVersion); ok && h.puller != nil {
			h.puller.Prewarm(image)
		}
		h.launchDBContainerAsync(ctx, id)
	}

	// Only a metadata-only instance is available the moment it exists. When a
	// container is coming up, the health check owns the transition — it is the
	// only thing that knows whether the engine answers.
	//
	// This used to run unconditionally, "so the instance is immediately usable
	// via the API". It made the status meaningless on the path most people
	// use: against a real MySQL container the instance reported available 0.9s
	// after CreateDBInstance and did not accept a connection for another 27s,
	// and because the health check only promotes creating/starting it found
	// the instance already available and did nothing — so a create whose
	// container never came up stayed available forever.
	if !launchingContainer {
		instID := id
		h.scheduler.AfterScoped(region, instID, "available", 0, func(ctx context.Context) {
			h.transitionInstance(ctx, instID, "creating", "available")
		})
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceCreated, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}
	h.recordInstanceEvent(ctx, id, "DB instance created.", "creation")

	if clusterID != "" {
		h.addInstanceToCluster(ctx, clusterID, id)
	}

	return &xmlCreateDBInstanceResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBInstanceResult{
			DBInstance: h.toXMLDBInstance(ctx, inst),
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
				DBInstances: xmlDBInstances{Items: []xmlDBInstance{h.toXMLDBInstance(ctx, inst)}},
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
		items = append(items, h.toXMLDBInstance(ctx, inst))
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

	var containerID string
	var hostPort int
	inst, aerr := h.mutateInstance(ctx, id, func(inst *DBInstance) *protocol.AWSError {
		containerID = inst.DockerContainerID
		hostPort = inst.HostPort
		inst.DBInstanceStatus = "deleting"
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceDeleted, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}
	h.recordInstanceEvent(ctx, id, "DB instance deleted.", "deletion")

	resp := &xmlDeleteDBInstanceResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDeleteDBInstanceResult{
			DBInstance: h.toXMLDBInstance(ctx, inst),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}

	region := h.store.region(ctx)
	h.scheduler.CancelScoped(region, id, "health")

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

	h.scheduler.AfterScoped(region, id, "delete", 50*time.Millisecond, func(bgCtx context.Context) {
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

	inst, aerr := h.mutateInstance(ctx, id, func(inst *DBInstance) *protocol.AWSError {
		if inst.DBInstanceStatus != "available" {
			return errInvalidDBInstanceState(id, "must be available to stop")
		}
		inst.DBInstanceStatus = "stopping"
		inst.StoppedByUser = true
		inst.DockerRecoveryPending = false
		inst.DockerRecoveryAttempts = 0
		inst.DockerRecoveryAvailableAt = time.Time{}
		return nil
	})
	if aerr != nil {
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
	region := h.store.region(ctx)
	h.scheduler.AfterScoped(region, instID, "stopped", 0, func(ctx context.Context) {
		h.transitionInstance(ctx, instID, "stopping", "stopped")
	})

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceStopped, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}
	// RDS-EVENT-0087.
	h.recordInstanceEvent(ctx, id, "DB instance stopped.", "notification")

	return &xmlStopDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlStopDBInstanceResult{DBInstance: h.toXMLDBInstance(ctx, inst)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- StartDBInstance ---

func (h *Handler) startDBInstanceTyped(ctx context.Context, req *startDBInstanceReq) (*xmlStartDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	inst, aerr := h.mutateInstance(ctx, id, func(inst *DBInstance) *protocol.AWSError {
		if inst.DBInstanceStatus != "stopped" {
			return errInvalidDBInstanceState(id, "must be stopped to start")
		}
		inst.DBInstanceStatus = "starting"
		inst.StoppedByUser = false
		inst.DockerRecoveryPending = false
		inst.DockerRecoveryAttempts = 0
		inst.DockerRecoveryAvailableAt = time.Time{}
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	region := h.store.region(ctx)
	if h.dockerReady.Load() && inst.DockerContainerID != "" {
		// Off the request path: a container Docker no longer has is rebuilt,
		// which may pull an image. The instance settles to available or failed
		// on its own — AWS answers StartDBInstance 200 with "starting" and
		// surfaces a start failure through the status, not through this call.
		h.startInstanceContainerAsync(ctx, id)
	} else {
		instID2 := id
		h.scheduler.AfterScoped(region, instID2, "available", 0, func(ctx context.Context) {
			h.transitionInstance(ctx, instID2, "starting", "available")
		})
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceStarted, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}
	// RDS-EVENT-0088.
	h.recordInstanceEvent(ctx, id, "DB instance started.", "notification")

	return &xmlStartDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlStartDBInstanceResult{DBInstance: h.toXMLDBInstance(ctx, inst)},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// --- ModifyDBInstance ---

func (h *Handler) modifyDBInstanceTyped(ctx context.Context, req *modifyDBInstanceReq) (*xmlModifyDBInstanceResponse, *protocol.AWSError) {
	id := req.DBInstanceIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBInstanceIdentifier is required")
	}

	// The password goes first, and nothing else is applied unless it lands: a
	// modification that half-succeeded is harder to reason about than one that
	// was refused outright, and the caller can retry either way.
	//
	// It runs against the engine in the container, which is far too slow to
	// hold a record lock across, so it happens here on a snapshot — the record
	// it reads only decides whether the password is really changing and how to
	// reach the container. The new value is written with everything else below.
	if req.MasterUserPassword != "" {
		current, aerr := h.store.getDBInstance(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		if req.MasterUserPassword != current.MasterUserPassword {
			if aerr := h.changeMasterPassword(ctx, current, req.MasterUserPassword); aerr != nil {
				return nil, aerr
			}
		}
	}

	var settledStatus string
	inst, aerr := h.mutateInstance(ctx, id, func(inst *DBInstance) *protocol.AWSError {
		if req.MasterUserPassword != "" {
			inst.MasterUserPassword = req.MasterUserPassword
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
		if req.MultiAZ != nil {
			inst.MultiAZ = *req.MultiAZ
		}
		if req.StorageType != "" {
			inst.StorageType = req.StorageType
		}

		settledStatus = inst.DBInstanceStatus
		if settledStatus == "" {
			settledStatus = "available"
		}
		inst.DBInstanceStatus = "modifying"
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	instID := id
	region := h.store.region(ctx)
	h.scheduler.AfterScoped(region, instID, "modified", 500*time.Millisecond, func(ctx context.Context) {
		h.transitionInstance(ctx, instID, "modifying", settledStatus)
	})

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: events.RDSInstanceModified, Time: h.clk.Now(), Source: "rds", Payload: events.ResourcePayload{Name: id}})
	}

	return &xmlModifyDBInstanceResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlModifyDBInstanceResult{DBInstance: h.toXMLDBInstance(ctx, inst)},
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
	if aerr := validateMasterUserPassword(req.MasterUserPassword); aerr != nil {
		return nil, aerr
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

	port := req.Port
	if port == 0 {
		port = defaultPorts[engine]
	}

	logExports := req.EnableCloudwatchLogsExports
	if cfg := req.CloudwatchLogsExportConfiguration; cfg != nil {
		logExports = applyLogExportConfiguration(logExports, cfg)
	}

	cluster := &DBCluster{
		DBClusterIdentifier:          id,
		DBClusterArn:                 arn,
		Engine:                       engine,
		EngineVersion:                engineVersion,
		Status:                       "creating",
		MasterUsername:               req.MasterUsername,
		MasterUserPassword:           req.MasterUserPassword,
		DatabaseName:                 req.DatabaseName,
		Port:                         port,
		Endpoint:                     id + ".cluster-rw." + region + ".rds." + h.cfg.ExternalHostname(),
		ReaderEndpoint:               id + ".cluster-ro." + region + ".rds." + h.cfg.ExternalHostname(),
		MultiAZ:                      req.MultiAZ,
		StorageType:                  storageType,
		ClusterCreateTime:            now,
		DBClusterMembers:             []DBClusterMember{},
		DBSubnetGroupName:            req.DBSubnetGroupName,
		PreferredBackupWindow:        req.PreferredBackupWindow,
		PreferredMaintenanceWindow:   req.PreferredMaintenanceWindow,
		DBClusterParameterGroup:      req.DBClusterParameterGroupName,
		VpcSecurityGroupIds:          req.VpcSecurityGroupIds,
		EnabledCloudwatchLogsExports: logExports,
	}
	if req.BackupRetentionPeriod != nil {
		cluster.BackupRetentionPeriod = *req.BackupRetentionPeriod
	}
	if req.DeletionProtection != nil {
		cluster.DeletionProtection = *req.DeletionProtection
	}

	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		return nil, aerr
	}

	clID := id
	h.scheduler.AfterScoped(region, clID, "available", 500*time.Millisecond, func(ctx context.Context) {
		h.transitionCluster(ctx, clID, "creating", "available")
	})

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: h.toXMLDBCluster(ctx, cluster),
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
				DBClusters: xmlDBClusters{Items: []xmlDBCluster{h.toXMLDBCluster(ctx, cluster)}},
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
		items = append(items, h.toXMLDBCluster(ctx, c))
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

	cluster, aerr := h.mutateCluster(ctx, id, func(cluster *DBCluster) *protocol.AWSError {
		// AWS refuses outright rather than deleting and reporting the flag
		// afterwards, and a flag that is recorded but not enforced is worse
		// than one that was never recorded: the console shows the cluster as
		// protected while a stack teardown removes it anyway. Refusing inside
		// the mutation leaves the record untouched.
		if cluster.DeletionProtection {
			return &protocol.AWSError{
				Code:       "InvalidParameterCombination",
				Message:    "Cannot delete protected DB Cluster, please disable deletion protection and try again.",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		cluster.Status = "deleting"
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	resp := &xmlDeleteDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDeleteDBClusterResult{
			DBCluster: h.toXMLDBCluster(ctx, cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}

	clID := id
	region := h.store.region(ctx)
	h.scheduler.AfterScoped(region, clID, "delete", 50*time.Millisecond, func(ctx context.Context) {
		if aerr := h.store.deleteDBCluster(ctx, clID); aerr != nil {
			h.log.Warn("failed to delete RDS cluster record",
				zap.String("cluster", clID), zap.Error(aerr))
		}
	})

	return resp, nil
}

// --- ModifyDBCluster ---

func (h *Handler) modifyDBClusterTyped(ctx context.Context, req *modifyDBClusterReq) (*xmlModifyDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	// The password goes first, and nothing else is applied unless it lands —
	// the same discipline ModifyDBInstance follows, and for the same reasons.
	// It runs here rather than inside the mutation below on two counts: it
	// reaches an engine in a container, which is far too slow to hold a record
	// lock across, and it takes each member's instance lock, which must never
	// nest inside the cluster's (see locks.go).
	if req.MasterUserPassword != "" {
		current, aerr := h.store.getDBCluster(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		if req.MasterUserPassword != current.MasterUserPassword {
			if aerr := h.changeClusterMasterPassword(ctx, current, req.MasterUserPassword); aerr != nil {
				return nil, aerr
			}
		}
	}

	cluster, aerr := h.mutateCluster(ctx, id, func(cluster *DBCluster) *protocol.AWSError {
		if req.MasterUserPassword != "" {
			cluster.MasterUserPassword = req.MasterUserPassword
		}
		if req.EngineVersion != "" {
			cluster.EngineVersion = req.EngineVersion
		}
		if req.Port != 0 {
			cluster.Port = req.Port
		}
		if req.PreferredBackupWindow != "" {
			cluster.PreferredBackupWindow = req.PreferredBackupWindow
		}
		if req.PreferredMaintenanceWindow != "" {
			cluster.PreferredMaintenanceWindow = req.PreferredMaintenanceWindow
		}
		if req.DBClusterParameterGroupName != "" {
			cluster.DBClusterParameterGroup = req.DBClusterParameterGroupName
		}
		if len(req.VpcSecurityGroupIds) > 0 {
			cluster.VpcSecurityGroupIds = req.VpcSecurityGroupIds
		}
		if req.BackupRetentionPeriod != nil {
			cluster.BackupRetentionPeriod = *req.BackupRetentionPeriod
		}
		if req.DeletionProtection != nil {
			cluster.DeletionProtection = *req.DeletionProtection
		}
		if cfg := req.CloudwatchLogsExportConfiguration; cfg != nil {
			cluster.EnabledCloudwatchLogsExports = applyLogExportConfiguration(
				cluster.EnabledCloudwatchLogsExports, cfg)
		}
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	return &xmlModifyDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: h.toXMLDBCluster(ctx, cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// applyLogExportConfiguration folds an enable/disable request into the log
// types already exported. AWS's parameter is a delta, not a replacement: a
// request that enables "audit" does not turn "error" off.
func applyLogExportConfiguration(current []string, cfg *cloudwatchLogsExportConfiguration) []string {
	enabled := make(map[string]bool, len(current))
	order := append([]string(nil), current...)
	for _, t := range current {
		enabled[t] = true
	}
	for _, t := range cfg.EnableLogTypes {
		if !enabled[t] {
			enabled[t] = true
			order = append(order, t)
		}
	}
	for _, t := range cfg.DisableLogTypes {
		delete(enabled, t)
	}

	out := make([]string, 0, len(order))
	for _, t := range order {
		if enabled[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- StartDBCluster ---

func (h *Handler) startDBClusterTyped(ctx context.Context, req *startDBClusterReq) (*xmlCreateDBClusterResponse, *protocol.AWSError) {
	id := req.DBClusterIdentifier
	if id == "" {
		return nil, errInvalidParameterValue("DBClusterIdentifier is required")
	}

	cluster, aerr := h.mutateCluster(ctx, id, func(cluster *DBCluster) *protocol.AWSError {
		if cluster.Status != "stopped" {
			return &protocol.AWSError{
				Code: "InvalidDBClusterStateFault", Message: "Cluster " + id + " is not in a stopped state.",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		cluster.Status = "starting"
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	clID := id
	region := h.store.region(ctx)
	h.scheduler.AfterScoped(region, clID, "start", 500*time.Millisecond, func(ctx context.Context) {
		h.transitionCluster(ctx, clID, "starting", "available")
	})

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: h.toXMLDBCluster(ctx, cluster),
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

	cluster, aerr := h.mutateCluster(ctx, id, func(cluster *DBCluster) *protocol.AWSError {
		if cluster.Status != "available" {
			return &protocol.AWSError{
				Code: "InvalidDBClusterStateFault", Message: "Cluster " + id + " is not in an available state.",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		cluster.Status = "stopping"
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	clID := id
	region := h.store.region(ctx)
	h.scheduler.AfterScoped(region, clID, "stop", 500*time.Millisecond, func(ctx context.Context) {
		h.transitionCluster(ctx, clID, "stopping", "stopped")
	})

	return &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: h.toXMLDBCluster(ctx, cluster),
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}
