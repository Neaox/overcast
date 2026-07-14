package rds

import (
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

func TestEndpointAliases_dbInstance(t *testing.T) {
	// Given: an RDS DB instance with its synthetic endpoint hostname.
	h := &Handler{cfg: &config.Config{Region: "us-east-1", Hostname: "localhost"}}
	inst := &DBInstance{DBInstanceIdentifier: "db-1", Endpoint: &Endpoint{Address: "db-1.us-east-1.rds.localhost", Port: 5432}}

	// When: aliases are built for Docker DNS.
	got := h.dbInstanceEndpointAliases(inst)

	// Then: Docker receives the exact endpoint hostname as an alias.
	want := []string{"db-1.us-east-1.rds.localhost"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
}

func TestEndpointAliases_dbInstancePreservesCanonicalNameWhenEndpointIsIP(t *testing.T) {
	// Given: Docker startup has already rewritten the stored endpoint to a direct container IP.
	h := &Handler{cfg: &config.Config{Region: "us-east-1", Hostname: "localhost"}}
	inst := &DBInstance{DBInstanceIdentifier: "db-1", Endpoint: &Endpoint{Address: "172.18.0.4", Port: 5432}}

	// When: aliases are built for Docker DNS.
	got := h.dbInstanceEndpointAliases(inst)

	// Then: the originally advertised synthetic endpoint is still registered.
	want := []string{"db-1.us-east-1.rds.localhost"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
}
