package ses

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// stub replies with 501 Not Implemented for any SES operation that is known
// but not yet implemented.  It is used in initOps as the handler value for
// every un-implemented action.
func (h *Handler) stub(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// GetSendStatisticsStub returns an empty list instead of 501 because some SDK
// clients call this on initialisation and a 501 would abort the workflow.
func (h *Handler) GetSendStatisticsStub(w http.ResponseWriter, r *http.Request) {
	writeQueryXML(w, r, "GetSendStatisticsResponse", "GetSendStatisticsResult", struct {
		SendDataPoints struct{} `xml:"SendDataPoints"`
	}{})
}
