package elasticache

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── XML types for parameter groups ──────────────────────────────────────────

type xmlCreateCacheParameterGroupResponse struct {
	XMLName          xml.Name                           `xml:"CreateCacheParameterGroupResponse"`
	Xmlns            string                             `xml:"xmlns,attr"`
	Result           xmlCreateCacheParameterGroupResult `xml:"CreateCacheParameterGroupResult"`
	ResponseMetadata protocol.ResponseMetadata          `xml:"ResponseMetadata"`
}

type xmlCreateCacheParameterGroupResult struct {
	CacheParameterGroup xmlCacheParameterGroup `xml:"CacheParameterGroup"`
}

type xmlDeleteCacheParameterGroupResponse struct {
	XMLName          xml.Name                  `xml:"DeleteCacheParameterGroupResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeCacheParameterGroupsResponse struct {
	XMLName          xml.Name                              `xml:"DescribeCacheParameterGroupsResponse"`
	Xmlns            string                                `xml:"xmlns,attr"`
	Result           xmlDescribeCacheParameterGroupsResult `xml:"DescribeCacheParameterGroupsResult"`
	ResponseMetadata protocol.ResponseMetadata             `xml:"ResponseMetadata"`
}

type xmlDescribeCacheParameterGroupsResult struct {
	CacheParameterGroups xmlCacheParameterGroups `xml:"CacheParameterGroups"`
}

type xmlCacheParameterGroups struct {
	Items []xmlCacheParameterGroup `xml:"CacheParameterGroup"`
}

type xmlCacheParameterGroup struct {
	CacheParameterGroupName   string `xml:"CacheParameterGroupName"`
	CacheParameterGroupFamily string `xml:"CacheParameterGroupFamily"`
	Description               string `xml:"Description"`
	ARN                       string `xml:"ARN"`
}

// ── CreateCacheParameterGroup ─────────────────────────────────────────────────

func (h *Handler) CreateCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheParameterGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheParameterGroupName is required"))
		return
	}

	if _, aerr := h.store.getCacheParameterGroup(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errParameterGroupAlreadyExists(name))
		return
	}

	family := r.FormValue("CacheParameterGroupFamily")
	description := r.FormValue("Description")

	region := h.store.region(r.Context())
	arn := fmt.Sprintf("arn:aws:elasticache:%s:%s:parametergroup:%s", region, h.cfg.AccountID, name)

	pg := &CacheParameterGroup{
		CacheParameterGroupName:   name,
		CacheParameterGroupFamily: family,
		Description:               description,
		ARN:                       arn,
	}

	if aerr := h.store.putCacheParameterGroup(r.Context(), pg); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateCacheParameterGroupResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlCreateCacheParameterGroupResult{CacheParameterGroup: toXMLCacheParameterGroup(pg)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeCacheParameterGroups ──────────────────────────────────────────────

func (h *Handler) DescribeCacheParameterGroups(w http.ResponseWriter, r *http.Request) {
	filterName := r.FormValue("CacheParameterGroupName")

	if filterName != "" {
		pg, aerr := h.store.getCacheParameterGroup(r.Context(), filterName)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheParameterGroupsResponse{
			Xmlns: cacheXMLNS,
			Result: xmlDescribeCacheParameterGroupsResult{
				CacheParameterGroups: xmlCacheParameterGroups{Items: []xmlCacheParameterGroup{toXMLCacheParameterGroup(pg)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listCacheParameterGroups(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := make([]xmlCacheParameterGroup, 0, len(all))
	for _, pg := range all {
		items = append(items, toXMLCacheParameterGroup(pg))
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheParameterGroupsResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDescribeCacheParameterGroupsResult{CacheParameterGroups: xmlCacheParameterGroups{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteCacheParameterGroup ─────────────────────────────────────────────────

func (h *Handler) DeleteCacheParameterGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheParameterGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheParameterGroupName is required"))
		return
	}

	if _, aerr := h.store.getCacheParameterGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteCacheParameterGroup(r.Context(), name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteCacheParameterGroupResponse{
		Xmlns:            cacheXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeCacheParameters ───────────────────────────────────────────────────

type xmlDescribeCacheParametersResponse struct {
	XMLName          xml.Name                         `xml:"DescribeCacheParametersResponse"`
	Xmlns            string                           `xml:"xmlns,attr"`
	Result           xmlDescribeCacheParametersResult `xml:"DescribeCacheParametersResult"`
	ResponseMetadata protocol.ResponseMetadata        `xml:"ResponseMetadata"`
}

type xmlDescribeCacheParametersResult struct {
	Parameters xmlCacheParameterList `xml:"Parameters"`
	Marker     string                `xml:"Marker"`
}

type xmlCacheParameterList struct {
	Items []xmlCacheParameter `xml:"Parameter"`
}

type xmlCacheParameter struct {
	ParameterName        string `xml:"ParameterName"`
	ParameterValue       string `xml:"ParameterValue"`
	Description          string `xml:"Description"`
	Source               string `xml:"Source"`
	DataType             string `xml:"DataType"`
	AllowedValues        string `xml:"AllowedValues,omitempty"`
	IsModifiable         bool   `xml:"IsModifiable"`
	MinimumEngineVersion string `xml:"MinimumEngineVersion"`
	ChangeType           string `xml:"ChangeType"`
}

type paramDef struct {
	name        string
	value       string
	description string
	dataType    string
	allowed     string
	modifiable  bool
	minVersion  string
	changeType  string
}

// redisParams is the curated list of well-known Redis/Valkey parameters returned
// for any redis* or valkey* parameter group family.
var redisParams = []paramDef{
	{"activerehashing", "yes", "Apply rehashing or not", "string", "yes,no", true, "2.8.6", "requires-reboot"},
	{"bind-source-addr", "", "Bind source address for outgoing connections", "string", "", false, "7.0.4", "requires-reboot"},
	{"close-on-oom-score-adj-zero", "no", "Close client on OOM score adj zero", "string", "yes,no", true, "6.2.6", "immediate"},
	{"databases", "16", "Number of logical databases", "integer", "1-2147483647", false, "2.8.6", "requires-reboot"},
	{"hz", "10", "Frequency of background tasks", "integer", "1-500", true, "2.8.6", "immediate"},
	{"lazyfree-lazy-eviction", "no", "Enable lazy eviction", "string", "yes,no", true, "4.0.10", "immediate"},
	{"lazyfree-lazy-expire", "no", "Enable lazy key expiry", "string", "yes,no", true, "4.0.10", "immediate"},
	{"lazyfree-lazy-server-del", "no", "Enable lazy server deletion", "string", "yes,no", true, "4.0.10", "immediate"},
	{"loglevel", "notice", "Server verbosity level", "string", "debug,verbose,notice,warning", false, "2.8.6", "immediate"},
	{"maxmemory-policy", "noeviction", "Key eviction policy when maxmemory is reached", "string", "noeviction,allkeys-lru,volatile-lru,allkeys-random,volatile-random,volatile-ttl,allkeys-lfu,volatile-lfu", true, "2.8.6", "immediate"},
	{"maxmemory-samples", "5", "Sample size for LRU/LFU eviction", "integer", "1-64", true, "2.8.6", "immediate"},
	{"notify-keyspace-events", "", "Enable keyspace notifications (empty = disabled)", "string", "", true, "2.8.6", "immediate"},
	{"repl-backlog-size", "1048576", "Replication backlog buffer size in bytes", "integer", "16384-2147483647", true, "2.8.6", "immediate"},
	{"slowlog-log-slower-than", "10000", "Slowlog threshold in microseconds (-1 disabled)", "integer", "-1-9223372036854775807", true, "2.8.6", "immediate"},
	{"slowlog-max-len", "128", "Maximum slowlog entries", "integer", "0-4294967295", true, "2.8.6", "immediate"},
	{"tcp-keepalive", "300", "TCP keepalive in seconds (0 = disabled)", "integer", "0-", true, "2.8.6", "immediate"},
	{"timeout", "0", "Close idle client connections after N seconds (0 = disabled)", "integer", "0-2147483647", true, "2.8.6", "immediate"},
}

// memcachedParams is the curated list for any memcached* parameter group family.
var memcachedParams = []paramDef{
	{"backlog_queue_limit", "1024", "TCP listen backlog", "integer", "1-10000", true, "1.4.14", "immediate"},
	{"binding_protocol", "auto", "Binding protocol (auto, ascii, or binary)", "string", "auto,ascii,binary", false, "1.4.14", "requires-reboot"},
	{"chunk_size", "48", "Minimum allocation chunk size in bytes", "integer", "1-1048576", true, "1.4.14", "requires-reboot"},
	{"chunk_size_growth_factor", "1.25", "Chunk size growth factor", "string", "1-2", true, "1.4.14", "requires-reboot"},
	{"connection_overhead", "100", "Per-connection memory overhead in bytes", "integer", "1-1000000", true, "1.4.14", "immediate"},
	{"max_item_size", "1048576", "Maximum item size in bytes", "integer", "1-1073741824", true, "1.4.14", "immediate"},
	{"max_simultaneous_connections", "65000", "Maximum simultaneous connections", "integer", "1-65000", false, "1.4.14", "requires-reboot"},
}

// engineParamsForFamily returns the static parameter list for a given engine family.
func engineParamsForFamily(family string) []paramDef {
	f := strings.ToLower(family)
	if strings.HasPrefix(f, "redis") || strings.HasPrefix(f, "valkey") {
		return redisParams
	}
	if strings.HasPrefix(f, "memcached") {
		return memcachedParams
	}
	// Unknown family — return redis defaults as a safe fallback.
	return redisParams
}

func (h *Handler) DescribeCacheParameters(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("CacheParameterGroupName")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheParameterGroupName is required"))
		return
	}

	pg, aerr := h.store.getCacheParameterGroup(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	source := strings.ToLower(r.FormValue("Source")) // "user" | "system" | "engine-default" | ""

	// We only return "system" / engine-default parameters (no user-modified values).
	// A Source filter of "user" always yields an empty list.
	var params []xmlCacheParameter
	if source == "" || source == "system" || source == "engine-default" {
		for _, p := range engineParamsForFamily(pg.CacheParameterGroupFamily) {
			params = append(params, xmlCacheParameter{
				ParameterName:        p.name,
				ParameterValue:       p.value,
				Description:          p.description,
				Source:               "system",
				DataType:             p.dataType,
				AllowedValues:        p.allowed,
				IsModifiable:         p.modifiable,
				MinimumEngineVersion: p.minVersion,
				ChangeType:           p.changeType,
			})
		}
	}

	// Pagination: MaxRecords (default 100) + Marker (decimal start index).
	maxRecords := 100
	if v := r.FormValue("MaxRecords"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxRecords = n
		}
	}
	startIdx := 0
	if m := r.FormValue("Marker"); m != "" {
		if n, err := strconv.Atoi(m); err == nil && n >= 0 {
			startIdx = n
		}
	}
	if startIdx > len(params) {
		startIdx = len(params)
	}
	page := params[startIdx:]
	nextMarker := ""
	if len(page) > maxRecords {
		page = page[:maxRecords]
		nextMarker = strconv.Itoa(startIdx + maxRecords)
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheParametersResponse{
		Xmlns: cacheXMLNS,
		Result: xmlDescribeCacheParametersResult{
			Parameters: xmlCacheParameterList{Items: page},
			Marker:     nextMarker,
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── Helper ────────────────────────────────────────────────────────────────────

func toXMLCacheParameterGroup(pg *CacheParameterGroup) xmlCacheParameterGroup {
	return xmlCacheParameterGroup{
		CacheParameterGroupName:   pg.CacheParameterGroupName,
		CacheParameterGroupFamily: pg.CacheParameterGroupFamily,
		Description:               pg.Description,
		ARN:                       pg.ARN,
	}
}
