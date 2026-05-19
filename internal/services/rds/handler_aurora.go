package rds

// handler_aurora.go implements Aurora DB cluster operations.
// Aurora MySQL and Aurora PostgreSQL are emulated using MySQL and PostgreSQL
// Docker images respectively — both engines use the same wire protocol as
// their open-source counterparts.

import (
	"context"
	"encoding/xml"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Aurora XML types ──────────────────────────────────────────────────────────

type xmlCreateDBClusterResponse struct {
	XMLName          xml.Name                  `xml:"CreateDBClusterResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlCreateDBClusterResult  `xml:"CreateDBClusterResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlCreateDBClusterResult struct {
	DBCluster xmlDBCluster `xml:"DBCluster"`
}

type xmlDeleteDBClusterResponse struct {
	XMLName          xml.Name                  `xml:"DeleteDBClusterResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlDeleteDBClusterResult  `xml:"DeleteDBClusterResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDeleteDBClusterResult struct {
	DBCluster xmlDBCluster `xml:"DBCluster"`
}

type xmlDescribeDBClustersResponse struct {
	XMLName          xml.Name                    `xml:"DescribeDBClustersResponse"`
	Xmlns            string                      `xml:"xmlns,attr"`
	Result           xmlDescribeDBClustersResult `xml:"DescribeDBClustersResult"`
	ResponseMetadata protocol.ResponseMetadata   `xml:"ResponseMetadata"`
}

type xmlDescribeDBClustersResult struct {
	DBClusters xmlDBClusters `xml:"DBClusters"`
}

type xmlDBClusters struct {
	Items []xmlDBCluster `xml:"DBCluster"`
}

type xmlDBCluster struct {
	DBClusterIdentifier string              `xml:"DBClusterIdentifier"`
	DBClusterArn        string              `xml:"DBClusterArn"`
	Engine              string              `xml:"Engine"`
	EngineVersion       string              `xml:"EngineVersion"`
	Status              string              `xml:"Status"`
	MasterUsername      string              `xml:"MasterUsername"`
	DatabaseName        string              `xml:"DatabaseName,omitempty"`
	Port                int                 `xml:"Port"`
	Endpoint            string              `xml:"Endpoint,omitempty"`
	ReaderEndpoint      string              `xml:"ReaderEndpoint,omitempty"`
	MultiAZ             bool                `xml:"MultiAZ"`
	StorageType         string              `xml:"StorageType"`
	ClusterCreateTime   string              `xml:"ClusterCreateTime,omitempty"`
	DBClusterMembers    xmlDBClusterMembers `xml:"DBClusterMembers"`
}

type xmlDBClusterMembers struct {
	Items []xmlDBClusterMember `xml:"DBClusterMember"`
}

type xmlDBClusterMember struct {
	DBInstanceIdentifier          string `xml:"DBInstanceIdentifier"`
	IsClusterWriter               bool   `xml:"IsClusterWriter"`
	DBClusterParameterGroupStatus string `xml:"DBClusterParameterGroupStatus"`
	PromotionTier                 int    `xml:"PromotionTier"`
}

// ── CreateDBCluster ───────────────────────────────────────────────────────────

// CreateDBCluster creates a new Aurora DB cluster. Only aurora-mysql and
// aurora-postgresql engines are accepted. The cluster is a logical resource —
// Docker containers are started when instances are added via CreateDBInstance.
func (h *Handler) CreateDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBClusterIdentifier is required"))
		return
	}

	engine := r.FormValue("Engine")
	if !auroraEngines[engine] {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue(
			"Engine must be one of: aurora-mysql, aurora-postgresql"))
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
	if _, aerr := h.store.getDBCluster(r.Context(), id); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errDBClusterAlreadyExists(id))
		return
	}

	engineVersion := r.FormValue("EngineVersion")
	if engineVersion == "" {
		engineVersion = defaultEngineVersions[engine]
	}

	storageType := r.FormValue("StorageType")
	if storageType == "" {
		storageType = "aurora"
	}

	multiAZ := r.FormValue("MultiAZ") == "true"
	dbName := r.FormValue("DatabaseName")
	region := h.store.region(r.Context())
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "cluster:"+id)
	now := h.clk.Now().UTC().Format(time.RFC3339)

	cluster := &DBCluster{
		DBClusterIdentifier: id,
		DBClusterArn:        arn,
		Engine:              engine,
		EngineVersion:       engineVersion,
		Status:              "creating",
		MasterUsername:      masterUser,
		DatabaseName:        dbName,
		Port:                defaultPorts[engine],
		Endpoint:            id + ".cluster-rw." + region + ".rds." + h.cfg.ExternalHostname(),
		ReaderEndpoint:      id + ".cluster-ro." + region + ".rds." + h.cfg.ExternalHostname(),
		MultiAZ:             multiAZ,
		StorageType:         storageType,
		ClusterCreateTime:   now,
		DBClusterMembers:    []DBClusterMember{},
	}

	if aerr := h.store.putDBCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Schedule the metadata-only creating → available transition.
	clID := id
	h.scheduler.After(clID+":available", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBCluster(ctx, clID)
		if aerr != nil {
			return
		}
		if got.Status == "creating" {
			got.Status = "available"
			h.store.putDBCluster(ctx, got) //nolint:errcheck
		}
	})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeDBClusters ────────────────────────────────────────────────────────

// DescribeDBClusters returns Aurora DB clusters, optionally filtered by identifier.
func (h *Handler) DescribeDBClusters(w http.ResponseWriter, r *http.Request) {
	filterID := r.FormValue("DBClusterIdentifier")

	if filterID != "" {
		cluster, aerr := h.store.getDBCluster(r.Context(), filterID)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBClustersResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBClustersResult{
				DBClusters: xmlDBClusters{Items: []xmlDBCluster{toXMLDBCluster(cluster)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listDBClusters(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlDBCluster, 0, len(all))
	for _, c := range all {
		items = append(items, toXMLDBCluster(c))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBClustersResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBClustersResult{
			DBClusters: xmlDBClusters{Items: items},
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteDBCluster ───────────────────────────────────────────────────────────

// DeleteDBCluster deletes an Aurora DB cluster. The cluster is marked "deleting"
// immediately and removed asynchronously. Member instances are not automatically
// deleted — callers should delete instances first (matching AWS behaviour).
func (h *Handler) DeleteDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBClusterIdentifier is required"))
		return
	}

	cluster, aerr := h.store.getDBCluster(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	cluster.Status = "deleting"
	if aerr := h.store.putDBCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDeleteDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})

	// Remove the cluster record asynchronously.
	clID := id
	h.scheduler.After(clID+":delete", 50*time.Millisecond, func() {
		ctx := context.Background()
		if aerr := h.store.deleteDBCluster(ctx, clID); aerr != nil {
			h.log.Warn("failed to delete RDS cluster record",
				zap.String("cluster", clID), zap.Error(aerr))
		}
	})
}

// ── ModifyDBCluster ───────────────────────────────────────────────────────────

// ModifyDBCluster updates settings on an Aurora DB cluster.
func (h *Handler) ModifyDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBClusterIdentifier is required"))
		return
	}

	cluster, aerr := h.store.getDBCluster(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if v := r.FormValue("EngineVersion"); v != "" {
		cluster.EngineVersion = v
	}

	if aerr := h.store.putDBCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	type xmlModifyDBClusterResponse struct {
		XMLName          xml.Name                  `xml:"ModifyDBClusterResponse"`
		Xmlns            string                    `xml:"xmlns,attr"`
		Result           xmlCreateDBClusterResult  `xml:"ModifyDBClusterResult"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── StartDBCluster / StopDBCluster ────────────────────────────────────────────

// StartDBCluster starts a stopped Aurora DB cluster.
func (h *Handler) StartDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBClusterIdentifier is required"))
		return
	}

	cluster, aerr := h.store.getDBCluster(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if cluster.Status != "stopped" {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidDBClusterStateFault",
			Message:    "Cluster " + id + " is not in a stopped state.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	cluster.Status = "starting"
	if aerr := h.store.putDBCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Transition starting → available.
	clID := id
	h.scheduler.After(clID+":start", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBCluster(ctx, clID)
		if aerr != nil {
			return
		}
		if got.Status == "starting" {
			got.Status = "available"
			h.store.putDBCluster(ctx, got) //nolint:errcheck
		}
	})

	type xmlStartDBClusterResponse struct {
		XMLName          xml.Name                  `xml:"StartDBClusterResponse"`
		Xmlns            string                    `xml:"xmlns,attr"`
		Result           xmlCreateDBClusterResult  `xml:"StartDBClusterResult"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlStartDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// StopDBCluster stops a running Aurora DB cluster.
func (h *Handler) StopDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("DBClusterIdentifier")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBClusterIdentifier is required"))
		return
	}

	cluster, aerr := h.store.getDBCluster(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if cluster.Status != "available" {
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidDBClusterStateFault",
			Message:    "Cluster " + id + " is not in an available state.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	cluster.Status = "stopping"
	if aerr := h.store.putDBCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Transition stopping → stopped.
	clID := id
	h.scheduler.After(clID+":stop", 500*time.Millisecond, func() {
		ctx := context.Background()
		got, aerr := h.store.getDBCluster(ctx, clID)
		if aerr != nil {
			return
		}
		if got.Status == "stopping" {
			got.Status = "stopped"
			h.store.putDBCluster(ctx, got) //nolint:errcheck
		}
	})

	type xmlStopDBClusterResponse struct {
		XMLName          xml.Name                  `xml:"StopDBClusterResponse"`
		Xmlns            string                    `xml:"xmlns,attr"`
		Result           xmlCreateDBClusterResult  `xml:"StopDBClusterResult"`
		ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlStopDBClusterResponse{
		Xmlns: rdsXMLNS,
		Result: xmlCreateDBClusterResult{
			DBCluster: toXMLDBCluster(cluster),
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// toXMLDBCluster converts a stored DBCluster to the XML response type.
func toXMLDBCluster(c *DBCluster) xmlDBCluster {
	members := make([]xmlDBClusterMember, 0, len(c.DBClusterMembers))
	for _, m := range c.DBClusterMembers {
		members = append(members, xmlDBClusterMember(m))
	}
	return xmlDBCluster{
		DBClusterIdentifier: c.DBClusterIdentifier,
		DBClusterArn:        c.DBClusterArn,
		Engine:              c.Engine,
		EngineVersion:       c.EngineVersion,
		Status:              c.Status,
		MasterUsername:      c.MasterUsername,
		DatabaseName:        c.DatabaseName,
		Port:                c.Port,
		Endpoint:            c.Endpoint,
		ReaderEndpoint:      c.ReaderEndpoint,
		MultiAZ:             c.MultiAZ,
		StorageType:         c.StorageType,
		ClusterCreateTime:   c.ClusterCreateTime,
		DBClusterMembers:    xmlDBClusterMembers{Items: members},
	}
}
