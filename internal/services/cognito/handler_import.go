package cognito

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) handleImportUsers(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")

	_, ok := s.requirePool(r.Context(), w, r, poolID)
	if !ok {
		return
	}

	var req importUsersRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	resp := importUsersResponse{}

	for i, entry := range req.Users {
		if entry.Username == "" {
			resp.Errors = append(resp.Errors, importUserError{Index: i, Reason: "missing Username"})
			resp.Skipped++
			continue
		}
		if entry.Sub == "" {
			resp.Errors = append(resp.Errors, importUserError{Index: i, Username: entry.Username, Reason: "missing Sub"})
			resp.Skipped++
			continue
		}

		targetStatus, skip := mapImportStatus(entry.Status)
		if skip {
			resp.Errors = append(resp.Errors, importUserError{Index: i, Username: entry.Username, Reason: "cannot import EXTERNAL_PROVIDER users"})
			resp.Skipped++
			continue
		}

		existing, err := s.loadUser(r.Context(), poolID, entry.Username)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		if existing != nil {
			resp.Errors = append(resp.Errors, importUserError{Index: i, Username: entry.Username, Reason: "user already exists"})
			resp.Skipped++
			continue
		}

		attrs := entry.Attributes
		if attrs == nil {
			attrs = []UserAttribute{}
		}

		createdAt := entry.CreatedAt
		if createdAt.IsZero() {
			createdAt = s.clk.Now()
		}
		modifiedAt := entry.ModifiedAt
		if modifiedAt.IsZero() {
			modifiedAt = createdAt
		}

		u := &User{
			Username:   entry.Username,
			Sub:        entry.Sub,
			UserPoolID: poolID,
			CreatedAt:  createdAt,
			ModifiedAt: modifiedAt,
			Status:     targetStatus,
			Enabled:    entry.Enabled,
			Attributes: attrs,
			Groups:     entry.Groups,
			MFAEnabled: entry.MFAEnabled,
		}
		u.setAttr("sub", entry.Sub)

		if err := s.saveUser(r.Context(), u); err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}

		for _, groupName := range entry.Groups {
			if err := s.ensureGroup(r.Context(), poolID, groupName); err != nil {
				continue
			}
		}

		s.log.Info("imported user",
			zap.String("poolId", poolID), zap.String("username", entry.Username))
		s.publish(r, events.CognitoUserCreated, events.ResourcePayload{Name: entry.Username})
		resp.Imported++
	}

	s.writeJSON(w, r, http.StatusOK, resp)
}

// ensureGroup creates a stub group if it does not already exist in the pool.
func (s *Service) ensureGroup(ctx context.Context, poolID, groupName string) error {
	g, err := s.loadGroup(ctx, poolID, groupName)
	if err != nil {
		return err
	}
	if g != nil {
		return nil
	}
	g = &Group{
		GroupName:  groupName,
		UserPoolID: poolID,
		Precedence: 0,
		CreatedAt:  s.clk.Now(),
	}
	return s.saveGroup(ctx, g)
}

// mapImportStatus converts an AWS Cognito user status to an Overcast UserStatus.
// It returns (status, skip). skip=true means the user should not be imported
// (e.g. EXTERNAL_PROVIDER users with no local credentials).
func mapImportStatus(awsStatus string) (UserStatus, bool) {
	switch awsStatus {
	case "CONFIRMED":
		return StatusForceChangePassword, false
	case "FORCE_CHANGE_PASSWORD":
		return StatusForceChangePassword, false
	case "UNCONFIRMED":
		return StatusUnconfirmed, false
	case "DISABLED":
		return StatusDisabled, false
	case "RESET_REQUIRED":
		return StatusForceChangePassword, false
	case "ARCHIVED", "COMPROMISED":
		return StatusDisabled, false
	case "EXTERNAL_PROVIDER":
		return "", true
	default:
		return StatusForceChangePassword, false
	}
}
