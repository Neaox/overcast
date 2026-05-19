package rds

import (
	"encoding/xml"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Known parameter group families ───────────────────────────────────────────

var knownParameterGroupFamilies = map[string]bool{
	"mysql8.0":     true,
	"mysql5.7":     true,
	"postgres16":   true,
	"postgres15":   true,
	"postgres14":   true,
	"mariadb11.4":  true,
	"mariadb10.11": true,
}

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateDBParameterGroupResponse struct {
	XMLName          xml.Name                        `xml:"CreateDBParameterGroupResponse"`
	Xmlns            string                          `xml:"xmlns,attr"`
	Result           xmlCreateDBParameterGroupResult `xml:"CreateDBParameterGroupResult"`
	ResponseMetadata protocol.ResponseMetadata       `xml:"ResponseMetadata"`
}

type xmlCreateDBParameterGroupResult struct {
	DBParameterGroup xmlDBParameterGroup `xml:"DBParameterGroup"`
}

type xmlDeleteDBParameterGroupResponse struct {
	XMLName          xml.Name                  `xml:"DeleteDBParameterGroupResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeDBParameterGroupsResponse struct {
	XMLName          xml.Name                           `xml:"DescribeDBParameterGroupsResponse"`
	Xmlns            string                             `xml:"xmlns,attr"`
	Result           xmlDescribeDBParameterGroupsResult `xml:"DescribeDBParameterGroupsResult"`
	ResponseMetadata protocol.ResponseMetadata          `xml:"ResponseMetadata"`
}

type xmlDescribeDBParameterGroupsResult struct {
	DBParameterGroups xmlDBParameterGroups `xml:"DBParameterGroups"`
}

type xmlDBParameterGroups struct {
	Items []xmlDBParameterGroup `xml:"DBParameterGroup"`
}

type xmlDBParameterGroup struct {
	DBParameterGroupName   string `xml:"DBParameterGroupName"`
	DBParameterGroupFamily string `xml:"DBParameterGroupFamily"`
	Description            string `xml:"Description"`
	DBParameterGroupArn    string `xml:"DBParameterGroupArn"`
}

// ── Orderable DB instance options ────────────────────────────────────────────

type xmlDescribeOrderableDBInstanceOptionsResponse struct {
	XMLName          xml.Name                                    `xml:"DescribeOrderableDBInstanceOptionsResponse"`
	Xmlns            string                                      `xml:"xmlns,attr"`
	Result           xmlDescribeOrderableDBInstanceOptionsResult `xml:"DescribeOrderableDBInstanceOptionsResult"`
	ResponseMetadata protocol.ResponseMetadata                   `xml:"ResponseMetadata"`
}

type xmlDescribeOrderableDBInstanceOptionsResult struct {
	OrderableDBInstanceOptions xmlOrderableDBInstanceOptions `xml:"OrderableDBInstanceOptions"`
}

type xmlOrderableDBInstanceOptions struct {
	Items []xmlOrderableDBInstanceOption `xml:"OrderableDBInstanceOption"`
}

type xmlOrderableDBInstanceOption struct {
	Engine          string `xml:"Engine"`
	EngineVersion   string `xml:"EngineVersion"`
	DBInstanceClass string `xml:"DBInstanceClass"`
	LicenseModel    string `xml:"LicenseModel"`
	StorageType     string `xml:"StorageType"`
	MultiAZCapable  bool   `xml:"MultiAZCapable"`
}

// ── Static orderable options ─────────────────────────────────────────────────

type orderableOption struct {
	Engine          string
	EngineVersion   string
	DBInstanceClass string
	LicenseModel    string
	StorageType     string
	MultiAZCapable  bool
}

var instanceClasses = []string{"db.t3.micro", "db.t3.small", "db.t3.medium", "db.m5.large"}

var allOrderableOptions []orderableOption

func init() {
	type engineDef struct {
		engine       string
		version      string
		licenseModel string
	}
	engines := []engineDef{
		{"postgres", "16.1", "postgresql-license"},
		{"postgres", "15.5", "postgresql-license"},
		{"postgres", "14.11", "postgresql-license"},
		{"mysql", "8.0", "general-public-license"},
		{"mysql", "5.7", "general-public-license"},
		{"mariadb", "11.4", "general-public-license"},
		{"mariadb", "10.11", "general-public-license"},
	}
	for _, e := range engines {
		for _, cls := range instanceClasses {
			allOrderableOptions = append(allOrderableOptions, orderableOption{
				Engine:          e.engine,
				EngineVersion:   e.version,
				DBInstanceClass: cls,
				LicenseModel:    e.licenseModel,
				StorageType:     "gp2",
				MultiAZCapable:  true,
			})
		}
	}
}

// ── CreateDBParameterGroup ───────────────────────────────────────────────────

// CreateDBParameterGroup creates a new DB parameter group.
func (h *Handler) CreateDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBParameterGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBParameterGroupName is required"))
		return
	}

	family := r.FormValue("DBParameterGroupFamily")
	if family == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBParameterGroupFamily is required"))
		return
	}
	if !knownParameterGroupFamilies[family] {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Invalid DB parameter group family: "+family))
		return
	}

	description := r.FormValue("Description")

	// Check for duplicate.
	if _, aerr := h.store.getDBParameterGroup(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errDBParameterGroupAlreadyExists(name))
		return
	}

	region := h.store.region(r.Context())
	arn := protocol.ARN(region, h.cfg.AccountID, "rds", "pg:"+name)

	pg := &DBParameterGroup{
		DBParameterGroupName:   name,
		DBParameterGroupFamily: family,
		Description:            description,
		DBParameterGroupArn:    arn,
	}

	if aerr := h.store.putDBParameterGroup(r.Context(), pg); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateDBParameterGroupResponse{
		Xmlns:            rdsXMLNS,
		Result:           xmlCreateDBParameterGroupResult{DBParameterGroup: toXMLDBParameterGroup(pg)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteDBParameterGroup ───────────────────────────────────────────────────

// DeleteDBParameterGroup deletes a DB parameter group.
func (h *Handler) DeleteDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("DBParameterGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("DBParameterGroupName is required"))
		return
	}

	if _, aerr := h.store.getDBParameterGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteDBParameterGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteDBParameterGroupResponse{
		Xmlns:            rdsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeDBParameterGroups ────────────────────────────────────────────────

// DescribeDBParameterGroups returns DB parameter groups, optionally filtered by name.
func (h *Handler) DescribeDBParameterGroups(w http.ResponseWriter, r *http.Request) {
	filterName := r.FormValue("DBParameterGroupName")

	if filterName != "" {
		pg, aerr := h.store.getDBParameterGroup(r.Context(), filterName)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBParameterGroupsResponse{
			Xmlns: rdsXMLNS,
			Result: xmlDescribeDBParameterGroupsResult{
				DBParameterGroups: xmlDBParameterGroups{Items: []xmlDBParameterGroup{toXMLDBParameterGroup(pg)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listDBParameterGroups(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	items := make([]xmlDBParameterGroup, 0, len(all))
	for _, pg := range all {
		items = append(items, toXMLDBParameterGroup(pg))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeDBParameterGroupsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeDBParameterGroupsResult{
			DBParameterGroups: xmlDBParameterGroups{Items: items},
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeOrderableDBInstanceOptions ───────────────────────────────────────

// DescribeOrderableDBInstanceOptions returns static orderable instance options.
func (h *Handler) DescribeOrderableDBInstanceOptions(w http.ResponseWriter, r *http.Request) {
	engine := r.FormValue("Engine")
	if engine == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine is required"))
		return
	}

	items := make([]xmlOrderableDBInstanceOption, 0)
	for _, opt := range allOrderableOptions {
		if opt.Engine != engine {
			continue
		}
		items = append(items, xmlOrderableDBInstanceOption(opt))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeOrderableDBInstanceOptionsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeOrderableDBInstanceOptionsResult{
			OrderableDBInstanceOptions: xmlOrderableDBInstanceOptions{Items: items},
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func toXMLDBParameterGroup(pg *DBParameterGroup) xmlDBParameterGroup {
	return xmlDBParameterGroup{
		DBParameterGroupName:   pg.DBParameterGroupName,
		DBParameterGroupFamily: pg.DBParameterGroupFamily,
		Description:            pg.Description,
		DBParameterGroupArn:    pg.DBParameterGroupArn,
	}
}
