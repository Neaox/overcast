package cognito

import (
	"net/http"
	"slices"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// createGroup — CreateGroup.
func (s *Service) createGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID  string `json:"UserPoolId"`
		GroupName   string `json:"GroupName"`
		Description string `json:"Description"`
		Precedence  int    `json:"Precedence"`
		RoleARN     string `json:"RoleArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}

	// Reject duplicate group names.
	existing, err := s.loadGroup(r.Context(), req.UserPoolID, req.GroupName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "GroupExistsException",
			Message:    "A group with the name already exists.",
			HTTPStatus: 400,
		})
		return
	}

	g := &Group{
		GroupName:   req.GroupName,
		UserPoolID:  req.UserPoolID,
		Description: req.Description,
		Precedence:  req.Precedence,
		RoleARN:     req.RoleARN,
		CreatedAt:   s.clk.Now(),
	}
	if err := s.saveGroup(r.Context(), g); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("group created",
		zap.String("poolId", req.UserPoolID), zap.String("group", req.GroupName))
	s.publish(r, events.CognitoGroupCreated, events.ResourcePayload{Name: req.GroupName})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"Group": toGroupWire(g)})
}

// getGroup — GetGroup.
func (s *Service) getGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		GroupName  string `json:"GroupName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	g, ok := s.requireGroup(r.Context(), w, r, req.UserPoolID, req.GroupName)
	if !ok {
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"Group": toGroupWire(g)})
}

// deleteGroup — DeleteGroup.
func (s *Service) deleteGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		GroupName  string `json:"GroupName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	if _, ok := s.requireGroup(r.Context(), w, r, req.UserPoolID, req.GroupName); !ok {
		return
	}
	if err := s.removeGroup(r.Context(), req.UserPoolID, req.GroupName); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("group deleted",
		zap.String("poolId", req.UserPoolID), zap.String("group", req.GroupName))
	s.publish(r, events.CognitoGroupDeleted, events.ResourcePayload{Name: req.GroupName})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// updateGroup — UpdateGroup.
func (s *Service) updateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID  string `json:"UserPoolId"`
		GroupName   string `json:"GroupName"`
		Description string `json:"Description"`
		Precedence  int    `json:"Precedence"`
		RoleARN     string `json:"RoleArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	g, ok := s.requireGroup(r.Context(), w, r, req.UserPoolID, req.GroupName)
	if !ok {
		return
	}
	g.Description = req.Description
	g.Precedence = req.Precedence
	if req.RoleARN != "" {
		g.RoleARN = req.RoleARN
	}
	if err := s.saveGroup(r.Context(), g); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoGroupUpdated, events.ResourcePayload{Name: req.GroupName})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// listGroups — ListGroups.
func (s *Service) listGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		Limit      int    `json:"Limit"`
		NextToken  string `json:"NextToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}
	groups, err := s.scanGroups(r.Context(), req.UserPoolID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	wires := make([]groupWire, 0, len(groups))
	for _, g := range groups {
		wires = append(wires, toGroupWire(g))
	}
	page, nextToken, aerr := pageGroupWires(wires, req.Limit, req.NextToken)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	resp := map[string]any{"Groups": page}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

// adminAddUserToGroup — AdminAddUserToGroup.
func (s *Service) adminAddUserToGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		Username   string `json:"Username"`
		GroupName  string `json:"GroupName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	if _, ok := s.requireGroup(r.Context(), w, r, req.UserPoolID, req.GroupName); !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, req.UserPoolID, req.Username)
	if !ok {
		return
	}

	if !slices.Contains(u.Groups, req.GroupName) {
		u.Groups = append(u.Groups, req.GroupName)
		if err := s.saveUser(r.Context(), u); err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
	}
	s.publish(r, events.CognitoGroupMembershipChanged, events.ResourcePayload{Name: req.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// adminRemoveUserFromGroup — AdminRemoveUserFromGroup.
func (s *Service) adminRemoveUserFromGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		Username   string `json:"Username"`
		GroupName  string `json:"GroupName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	if _, ok := s.requireGroup(r.Context(), w, r, req.UserPoolID, req.GroupName); !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, req.UserPoolID, req.Username)
	if !ok {
		return
	}
	u.Groups = slices.DeleteFunc(u.Groups, func(g string) bool { return g == req.GroupName })
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoGroupMembershipChanged, events.ResourcePayload{Name: req.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// adminListGroupsForUser — AdminListGroupsForUser.
func (s *Service) adminListGroupsForUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		Username   string `json:"Username"`
		Limit      int    `json:"Limit"`
		NextToken  string `json:"NextToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, req.UserPoolID, req.Username)
	if !ok {
		return
	}

	wires := make([]groupWire, 0, len(u.Groups))
	for _, name := range u.Groups {
		g, err := s.loadGroup(r.Context(), req.UserPoolID, name)
		if err != nil || g == nil {
			continue // group was deleted; skip stale membership
		}
		wires = append(wires, toGroupWire(g))
	}
	page, nextToken, aerr := pageGroupWires(wires, req.Limit, req.NextToken)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	resp := map[string]any{"Groups": page}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

// listUsersInGroup — ListUsersInGroup.
func (s *Service) listUsersInGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID string `json:"UserPoolId"`
		GroupName  string `json:"GroupName"`
		Limit      int    `json:"Limit"`
		NextToken  string `json:"NextToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.GroupName, "GroupName") {
		return
	}
	if _, ok := s.requireGroup(r.Context(), w, r, req.UserPoolID, req.GroupName); !ok {
		return
	}

	all, err := s.scanUsers(r.Context(), req.UserPoolID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	wires := make([]userWire, 0)
	for _, u := range all {
		if slices.Contains(u.Groups, req.GroupName) {
			wires = append(wires, toUserWire(u))
		}
	}
	page, nextToken, aerr := pageUserWires(wires, req.Limit, req.NextToken)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	resp := map[string]any{"Users": page}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}
