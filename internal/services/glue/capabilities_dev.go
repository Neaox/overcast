//go:build dev

package glue

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Databases
		capabilities.Capability{Service: "glue", Operation: "CreateDatabase", Category: "Databases", Status: capabilities.StatusSupported, Notes: "Creates a database in the catalog"},
		capabilities.Capability{Service: "glue", Operation: "GetDatabase", Category: "Databases", Status: capabilities.StatusSupported, Notes: "Returns database details"},
		capabilities.Capability{Service: "glue", Operation: "GetDatabases", Category: "Databases", Status: capabilities.StatusSupported, Notes: "Lists all databases"},
		capabilities.Capability{Service: "glue", Operation: "DeleteDatabase", Category: "Databases", Status: capabilities.StatusSupported, Notes: "Deletes a database"},
		// Tables
		capabilities.Capability{Service: "glue", Operation: "CreateTable", Category: "Tables", Status: capabilities.StatusSupported, Notes: "Creates a table in a database"},
		capabilities.Capability{Service: "glue", Operation: "GetTable", Category: "Tables", Status: capabilities.StatusSupported, Notes: "Returns table details"},
		capabilities.Capability{Service: "glue", Operation: "GetTables", Category: "Tables", Status: capabilities.StatusSupported, Notes: "Lists tables in a database"},
		capabilities.Capability{Service: "glue", Operation: "DeleteTable", Category: "Tables", Status: capabilities.StatusSupported, Notes: "Deletes a table"},
	)
}
