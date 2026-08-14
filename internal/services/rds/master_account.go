package rds

import (
	"strings"
)

const rdsSuperuserRole = "rds_superuser_role"

const mysqlBaseMasterPrivileges = "SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, RELOAD, PROCESS, " +
	"REFERENCES, INDEX, ALTER, SHOW DATABASES, CREATE TEMPORARY TABLES, LOCK TABLES, EXECUTE, " +
	"REPLICATION SLAVE, REPLICATION CLIENT, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, " +
	"CREATE USER, EVENT, TRIGGER"

const mysqlRoleMasterPrivileges = "CREATE ROLE, DROP ROLE, APPLICATION_PASSWORD_ADMIN, ROLE_ADMIN, SET_USER_ID, XA_RECOVER_ADMIN"

func mysqlBasePrivileges() []string { return strings.Split(mysqlBaseMasterPrivileges, ", ") }

func mysqlRolePrivileges() []string { return strings.Split(mysqlRoleMasterPrivileges, ", ") }

// masterAccountSpec is the engine-version contract used to initialize a
// MySQL-family master account. Container image selection is intentionally not
// part of it: AWS account behavior follows EngineVersion, while the image is
// only Overcast's mechanism for providing a compatible data plane.
type masterAccountSpec struct {
	AuthPlugin string
	RoleName   string
	Privileges []string
}

func mysqlMasterAccountFor(engine, version string) masterAccountSpec {
	spec := masterAccountSpec{Privileges: mysqlBasePrivileges()}
	switch engine {
	case "mariadb":
		if versionAtLeast(version, "11.4") {
			spec.Privileges = append(spec.Privileges, "SHOW CREATE ROUTINE")
		}
	case "mysql":
		if versionAtLeast(version, "8.0.34") || version == "8.0" {
			spec.AuthPlugin = "mysql_native_password"
		}
		if versionAtLeast(version, "8.4") {
			spec.AuthPlugin = "caching_sha2_password"
		}
		if versionAtLeast(version, "8.0.36") || version == "8.0" {
			spec.RoleName = rdsSuperuserRole
			spec.Privileges = append(spec.Privileges, mysqlRolePrivileges()...)
		}
		if versionAtLeast(version, "8.4") {
			spec.Privileges = removePrivilege(spec.Privileges, "SET_USER_ID")
			spec.Privileges = append(spec.Privileges, "FLUSH_OPTIMIZER_COSTS", "FLUSH_PRIVILEGES",
				"FLUSH_STATUS", "FLUSH_TABLES", "FLUSH_USER_RESOURCES", "SENSITIVE_VARIABLES_OBSERVER",
				"SESSION_VARIABLES_ADMIN", "SET_ANY_DEFINER", "SHOW_ROUTINE")
		}
	case "aurora-mysql":
		track := auroraMySQLTrackVersion(version)
		if majorOf(track) != "3" && majorOf(track) != "4" {
			return spec
		}
		spec.AuthPlugin = "mysql_native_password"
		if majorOf(track) == "4" {
			spec.AuthPlugin = "caching_sha2_password"
		}
		spec.RoleName = rdsSuperuserRole
		spec.Privileges = append(spec.Privileges, mysqlRolePrivileges()...)
		spec.Privileges = append(spec.Privileges, "CONNECTION_ADMIN")
		if majorOf(track) == "4" {
			spec.Privileges = removePrivilege(spec.Privileges, "SET_USER_ID")
			spec.Privileges = append(spec.Privileges, "ALLOW_NONEXISTENT_DEFINER", "FLUSH_OPTIMIZER_COSTS",
				"FLUSH_PRIVILEGES", "FLUSH_STATUS", "FLUSH_TABLES", "FLUSH_USER_RESOURCES",
				"OPTIMIZE_LOCAL_TABLE", "SET_ANY_DEFINER", "SHOW_ROUTINE")
			return spec
		}
		if versionAtLeast(track, "3.04") {
			spec.Privileges = append(spec.Privileges, "SHOW_ROUTINE")
		}
		if versionAtLeast(track, "3.09") {
			spec.Privileges = append(spec.Privileges,
				"FLUSH_OPTIMIZER_COSTS", "FLUSH_STATUS", "FLUSH_TABLES", "FLUSH_USER_RESOURCES")
		}
	}
	return spec
}

func removePrivilege(privileges []string, remove string) []string {
	for i, privilege := range privileges {
		if privilege == remove {
			return append(privileges[:i], privileges[i+1:]...)
		}
	}
	return privileges
}
