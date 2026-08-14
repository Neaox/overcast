package rds

import (
	"fmt"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

func validateMasterUsername(engine, username string, cluster bool) *protocol.AWSError {
	allowUnderscore := true
	if cluster {
		allowUnderscore = false
	}
	if !validDatabaseIdentifier(username, 16, allowUnderscore) || reservedMasterUsername(engine, username) {
		return errInvalidParameterValue(fmt.Sprintf("The parameter MasterUsername is not a valid master user name: %q.", username))
	}
	return nil
}

func validateInitialDatabaseName(engine, database string) *protocol.AWSError {
	if database == "" {
		return nil
	}
	maxLen := 64
	if engine == "postgres" || engine == "aurora-postgresql" {
		maxLen = 63
	}
	if !validDatabaseIdentifier(database, maxLen, true) || reservedDatabaseName(engine, database) {
		return errInvalidParameterValue(fmt.Sprintf("The parameter DatabaseName is not a valid database name: %q.", database))
	}
	return nil
}

func validDatabaseIdentifier(value string, maxLen int, allowUnderscore bool) bool {
	if len(value) == 0 || len(value) > maxLen || !asciiLetter(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if asciiLetter(c) || (c >= '0' && c <= '9') || (allowUnderscore && c == '_') {
			continue
		}
		return false
	}
	return true
}

func asciiLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func reservedMasterUsername(engine, username string) bool {
	name := strings.ToLower(username)
	if name == "rdsadmin" || name == "rds_superuser_role" {
		return true
	}
	if engine == "postgres" || engine == "aurora-postgresql" {
		switch name {
		case "rds_superuser", "rds_password", "rds_replication":
			return true
		}
	}
	if engine == "aurora-mysql" {
		switch name {
		case "rdsproxyadmin", "rdsrepladmin", "rdsrepladmin_priv_checks_user",
			"aws_comprehend_access", "aws_lambda_access", "aws_load_s3_access",
			"aws_sagemaker_access", "aws_select_s3_access", "aws_bedrock_access":
			return true
		}
	}
	return false
}

func reservedDatabaseName(engine, database string) bool {
	name := strings.ToLower(database)
	switch name {
	case "select", "create", "drop", "table", "database", "user", "grant":
		return true
	}
	if engine == "mysql" || engine == "mariadb" || engine == "aurora-mysql" {
		switch name {
		case "mysql", "information_schema", "performance_schema", "sys":
			return true
		}
	}
	return false
}
