package rds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsDBInstances     = "rds:instances"
	nsClusters        = "rds:clusters"
	nsPorts           = "rds:ports"
	nsSubnetGroups    = "rds:subnet-groups"
	nsParameterGroups = "rds:parameter-groups"
)

// DBInstance represents a stored RDS DB instance.
type DBInstance struct {
	DBInstanceIdentifier string    `json:"DBInstanceIdentifier"`
	DBInstanceClass      string    `json:"DBInstanceClass"`
	Engine               string    `json:"Engine"`
	EngineVersion        string    `json:"EngineVersion"`
	DBInstanceStatus     string    `json:"DBInstanceStatus"`
	MasterUsername       string    `json:"MasterUsername"`
	MasterUserPassword   string    `json:"MasterUserPassword,omitempty"`
	DBName               string    `json:"DBName,omitempty"`
	AllocatedStorage     int       `json:"AllocatedStorage"`
	Endpoint             *Endpoint `json:"Endpoint,omitempty"`
	DBInstanceArn        string    `json:"DBInstanceArn"`
	InstanceCreateTime   string    `json:"InstanceCreateTime,omitempty"`
	MultiAZ              bool      `json:"MultiAZ"`
	StorageType          string    `json:"StorageType"`
	Port                 int       `json:"Port"`
	DockerContainerID    string    `json:"DockerContainerID,omitempty"`
	HostPort             int       `json:"HostPort,omitempty"`
	DBClusterIdentifier  string    `json:"DBClusterIdentifier,omitempty"`
	DBSubnetGroupName    string    `json:"DBSubnetGroupName,omitempty"`
	VpcID                string    `json:"VpcId,omitempty"`
}

// DBCluster represents a stored Aurora DB cluster.
type DBCluster struct {
	DBClusterIdentifier string            `json:"DBClusterIdentifier"`
	DBClusterArn        string            `json:"DBClusterArn"`
	Engine              string            `json:"Engine"`
	EngineVersion       string            `json:"EngineVersion"`
	Status              string            `json:"Status"`
	MasterUsername      string            `json:"MasterUsername"`
	DatabaseName        string            `json:"DatabaseName,omitempty"`
	Port                int               `json:"Port"`
	Endpoint            string            `json:"Endpoint,omitempty"`
	ReaderEndpoint      string            `json:"ReaderEndpoint,omitempty"`
	MultiAZ             bool              `json:"MultiAZ"`
	StorageType         string            `json:"StorageType"`
	ClusterCreateTime   string            `json:"ClusterCreateTime,omitempty"`
	DBClusterMembers    []DBClusterMember `json:"DBClusterMembers,omitempty"`
}

// DBClusterMember represents one DB instance that belongs to an Aurora cluster.
type DBClusterMember struct {
	DBInstanceIdentifier          string `json:"DBInstanceIdentifier"`
	IsClusterWriter               bool   `json:"IsClusterWriter"`
	DBClusterParameterGroupStatus string `json:"DBClusterParameterGroupStatus"`
	PromotionTier                 int    `json:"PromotionTier"`
}

// Endpoint represents the connection endpoint for a DB instance.
type Endpoint struct {
	Address string `json:"Address"`
	Port    int    `json:"Port"`
}

// DBSubnetGroup represents a stored RDS DB subnet group.
type DBSubnetGroup struct {
	DBSubnetGroupName        string   `json:"DBSubnetGroupName"`
	DBSubnetGroupDescription string   `json:"DBSubnetGroupDescription"`
	DBSubnetGroupArn         string   `json:"DBSubnetGroupArn"`
	VpcId                    string   `json:"VpcId"`
	SubnetIds                []string `json:"SubnetIds"`
	Status                   string   `json:"Status"`
}

// DBParameterGroup represents a stored RDS DB parameter group.
type DBParameterGroup struct {
	DBParameterGroupName   string `json:"dbParameterGroupName"`
	DBParameterGroupFamily string `json:"dbParameterGroupFamily"`
	Description            string `json:"description"`
	DBParameterGroupArn    string `json:"dbParameterGroupArn"`
}

type rdsStore struct {
	mu            sync.Mutex
	store         state.Store
	defaultRegion string
}

func newRDSStore(store state.Store, defaultRegion string) *rdsStore {
	return &rdsStore{store: store, defaultRegion: defaultRegion}
}

func (s *rdsStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

func (s *rdsStore) putDBInstance(ctx context.Context, inst *DBInstance) *protocol.AWSError {
	raw, err := json.Marshal(inst)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsDBInstances, serviceutil.RegionKey(s.region(ctx), inst.DBInstanceIdentifier), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *rdsStore) getDBInstance(ctx context.Context, id string) (*DBInstance, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsDBInstances, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errDBInstanceNotFound(id)
	}
	var inst DBInstance
	if err := json.Unmarshal([]byte(raw), &inst); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &inst, nil
}

func (s *rdsStore) deleteDBInstance(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsDBInstances, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *rdsStore) listDBInstances(ctx context.Context) ([]*DBInstance, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsDBInstances, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	instances := make([]*DBInstance, 0, len(pairs))
	for _, p := range pairs {
		var inst DBInstance
		if err := json.Unmarshal([]byte(p.Value), &inst); err != nil {
			continue
		}
		instances = append(instances, &inst)
	}
	return instances, nil
}

// ── Errors ────────────────────────────────────────────────────────────────────

func errDBInstanceNotFound(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBInstanceNotFound",
		Message:    fmt.Sprintf("DBInstance %s not found.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBInstanceAlreadyExists(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBInstanceAlreadyExists",
		Message:    fmt.Sprintf("DB instance already exists: %s", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errInvalidParameterValue(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
	}
}

func errInvalidDBInstanceState(id, msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidDBInstanceState",
		Message:    fmt.Sprintf("Instance %s is not in a valid state: %s", id, msg),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBSubnetGroupNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBSubnetGroupNotFoundFault",
		Message:    fmt.Sprintf("DBSubnetGroup '%s' not found.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBSubnetGroupAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBSubnetGroupAlreadyExistsFault",
		Message:    fmt.Sprintf("DB Subnet Group '%s' already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBParameterGroupNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBParameterGroupNotFound",
		Message:    fmt.Sprintf("DBParameterGroup '%s' not found.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBParameterGroupAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBParameterGroupAlreadyExists",
		Message:    fmt.Sprintf("A DB parameter group named '%s' already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBClusterNotFound(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBClusterNotFoundFault",
		Message:    fmt.Sprintf("DBCluster %s not found.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errDBClusterAlreadyExists(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "DBClusterAlreadyExistsFault",
		Message:    fmt.Sprintf("DB cluster already exists: %s", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

// ── Port allocation ───────────────────────────────────────────────────────────

// allocatePort scans existing port allocations and claims the first free port
// in [portBase, portBase+1000). Protected by a mutex to prevent concurrent
// callers from claiming the same port.
func (s *rdsStore) allocatePort(ctx context.Context, instanceID string, portBase int) (int, *protocol.AWSError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pairs, err := s.store.Scan(ctx, nsPorts, "")
	if err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}

	used := make(map[int]bool, len(pairs))
	for _, p := range pairs {
		port, parseErr := strconv.Atoi(p.Key)
		if parseErr == nil {
			used[port] = true
		}
	}

	for port := portBase; port < portBase+1000; port++ {
		if !used[port] {
			if err := s.store.Set(ctx, nsPorts, strconv.Itoa(port), instanceID); err != nil {
				return 0, protocol.Wrap(protocol.ErrInternalError, err)
			}
			return port, nil
		}
	}
	return 0, &protocol.AWSError{
		Code:       "InternalFailure",
		Message:    "no free port available for RDS instance",
		HTTPStatus: http.StatusInternalServerError,
	}
}

func (s *rdsStore) releasePort(ctx context.Context, port int) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsPorts, strconv.Itoa(port)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// allocatePortFixed records a specific port as allocated for an instance.
// Used when reusing an existing container whose host port is already known.
// If the port is already recorded (idempotent), the existing record is overwritten.
func (s *rdsStore) allocatePortFixed(ctx context.Context, instanceID string, port int) *protocol.AWSError {
	if err := s.store.Set(ctx, nsPorts, strconv.Itoa(port), instanceID); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ── SubnetGroup store ─────────────────────────────────────────────────────────

func (s *rdsStore) putDBSubnetGroup(ctx context.Context, sg *DBSubnetGroup) *protocol.AWSError {
	raw, err := json.Marshal(sg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), sg.DBSubnetGroupName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *rdsStore) getDBSubnetGroup(ctx context.Context, name string) (*DBSubnetGroup, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !ok {
		return nil, errDBSubnetGroupNotFound(name)
	}
	var sg DBSubnetGroup
	if err := json.Unmarshal([]byte(raw), &sg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &sg, nil
}

func (s *rdsStore) listDBSubnetGroups(ctx context.Context) ([]*DBSubnetGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]*DBSubnetGroup, 0, len(pairs))
	for _, p := range pairs {
		var sg DBSubnetGroup
		if err := json.Unmarshal([]byte(p.Value), &sg); err != nil {
			continue
		}
		groups = append(groups, &sg)
	}
	return groups, nil
}

func (s *rdsStore) deleteDBSubnetGroup(ctx context.Context, name string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), name)
	if err := s.store.Delete(ctx, nsSubnetGroups, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ── ParameterGroup store ──────────────────────────────────────────────────────

func (s *rdsStore) putDBParameterGroup(ctx context.Context, pg *DBParameterGroup) *protocol.AWSError {
	raw, err := json.Marshal(pg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), pg.DBParameterGroupName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *rdsStore) getDBParameterGroup(ctx context.Context, name string) (*DBParameterGroup, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !ok {
		return nil, errDBParameterGroupNotFound(name)
	}
	var pg DBParameterGroup
	if err := json.Unmarshal([]byte(raw), &pg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &pg, nil
}

func (s *rdsStore) listDBParameterGroups(ctx context.Context) ([]*DBParameterGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]*DBParameterGroup, 0, len(pairs))
	for _, p := range pairs {
		var pg DBParameterGroup
		if err := json.Unmarshal([]byte(p.Value), &pg); err != nil {
			continue
		}
		groups = append(groups, &pg)
	}
	return groups, nil
}

func (s *rdsStore) deleteDBParameterGroup(ctx context.Context, name string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), name)
	if err := s.store.Delete(ctx, nsParameterGroups, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ── Cluster store ─────────────────────────────────────────────────────────────

func (s *rdsStore) putDBCluster(ctx context.Context, c *DBCluster) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), c.DBClusterIdentifier), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *rdsStore) getDBCluster(ctx context.Context, id string) (*DBCluster, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errDBClusterNotFound(id)
	}
	var c DBCluster
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &c, nil
}

func (s *rdsStore) deleteDBCluster(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *rdsStore) listDBClusters(ctx context.Context) ([]*DBCluster, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	clusters := make([]*DBCluster, 0, len(pairs))
	for _, p := range pairs {
		var c DBCluster
		if err := json.Unmarshal([]byte(p.Value), &c); err != nil {
			continue
		}
		clusters = append(clusters, &c)
	}
	return clusters, nil
}
