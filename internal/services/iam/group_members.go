package iam

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// GetGroup's page size, as AWS documents it: MaxItems defaults to 100 and is
// capped at 1000, and a caller asking for more than the cap gets the cap
// rather than an error.
// https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetGroup.html
const (
	getGroupDefaultMaxItems = 100
	getGroupMaxItems        = 1000
)

// groupPage is one page of a group's membership, ready to serialise.
type groupPage struct {
	group       *Group
	users       []userXML
	marker      string
	isTruncated bool
}

// resolveGroupPage is the whole of GetGroup's logic, shared by the typed and
// the legacy Query dispatch paths so the two cannot drift.
//
// Membership entries that no longer resolve to a readable user record are
// skipped rather than failing the call, and the gap is logged.
func (h *Handler) resolveGroupPage(ctx context.Context, groupName, marker string, maxItems int) (*groupPage, *protocol.AWSError) {
	if groupName == "" {
		return nil, protocol.ErrMissingParameter("GroupName")
	}
	g, aerr := h.store.getGroup(ctx, groupName)
	if aerr != nil {
		return nil, aerr
	}
	members, skipped, aerr := h.store.resolveGroupMembers(ctx, g)
	if aerr != nil {
		return nil, aerr
	}
	if len(skipped) > 0 && h.log != nil {
		h.log.Warn("iam: GetGroup skipped group members with no readable user record",
			zap.String("group", groupName),
			zap.Strings("members", skipped))
	}

	page, err := serviceutil.Paginate(members, maxItems, marker, serviceutil.PaginateOptions{
		DefaultLimit: getGroupDefaultMaxItems,
		MaxLimit:     getGroupMaxItems,
	})
	if err != nil {
		if errors.Is(err, serviceutil.ErrInvalidPageToken) {
			// IAM's modeled error for a parameter value it cannot use. Silently
			// restarting at page 1 would hand a client polling with a stale
			// marker the whole membership again, which reads as a legitimate
			// page.
			return nil, errInvalidInput("The request was rejected because an invalid or out-of-range value was supplied for the Marker parameter.")
		}
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	users := make([]userXML, 0, len(page.Items))
	for i := range page.Items {
		users = append(users, toUserXML(&page.Items[i]))
	}
	return &groupPage{
		group:       g,
		users:       users,
		marker:      page.NextToken,
		isTruncated: page.IsTruncated,
	}, nil
}
