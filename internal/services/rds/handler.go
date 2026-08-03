package rds

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const rdsXMLNS = "http://rds.amazonaws.com/doc/2014-10-31/"

// Handler handles RDS Query-protocol requests.
type Handler struct {
	cfg         *config.Config
	store       *rdsStore
	log         *serviceutil.ServiceLogger
	clk         clock.Clock
	bus         *events.Bus
	scheduler   *lifecycle.Scheduler
	docker      *docker.Client
	dockerReady atomic.Bool
	dockerWg    sync.WaitGroup
	puller      *docker.ImagePuller
	vpcResolver VPCNetworkResolver
	gc          *docker.GC
	ops         map[string]http.HandlerFunc
	typedOp     map[string]op.Operation
}

// VPCNetworkResolver resolves DB subnet groups back to EC2 VPC network state.
type VPCNetworkResolver interface {
	VpcIDForSubnet(ctx context.Context, subnetID string) string
	VPCNetworkStatus(ctx context.Context, vpcID string) string
	DockerNetworkForVpc(ctx context.Context, vpcID string) string
}

func newHandler(cfg *config.Config, store *rdsStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:       cfg,
		store:     store,
		log:       log,
		clk:       clk,
		scheduler: lifecycle.NewScheduler(clk),
	}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateDBInstance":                   h.CreateDBInstance,
		"DescribeDBInstances":                h.DescribeDBInstances,
		"DeleteDBInstance":                   h.DeleteDBInstance,
		"DescribeDBEngineVersions":           h.DescribeDBEngineVersions,
		"StopDBInstance":                     h.StopDBInstance,
		"StartDBInstance":                    h.StartDBInstance,
		"ModifyDBInstance":                   h.ModifyDBInstance,
		"CreateDBSubnetGroup":                h.CreateDBSubnetGroup,
		"DeleteDBSubnetGroup":                h.DeleteDBSubnetGroup,
		"DescribeDBSubnetGroups":             h.DescribeDBSubnetGroups,
		"CreateDBParameterGroup":             h.CreateDBParameterGroup,
		"DeleteDBParameterGroup":             h.DeleteDBParameterGroup,
		"DescribeDBParameterGroups":          h.DescribeDBParameterGroups,
		"DescribeOrderableDBInstanceOptions": h.DescribeOrderableDBInstanceOptions,
		"CreateDBCluster":                    h.CreateDBCluster,
		"DeleteDBCluster":                    h.DeleteDBCluster,
		"DescribeDBClusters":                 h.DescribeDBClusters,
		"ModifyDBCluster":                    h.ModifyDBCluster,
		"StartDBCluster":                     h.StartDBCluster,
		"StopDBCluster":                      h.StopDBCluster,
		"CreateDBSnapshot":                   h.CreateDBSnapshot,
		"DeleteDBSnapshot":                   h.DeleteDBSnapshot,
		"DescribeDBSnapshots":                h.DescribeDBSnapshots,
		"RestoreDBInstanceFromDBSnapshot":    h.RestoreDBInstanceFromDBSnapshot,
		"CreateDBClusterSnapshot":            h.CreateDBClusterSnapshot,
		"DeleteDBClusterSnapshot":            h.DeleteDBClusterSnapshot,
		"DescribeDBClusterSnapshots":         h.DescribeDBClusterSnapshots,
		"RebootDBInstance":                   h.RebootDBInstance,
		"DescribeDBLogFiles":                 h.DescribeDBLogFiles,
		"DownloadDBLogFilePortion":           h.DownloadDBLogFilePortion,
		"AddTagsToResource":                  h.AddTagsToResource,
		"RemoveTagsFromResource":             h.RemoveTagsFromResource,
		"ListTagsForResource":                h.ListTagsForResource,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) ownsAction(action string) bool {
	_, ok := h.ops[action]
	return ok
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// ── Engine defaults ──────────────────────────────────────────────────────────

var defaultEngineVersions = map[string]string{
	"mysql":             "8.0",
	"postgres":          "16.1",
	"mariadb":           "11.4",
	"aurora-mysql":      "3.04",
	"aurora-postgresql": "15.4",
}

var defaultPorts = map[string]int{
	"mysql":             3306,
	"postgres":          5432,
	"mariadb":           3306,
	"aurora-mysql":      3306,
	"aurora-postgresql": 5432,
}

var supportedEngines = map[string]bool{
	"mysql":             true,
	"postgres":          true,
	"mariadb":           true,
	"aurora-mysql":      true,
	"aurora-postgresql": true,
}

// auroraEngines lists engines that require the cluster/instance resource model.
var auroraEngines = map[string]bool{
	"aurora-mysql":      true,
	"aurora-postgresql": true,
}

// engineImages maps engine → version → Docker image tag. The keys are the
// versions DescribeDBEngineVersions advertises; resolveEngineImage matches
// anything else against them, so a real EngineVersion still starts a container.
var engineImages = map[string]map[string]string{
	"mysql": {
		"8.0": "mysql:8.0",
		"5.7": "mysql:5.7",
	},
	"postgres": {
		"16.1":  "postgres:16",
		"15.5":  "postgres:15",
		"14.11": "postgres:14",
	},
	"mariadb": {
		"11.4":  "mariadb:11",
		"10.11": "mariadb:10.11",
	},
	// Aurora variants use the same Docker images as their underlying engines.
	// aurora-mysql is MySQL-wire-compatible; aurora-postgresql is PostgreSQL-wire-compatible.
	"aurora-mysql": {
		"3.04": "mysql:8.0",
		"2.11": "mysql:5.7",
	},
	"aurora-postgresql": {
		"15.4":  "postgres:15",
		"14.11": "postgres:14",
	},
}

// engineEnv describes engine-specific Docker environment variables and ports.
type engineEnv struct {
	PasswordVar   string
	DatabaseVar   string
	UserVar       string // optional: sets the DB superuser name (e.g. POSTGRES_USER)
	ContainerPort int
}

var engineEnvConfig = map[string]engineEnv{
	"mysql":             {PasswordVar: "MYSQL_ROOT_PASSWORD", DatabaseVar: "MYSQL_DATABASE", ContainerPort: 3306},
	"postgres":          {PasswordVar: "POSTGRES_PASSWORD", DatabaseVar: "POSTGRES_DB", UserVar: "POSTGRES_USER", ContainerPort: 5432},
	"mariadb":           {PasswordVar: "MARIADB_ROOT_PASSWORD", DatabaseVar: "MARIADB_DATABASE", ContainerPort: 3306},
	"aurora-mysql":      {PasswordVar: "MYSQL_ROOT_PASSWORD", DatabaseVar: "MYSQL_DATABASE", ContainerPort: 3306},
	"aurora-postgresql": {PasswordVar: "POSTGRES_PASSWORD", DatabaseVar: "POSTGRES_DB", UserVar: "POSTGRES_USER", ContainerPort: 5432},
}

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateDBInstanceResponse struct {
	XMLName          xml.Name                  `xml:"CreateDBInstanceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlCreateDBInstanceResult `xml:"CreateDBInstanceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlCreateDBInstanceResult struct {
	DBInstance xmlDBInstance `xml:"DBInstance"`
}

type xmlDeleteDBInstanceResponse struct {
	XMLName          xml.Name                  `xml:"DeleteDBInstanceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlDeleteDBInstanceResult `xml:"DeleteDBInstanceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDeleteDBInstanceResult struct {
	DBInstance xmlDBInstance `xml:"DBInstance"`
}

type xmlDescribeDBInstancesResponse struct {
	XMLName          xml.Name                     `xml:"DescribeDBInstancesResponse"`
	Xmlns            string                       `xml:"xmlns,attr"`
	Result           xmlDescribeDBInstancesResult `xml:"DescribeDBInstancesResult"`
	ResponseMetadata protocol.ResponseMetadata    `xml:"ResponseMetadata"`
}

type xmlDescribeDBInstancesResult struct {
	DBInstances xmlDBInstances `xml:"DBInstances"`
}

type xmlDBInstances struct {
	Items []xmlDBInstance `xml:"DBInstance"`
}

type xmlDBInstance struct {
	DBInstanceIdentifier string      `xml:"DBInstanceIdentifier"`
	DBInstanceClass      string      `xml:"DBInstanceClass"`
	Engine               string      `xml:"Engine"`
	EngineVersion        string      `xml:"EngineVersion"`
	DBInstanceStatus     string      `xml:"DBInstanceStatus"`
	MasterUsername       string      `xml:"MasterUsername"`
	DBName               string      `xml:"DBName,omitempty"`
	Endpoint             xmlEndpoint `xml:"Endpoint"`
	AllocatedStorage     int         `xml:"AllocatedStorage"`
	DBInstanceArn        string      `xml:"DBInstanceArn"`
	InstanceCreateTime   string      `xml:"InstanceCreateTime,omitempty"`
	MultiAZ              bool        `xml:"MultiAZ"`
	StorageType          string      `xml:"StorageType"`
	DBClusterIdentifier  string      `xml:"DBClusterIdentifier,omitempty"`
}

type xmlEndpoint struct {
	Address string `xml:"Address"`
	Port    int    `xml:"Port"`
}

type xmlDescribeDBEngineVersionsResponse struct {
	XMLName          xml.Name                          `xml:"DescribeDBEngineVersionsResponse"`
	Xmlns            string                            `xml:"xmlns,attr"`
	Result           xmlDescribeDBEngineVersionsResult `xml:"DescribeDBEngineVersionsResult"`
	ResponseMetadata protocol.ResponseMetadata         `xml:"ResponseMetadata"`
}

type xmlDescribeDBEngineVersionsResult struct {
	DBEngineVersions xmlDBEngineVersions `xml:"DBEngineVersions"`
}

type xmlDBEngineVersions struct {
	Items []xmlDBEngineVersion `xml:"DBEngineVersion"`
}

type xmlDBEngineVersion struct {
	Engine                     string `xml:"Engine"`
	EngineVersion              string `xml:"EngineVersion"`
	DBParameterGroupFamily     string `xml:"DBParameterGroupFamily"`
	DBEngineDescription        string `xml:"DBEngineDescription"`
	DBEngineVersionDescription string `xml:"DBEngineVersionDescription"`
}

// ── New XML response types ───────────────────────────────────────────────────

type xmlStopDBInstanceResponse struct {
	XMLName          xml.Name                  `xml:"StopDBInstanceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlStopDBInstanceResult   `xml:"StopDBInstanceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlStopDBInstanceResult struct {
	DBInstance xmlDBInstance `xml:"DBInstance"`
}

type xmlStartDBInstanceResponse struct {
	XMLName          xml.Name                  `xml:"StartDBInstanceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlStartDBInstanceResult  `xml:"StartDBInstanceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlStartDBInstanceResult struct {
	DBInstance xmlDBInstance `xml:"DBInstance"`
}

type xmlModifyDBInstanceResponse struct {
	XMLName          xml.Name                  `xml:"ModifyDBInstanceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlModifyDBInstanceResult `xml:"ModifyDBInstanceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlModifyDBInstanceResult struct {
	DBInstance xmlDBInstance `xml:"DBInstance"`
}

type xmlCreateDBSubnetGroupResponse struct {
	XMLName          xml.Name                     `xml:"CreateDBSubnetGroupResponse"`
	Xmlns            string                       `xml:"xmlns,attr"`
	Result           xmlCreateDBSubnetGroupResult `xml:"CreateDBSubnetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata    `xml:"ResponseMetadata"`
}

type xmlCreateDBSubnetGroupResult struct {
	DBSubnetGroup xmlDBSubnetGroup `xml:"DBSubnetGroup"`
}

type xmlDeleteDBSubnetGroupResponse struct {
	XMLName          xml.Name                  `xml:"DeleteDBSubnetGroupResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeDBSubnetGroupsResponse struct {
	XMLName          xml.Name                        `xml:"DescribeDBSubnetGroupsResponse"`
	Xmlns            string                          `xml:"xmlns,attr"`
	Result           xmlDescribeDBSubnetGroupsResult `xml:"DescribeDBSubnetGroupsResult"`
	ResponseMetadata protocol.ResponseMetadata       `xml:"ResponseMetadata"`
}

type xmlDescribeDBSubnetGroupsResult struct {
	DBSubnetGroups xmlDBSubnetGroups `xml:"DBSubnetGroups"`
}

type xmlDBSubnetGroups struct {
	Items []xmlDBSubnetGroup `xml:"DBSubnetGroup"`
}

type xmlDBSubnetGroup struct {
	DBSubnetGroupName        string     `xml:"DBSubnetGroupName"`
	DBSubnetGroupDescription string     `xml:"DBSubnetGroupDescription"`
	DBSubnetGroupArn         string     `xml:"DBSubnetGroupArn"`
	VpcId                    string     `xml:"VpcId"`
	SubnetIds                xmlSubnets `xml:"Subnets"`
	Status                   string     `xml:"SubnetGroupStatus"`
}

type xmlSubnets struct {
	Items []xmlSubnet `xml:"Subnet"`
}

type xmlSubnet struct {
	SubnetIdentifier string `xml:"SubnetIdentifier"`
}

// ── CreateDBInstance ─────────────────────────────────────────────────────────

// CreateDBInstance creates a new RDS DB instance (metadata-only).
func (h *Handler) CreateDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBInstanceIdentifier is required"))
		return
	}

	engine := r.FormValue("Engine")
	if engine == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine is required"))
		return
	}
	if !supportedEngines[engine] {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine must be one of: mysql, postgres, mariadb, aurora-mysql, aurora-postgresql"))
		return
	}

	masterUser := r.FormValue("MasterUsername")
	if masterUser == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("MasterUsername is required"))
		return
	}

	masterPass := r.FormValue("MasterUserPassword")
	if masterPass == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("MasterUserPassword is required"))
		return
	}

	// Check for duplicate.
	if _, aerr := h.store.getDBInstance(r.Context(), id); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errDBInstanceAlreadyExists(id))
		return
	}

	// Defaults.
	instanceClass := r.FormValue("DBInstanceClass")
	if instanceClass == "" {
		instanceClass = "db.t3.micro"
	}

	engineVersion := r.FormValue("EngineVersion")
	if engineVersion == "" {
		engineVersion = defaultEngineVersions[engine]
	}

	allocatedStorage := formInt(r, "AllocatedStorage", 20)

	port := formInt(r, "Port", defaultPorts[engine])

	storageType := r.FormValue("StorageType")
	if storageType == "" {
		storageType = "gp2"
	}

	multiAZ := r.FormValue("MultiAZ") == "true"
	dbName := r.FormValue("DBName")
	clusterID := r.FormValue("DBClusterIdentifier")
	dbSubnetGroupName := r.FormValue("DBSubnetGroupName")
	vpcID := ""

	// If a cluster identifier is supplied, validate it exists before proceeding.
	if clusterID != "" {
		if _, aerr := h.store.getDBCluster(r.Context(), clusterID); aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
	}
	if dbSubnetGroupName != "" {
		sg, aerr := h.store.getDBSubnetGroup(r.Context(), dbSubnetGroupName)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		vpcID = sg.VpcId
		if h.vpcResolver != nil && vpcID != "" {
			switch status := h.vpcResolver.VPCNetworkStatus(r.Context(), vpcID); status {
			case "", "ok", "shared", "remapped":
				// launchable
			case "conflict", "unbacked":
				protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
					Code:       "InvalidVPCNetworkStateFault",
					Message:    fmt.Sprintf("VPC '%s' is not launchable for DB instances (network status=%s).", vpcID, status),
					HTTPStatus: http.StatusBadRequest,
				})
				return
			default:
				protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
					Code:       "InvalidVPCNetworkStateFault",
					Message:    fmt.Sprintf("VPC '%s' is not launchable for DB instances (network status=%s).", vpcID, status),
					HTTPStatus: http.StatusBadRequest,
				})
				return
			}
		}
	}

	region := h.store.region(r.Context())
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

	if aerr := h.store.putDBInstance(r.Context(), inst); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// If Docker is available, start the database container in the background
	// so the image pull does not block CreateDBInstance on the request path.
	// The instance stays in "creating" until the pull + container start +
	// health check all complete, matching AWS semantics where CreateDBInstance
	// returns immediately and clients poll DescribeDBInstances.
	if h.dockerReady.Load() {
		// Pre-warm the image via the shared puller's dedup so a concurrent
		// describe-wait has its pull coalesced with the background start.
		if image, _, ok := resolveEngineImage(inst.Engine, inst.EngineVersion); ok && h.puller != nil {
			h.puller.Prewarm(image)
		}
		h.launchDBContainerAsync(r.Context(), id)
	}

	// Always schedule the metadata-only creating → available transition so
	// the instance is immediately usable via the API regardless of whether
	// a Docker container was started.  With a real clock the scheduler runs
	// 0-delay callbacks synchronously; with a mock clock the transition stays
	// pending until clock.Add is called.
	instID := id
	h.scheduler.AfterScoped(region, instID, "available", 0, func(ctx context.Context) {
		got, aerr := h.store.getDBInstance(ctx, instID)
		if aerr != nil {
			return
		}
		if got.DBInstanceStatus == "creating" {
			got.DBInstanceStatus = "available"
			h.store.putDBInstance(ctx, got) //nolint:errcheck
		}
	})

	h.publish(r, events.RDSInstanceCreated, events.ResourcePayload{Name: id})

	// If the instance belongs to a cluster, register it as a cluster member.
	if clusterID != "" {
		h.addInstanceToCluster(r.Context(), clusterID, id)
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateDBInstanceResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBInstanceResult{
			DBInstance: h.toXMLDBInstance(r.Context(), inst),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeDBInstances ──────────────────────────────────────────────────────

// DescribeDBInstances returns DB instances, optionally filtered by identifier.
func (h *Handler) DescribeDBInstances(w http.ResponseWriter, r *http.Request) {
	filterID := r.FormValue("DBInstanceIdentifier")

	if filterID != "" {
		inst, aerr := h.store.getDBInstance(r.Context(), filterID)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBInstancesResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBInstancesResult{
				DBInstances: xmlDBInstances{Items: []xmlDBInstance{h.toXMLDBInstance(r.Context(), inst)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listDBInstances(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlDBInstance, 0, len(all))
	for _, inst := range all {
		items = append(items, h.toXMLDBInstance(r.Context(), inst))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBInstancesResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBInstancesResult{
			DBInstances: xmlDBInstances{Items: items},
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteDBInstance ─────────────────────────────────────────────────────────

// DeleteDBInstance deletes a DB instance. It immediately transitions to
// "deleting" (matching AWS behaviour), then asynchronously stops/removes the
// Docker container (if any) and removes the record from the store.
// All Docker cleanup steps are best-effort and fault-tolerant: a missing or
// already-stopped container will not block deletion.
func (h *Handler) DeleteDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBInstanceIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBInstanceIdentifier is required"))
		return
	}

	inst, aerr := h.store.getDBInstance(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Snapshot the fields we need for async cleanup before mutating the record.
	containerID := inst.DockerContainerID
	hostPort := inst.HostPort

	// Mark as deleting immediately — this is what AWS returns in the response.
	inst.DBInstanceStatus = "deleting"
	if aerr := h.store.putDBInstance(r.Context(), inst); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.RDSInstanceDeleted, events.ResourcePayload{Name: id})

	// Return the "deleting" response to the caller right away (AWS does the same).
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteDBInstanceResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDeleteDBInstanceResult{
			DBInstance: h.toXMLDBInstance(r.Context(), inst),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})

	// Cancel any in-flight health check so it doesn't race with cleanup.
	region := h.store.region(r.Context())
	h.scheduler.CancelScoped(region, id, "health")

	// Stop the container immediately (async, non-blocking).
	if h.gc != nil && containerID != "" {
		h.gc.StopNow(containerID)
		h.gc.ScheduleRemove(containerID)
	}
	// Release port reservation.
	if hostPort > 0 {
		_ = h.store.releasePort(r.Context(), hostPort) //nolint:errcheck
	}

	// Schedule async store record deletion. The GC handles the Docker
	// container cleanup independently — this just removes the DB record.
	h.scheduler.AfterScoped(region, id, "delete", 50*time.Millisecond, func(ctx context.Context) {
		if aerr := h.store.deleteDBInstance(ctx, id); aerr != nil {
			h.log.Warn("failed to delete RDS instance record", zap.String("instance", id), zap.Error(aerr))
		}
	})
}

// ── DescribeDBEngineVersions ─────────────────────────────────────────────────

type engineVersionEntry struct {
	Engine                     string
	EngineVersion              string
	DBParameterGroupFamily     string
	DBEngineDescription        string
	DBEngineVersionDescription string
}

var allEngineVersions = []engineVersionEntry{
	{"mysql", "8.0", "mysql8.0", "MySQL Community Edition", "MySQL 8.0"},
	{"mysql", "5.7", "mysql5.7", "MySQL Community Edition", "MySQL 5.7"},
	{"postgres", "16.1", "postgres16", "PostgreSQL", "PostgreSQL 16.1"},
	{"postgres", "15.5", "postgres15", "PostgreSQL", "PostgreSQL 15.5"},
	{"postgres", "14.11", "postgres14", "PostgreSQL", "PostgreSQL 14.11"},
	{"mariadb", "11.4", "mariadb11.4", "MariaDB Community Edition", "MariaDB 11.4"},
	{"mariadb", "10.11", "mariadb10.11", "MariaDB Community Edition", "MariaDB 10.11"},
	{"aurora-mysql", "3.04", "aurora-mysql8.0", "Aurora MySQL", "Aurora MySQL 3.04"},
	{"aurora-mysql", "2.11", "aurora-mysql5.7", "Aurora MySQL", "Aurora MySQL 2.11"},
	{"aurora-postgresql", "15.4", "aurora-postgresql15", "Aurora PostgreSQL", "Aurora PostgreSQL 15.4"},
	{"aurora-postgresql", "14.11", "aurora-postgresql14", "Aurora PostgreSQL", "Aurora PostgreSQL 14.11"},
}

// DescribeDBEngineVersions returns the supported engine versions.
func (h *Handler) DescribeDBEngineVersions(w http.ResponseWriter, r *http.Request) {
	filterEngine := r.FormValue("Engine")

	items := make([]xmlDBEngineVersion, 0, len(allEngineVersions))
	for _, ev := range allEngineVersions {
		if filterEngine != "" && ev.Engine != filterEngine {
			continue
		}
		items = append(items, xmlDBEngineVersion(ev))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBEngineVersionsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBEngineVersionsResult{
			DBEngineVersions: xmlDBEngineVersions{Items: items},
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// toXMLDBInstance converts a stored DBInstance to the XML response type.
// MasterUserPassword is intentionally omitted (AWS never returns it).
//
// The endpoint is re-minted for this caller rather than replayed from the
// record: the stored form is canonical, and which name and port are dialable
// depends on who is asking. Every response path goes through here, so the two
// dispatch paths (Query XML and typed) cannot disagree.
func (h *Handler) toXMLDBInstance(ctx context.Context, inst *DBInstance) xmlDBInstance {
	var ep xmlEndpoint
	if inst.Endpoint != nil {
		address, port := h.instanceEndpointFor(ctx, inst)
		ep = xmlEndpoint{Address: address, Port: port}
	}
	return xmlDBInstance{
		DBInstanceIdentifier: inst.DBInstanceIdentifier,
		DBInstanceClass:      inst.DBInstanceClass,
		Engine:               inst.Engine,
		EngineVersion:        inst.EngineVersion,
		DBInstanceStatus:     inst.DBInstanceStatus,
		MasterUsername:       inst.MasterUsername,
		DBName:               inst.DBName,
		Endpoint:             ep,
		AllocatedStorage:     inst.AllocatedStorage,
		DBInstanceArn:        inst.DBInstanceArn,
		InstanceCreateTime:   inst.InstanceCreateTime,
		MultiAZ:              inst.MultiAZ,
		StorageType:          inst.StorageType,
		DBClusterIdentifier:  inst.DBClusterIdentifier,
	}
}

// toXMLDBSubnetGroup converts a stored DBSubnetGroup to the XML response type.
func toXMLDBSubnetGroup(sg *DBSubnetGroup) xmlDBSubnetGroup {
	subnets := make([]xmlSubnet, 0, len(sg.SubnetIds))
	for _, id := range sg.SubnetIds {
		subnets = append(subnets, xmlSubnet{SubnetIdentifier: id})
	}
	return xmlDBSubnetGroup{
		DBSubnetGroupName:        sg.DBSubnetGroupName,
		DBSubnetGroupDescription: sg.DBSubnetGroupDescription,
		DBSubnetGroupArn:         sg.DBSubnetGroupArn,
		VpcId:                    sg.VpcId,
		SubnetIds:                xmlSubnets{Items: subnets},
		Status:                   sg.Status,
	}
}

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

// ── Cluster helpers ───────────────────────────────────────────────────────────

// addInstanceToCluster atomically adds a DB instance as a member of the given
// Aurora cluster. The first instance added becomes the cluster writer.
// Errors are logged and not surfaced — the instance creation response has
// already been committed at this point.
func (h *Handler) addInstanceToCluster(ctx context.Context, clusterID, instanceID string) {
	cluster, aerr := h.store.getDBCluster(ctx, clusterID)
	if aerr != nil {
		h.log.Warn("addInstanceToCluster: cluster not found",
			zap.String("cluster", clusterID), zap.Error(aerr))
		return
	}
	isWriter := len(cluster.DBClusterMembers) == 0
	cluster.DBClusterMembers = append(cluster.DBClusterMembers, DBClusterMember{
		DBInstanceIdentifier:          instanceID,
		IsClusterWriter:               isWriter,
		DBClusterParameterGroupStatus: "in-sync",
		PromotionTier:                 1,
	})
	if aerr := h.store.putDBCluster(ctx, cluster); aerr != nil {
		h.log.Warn("addInstanceToCluster: failed to update cluster",
			zap.String("cluster", clusterID), zap.Error(aerr))
	}
}

// ── Docker helpers ───────────────────────────────────────────────────────────

// setContainerDialTarget inspects the container after start and records how
// *Overcast* reaches it: the container's IP on the RDS Docker network when
// Overcast runs inside a container, otherwise 127.0.0.1 with the host port
// binding.
//
// It deliberately leaves Endpoint alone. Endpoint is what clients are told, and
// a raw address is dialable by exactly one party — the container IP means
// nothing on the host, and 127.0.0.1 means "yourself" inside a sibling
// container, which is how an ECS task ends up connecting to itself. The
// endpoint stays the AWS-shaped hostname and is rendered per caller on the way
// out (endpoint.go); this pair is for health checks only.
func (h *Handler) setContainerDialTarget(ctx context.Context, inst *DBInstance, ecfg engineEnv) {
	network := h.network()

	// When overcast itself runs inside Docker, reach the DB over the network
	// they share.
	if _, err := os.Stat("/.dockerenv"); err == nil {
		// Attach overcast's container to the RDS network (idempotent).
		hostname, _ := os.Hostname()
		if hostname != "" {
			_ = h.docker.ConnectNetwork(ctx, network, hostname)
		}

		info, err := h.docker.InspectContainer(ctx, inst.DockerContainerID)
		if err == nil {
			if ep, ok := info.NetworkSettings.Networks[network]; ok && ep.IPAddress != "" {
				inst.DialAddress = ep.IPAddress
				inst.DialPort = ecfg.ContainerPort
				return
			}
		}
	}

	// Native mode — use host port binding on localhost.
	inst.DialAddress = "127.0.0.1"
	inst.DialPort = inst.HostPort
}

// startDBContainer creates (or reuses) and starts a Docker container for the
// given DB instance. Updates inst.DockerContainerID, inst.HostPort and the
// health-check dial target in place.
//
// If a container with the expected name already exists (e.g. overcast was
// restarted while containers were kept running), we reuse it rather than
// failing or creating a duplicate. The existing container's host port binding
// is read from the inspect response so the stored port stays accurate.
func (h *Handler) startDBContainer(ctx context.Context, inst *DBInstance) error {
	// Resolve Docker image.
	image, matched, ok := resolveEngineImage(inst.Engine, inst.EngineVersion)
	if !ok {
		return fmt.Errorf("no image for engine %q version %q", inst.Engine, inst.EngineVersion)
	}
	if matched != inst.EngineVersion {
		h.log.Info("RDS: engine version has no image of its own, using the nearest one",
			zap.String("instance", inst.DBInstanceIdentifier),
			zap.String("engine", inst.Engine),
			zap.String("version", inst.EngineVersion),
			zap.String("served_by", matched),
			zap.String("image", image))
	}

	containerName := "overcast-rds-" + inst.DBInstanceIdentifier
	ecfg := engineEnvConfig[inst.Engine]
	containerPort := fmt.Sprintf("%d/tcp", ecfg.ContainerPort)

	// The DNS names this container must answer to, on every network a caller
	// might reach it from. Same set for each attachment below.
	aliases := h.instanceEndpointAliases(h.store.region(ctx), inst)

	// Check whether a container with this name already exists — this happens
	// after an overcast restart when RDSKeepContainers=true (or the process
	// was killed before cleanup completed).
	// We verify overcast labels before reusing to avoid accidentally attaching
	// to a user-created container that happens to share the same name.
	if existing, err := h.docker.GetContainerByName(ctx, containerName); err == nil && existing != nil {
		if !existing.HasOvercastLabels("rds", inst.DBInstanceIdentifier) {
			return fmt.Errorf("container %q exists but is not an overcast-managed RDS container for instance %q — refusing to reuse",
				containerName, inst.DBInstanceIdentifier)
		}

		h.log.Info("RDS: reusing existing container",
			zap.String("instance", inst.DBInstanceIdentifier),
			zap.String("container", existing.ID),
			zap.String("state", existing.State.Status))

		// Recover the host port from the existing bindings.
		hostPort := 0
		if bindings, ok := existing.NetworkSettings.Ports[containerPort]; ok && len(bindings) > 0 {
			if p, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				hostPort = p
			}
		}

		// If we can't read the port (shouldn't happen) allocate a new one.
		if hostPort == 0 {
			portBase := h.cfg.RDSPortBase
			if portBase == 0 {
				portBase = 33060
			}
			if hp, aerr := h.store.allocatePort(ctx, inst.DBInstanceIdentifier, portBase); aerr == nil {
				hostPort = hp
			}
		} else {
			// Record port in the port-allocation namespace so it's not reused.
			h.store.allocatePortFixed(ctx, inst.DBInstanceIdentifier, hostPort) //nolint:errcheck
		}

		// Ensure the container is running.
		if !existing.State.Running {
			if err := h.docker.StartContainer(ctx, existing.ID); err != nil {
				return fmt.Errorf("start existing container: %w", err)
			}
		}

		inst.DockerContainerID = existing.ID
		inst.HostPort = hostPort
		h.connectToComputeNetworks(ctx, existing.ID, aliases)
		h.setContainerDialTarget(ctx, inst, ecfg)
		return nil
	}

	// No existing container — allocate a port and create a fresh one.
	portBase := h.cfg.RDSPortBase
	if portBase == 0 {
		portBase = 33060
	}
	hostPort, aerr := h.store.allocatePort(ctx, inst.DBInstanceIdentifier, portBase)
	if aerr != nil {
		return fmt.Errorf("allocate port: %s", aerr.Message)
	}

	// Pull image (deduplicated per process lifetime).
	if err := h.puller.Ensure(ctx, image); err != nil {
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("pull image: %w", err)
	}

	// Create network (idempotent).
	network := h.network()
	if _, err := h.docker.CreateNetwork(ctx, network); err != nil {
		h.log.Warn("RDS: failed to create network (may already exist)",
			zap.String("network", network), zap.Error(err))
	}

	// Build engine-specific env vars.
	env := []string{ecfg.PasswordVar + "=" + inst.MasterUserPassword}
	dbName := inst.DBName
	if dbName == "" {
		dbName = "test"
	}
	env = append(env, ecfg.DatabaseVar+"="+dbName)

	// Set the superuser name when the engine supports it (Postgres).
	if ecfg.UserVar != "" {
		env = append(env, ecfg.UserVar+"="+inst.MasterUsername)
	}

	// For MySQL/MariaDB, create an additional non-root user when MasterUsername
	// is not "root". The root password is already set via MYSQL_ROOT_PASSWORD.
	if (inst.Engine == "mysql" || inst.Engine == "aurora-mysql") && inst.MasterUsername != "root" {
		env = append(env, "MYSQL_USER="+inst.MasterUsername, "MYSQL_PASSWORD="+inst.MasterUserPassword)
	}
	if inst.Engine == "mariadb" && inst.MasterUsername != "root" {
		env = append(env, "MARIADB_USER="+inst.MasterUsername, "MARIADB_PASSWORD="+inst.MasterUserPassword)
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        image,
			Env:          env,
			ExposedPorts: map[string]struct{}{containerPort: {}},
			Labels:       docker.ManagedLabels("rds", inst.DBInstanceIdentifier),
		},
		// No AutoRemove: StopDBInstance stops this container on purpose and keeps
		// its ID so StartDBInstance can bring it back, which AutoRemove makes
		// impossible by deleting it the moment it exits. DeleteDBInstance owns
		// removal instead, via the GC.
		HostConfig: &docker.HostConfig{
			NetworkMode: network,
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointSettings{
				network: {Aliases: aliases},
			},
		},
	}

	// The puller retries once when the image was removed behind our back
	// (docker rmi after the recorded pull) instead of failing until restart.
	containerID, err := h.puller.CreateContainerWithRetry(ctx, containerName, req)
	if err != nil {
		// A conflict means the name appeared between our GetContainerByName check
		// and CreateContainer — race condition. Retry once by inspecting and reusing.
		if docker.IsConflict(err) {
			h.log.Warn("RDS: name conflict on create, retrying reuse",
				zap.String("instance", inst.DBInstanceIdentifier))
			h.store.releasePort(ctx, hostPort) //nolint:errcheck
			return h.startDBContainer(ctx, inst)
		}
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("create container: %w", err)
	}

	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("start container: %w", err)
	}
	if h.vpcResolver != nil && inst.VpcID != "" {
		status := h.vpcResolver.VPCNetworkStatus(ctx, inst.VpcID)
		switch status {
		case "", "ok", "shared", "remapped":
			if netID := h.vpcResolver.DockerNetworkForVpc(ctx, inst.VpcID); netID != "" {
				// Attach with the endpoint hostname as a DNS alias, as the
				// Lambda-network attachment below does. Without the alias the
				// instance is reachable on the VPC network but not resolvable
				// on it, so anything else in the VPC — an ECS task, most often
				// — gets Overcast's own address for the endpoint name and
				// connects to a port nothing is listening on.
				if err := h.docker.ConnectNetworkWithAliases(ctx, netID, containerID, aliases); err != nil {
					h.docker.RemoveContainerForce(containerID) //nolint:errcheck
					h.store.releasePort(ctx, hostPort)         //nolint:errcheck
					return fmt.Errorf("connect container to VPC network %s: %w", netID, err)
				}
			}
		case "conflict", "unbacked":
			h.docker.RemoveContainerForce(containerID) //nolint:errcheck
			h.store.releasePort(ctx, hostPort)         //nolint:errcheck
			return fmt.Errorf("RDS VPC %s is not launchable (network status=%s)", inst.VpcID, status)
		default:
			h.docker.RemoveContainerForce(containerID) //nolint:errcheck
			h.store.releasePort(ctx, hostPort)         //nolint:errcheck
			return fmt.Errorf("RDS VPC %s is not launchable (network status=%s)", inst.VpcID, status)
		}
	}

	inst.DockerContainerID = containerID
	inst.HostPort = hostPort
	h.connectToComputeNetworks(ctx, containerID, aliases)
	h.setContainerDialTarget(ctx, inst, ecfg)
	return nil
}

func (h *Handler) region() string {
	if h.cfg != nil && h.cfg.Region != "" {
		return h.cfg.Region
	}
	return "us-east-1"
}

func (h *Handler) externalHostname() string {
	if h.cfg != nil {
		return h.cfg.ExternalHostname()
	}
	return "localhost"
}

// connectToComputeNetworks attaches the DB container to the networks emulated
// compute runs on, advertising the instance's endpoint hostname as a DNS alias
// on each. That alias is what lets a Lambda function or an ECS task connect to
// the endpoint name the API handed them: Docker's embedded resolver answers
// from the aliases on the caller's own network before forwarding anything
// upstream to Overcast.
//
// A task placed in a VPC reaches the instance over the VPC network instead —
// see the aliased attachment in startDBContainer. This covers the rest: a task
// or function with no VPC of its own.
func (h *Handler) connectToComputeNetworks(ctx context.Context, containerID string, aliases []string) {
	if h.cfg == nil || len(aliases) == 0 {
		return
	}
	for _, network := range []string{h.cfg.LambdaNetwork, h.cfg.ECSNetwork} {
		if network == "" || network == h.network() {
			continue
		}
		if err := h.docker.ConnectNetworkWithAliases(ctx, network, containerID, aliases); err != nil {
			h.log.Warn("RDS: failed to attach container to compute network for DNS aliases",
				zap.String("network", network), zap.String("container", containerID), zap.Error(err))
		}
	}
}

func (h *Handler) network() string {
	if h.cfg != nil && h.cfg.RDSNetwork != "" {
		return h.cfg.RDSNetwork
	}
	return "overcast_rds"
}

// scheduleHealthCheck polls TCP connectivity to the DB container and transitions
// the instance from "creating" (or "starting") to "available" once it responds.
// region is the region the instance is stored under — the callbacks run outside
// any request context, so the store lookup would otherwise hit the default
// region and silently no-op for instances created elsewhere.
func (h *Handler) scheduleHealthCheck(region, instanceID, host string, port int) {
	const maxRetries = 30
	var attempt int
	var check func(ctx context.Context)
	check = func(ctx context.Context) {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			// DB is ready — transition to available.
			got, aerr := h.store.getDBInstance(ctx, instanceID)
			if aerr != nil {
				return
			}
			if got.DBInstanceStatus == "creating" || got.DBInstanceStatus == "starting" {
				got.DBInstanceStatus = "available"
				h.store.putDBInstance(ctx, got) //nolint:errcheck
			}
			return
		}
		if attempt < maxRetries {
			h.scheduler.AfterScoped(region, instanceID, "health", 2*time.Second, check)
		} else {
			h.log.Warn("RDS health check timed out", zap.String("instance", instanceID), zap.Int("attempts", attempt))
			// Transition to available anyway so the API is usable.
			got, aerr := h.store.getDBInstance(ctx, instanceID)
			if aerr != nil {
				return
			}
			if got.DBInstanceStatus == "creating" || got.DBInstanceStatus == "starting" {
				got.DBInstanceStatus = "available"
				h.store.putDBInstance(ctx, got) //nolint:errcheck
			}
		}
	}
	h.scheduler.AfterScoped(region, instanceID, "health", 1*time.Second, check)
}

// launchDBContainerAsync starts the instance's DB container in the background
// so the image pull and container start do not block the request path. ctx is
// only used to capture the request's region; the work runs on a detached
// context.
//
// The container start takes real time, so the instance may have been stopped
// or deleted by the time it finishes. Persisting the pre-start snapshot would
// overwrite those transitions (StartDBInstance then rejects a "stopped"
// instance whose status reverted), so only the container-owned fields are
// merged into a fresh read.
func (h *Handler) launchDBContainerAsync(ctx context.Context, instID string) {
	region := h.store.region(ctx)
	h.dockerWg.Add(1)
	go func() {
		defer h.dockerWg.Done()
		bgCtx := middleware.ContextWithRegion(context.Background(), region)
		got, aerr := h.store.getDBInstance(bgCtx, instID)
		if aerr != nil || got == nil {
			return
		}
		if err := h.startDBContainer(bgCtx, got); err != nil {
			h.log.Warn("failed to start Docker container for RDS instance — falling back to metadata-only",
				zap.String("instance", instID), zap.Error(err))
			return
		}
		fresh, aerr := h.store.getDBInstance(bgCtx, instID)
		if aerr != nil || fresh == nil || fresh.DBInstanceStatus == "deleting" {
			h.teardownOrphanedContainer(bgCtx, instID, got.DockerContainerID, got.HostPort)
			return
		}
		fresh.DockerContainerID = got.DockerContainerID
		fresh.HostPort = got.HostPort
		fresh.Endpoint = got.Endpoint
		if aerr := h.store.putDBInstance(bgCtx, fresh); aerr != nil {
			h.log.Warn("RDS: persist post-start instance",
				zap.String("instance", instID), zap.String("error", aerr.Message))
			return
		}
		switch fresh.DBInstanceStatus {
		case "stopping", "stopped":
			// Instance was stopped while the container was starting — bring
			// the container down to match; it restarts on StartDBInstance.
			if h.gc != nil {
				h.gc.StopNow(fresh.DockerContainerID)
			} else {
				_ = h.docker.StopContainer(bgCtx, fresh.DockerContainerID, 10)
			}
		default:
			healthHost, healthPort := dialTarget(fresh)
			h.scheduleHealthCheck(region, instID, healthHost, healthPort)
		}
	}()
}

// teardownOrphanedContainer removes a container whose DB instance record was
// deleted while the container was still starting. DeleteDBInstance could not
// stop it — the container ID had not been persisted yet — so the start
// goroutine owns the cleanup, including the port it allocated.
func (h *Handler) teardownOrphanedContainer(ctx context.Context, instanceID, containerID string, hostPort int) {
	h.log.Info("RDS: instance deleted while its container was starting — removing container",
		zap.String("instance", instanceID), zap.String("container", containerID))
	if containerID != "" {
		if h.gc != nil {
			h.gc.StopNow(containerID)
			h.gc.ScheduleRemove(containerID)
		} else if h.docker != nil {
			_ = h.docker.RemoveContainerForce(containerID)
		}
	}
	if hostPort > 0 {
		if aerr := h.store.releasePort(ctx, hostPort); aerr != nil {
			h.log.Warn("RDS cleanup: release port",
				zap.String("instance", instanceID), zap.Int("port", hostPort), zap.Error(aerr))
		}
	}
}

// cleanupDBContainer releases the port reservation for a DB instance that had
// no Docker container (e.g. it was created before Docker was available).
// Docker container stop/remove is handled by the GC — this function is only
// for port cleanup. Kept as a separate function for clarity.
//
//nolint:unused // Kept for explicit Docker cleanup call sites.
func (h *Handler) cleanupDBContainer(ctx context.Context, instanceID, containerID string, hostPort int) {
	if hostPort > 0 {
		if aerr := h.store.releasePort(ctx, hostPort); aerr != nil {
			h.log.Warn("RDS cleanup: release port",
				zap.String("instance", instanceID), zap.Int("port", hostPort), zap.Error(aerr))
		}
	}
}
