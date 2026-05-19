package cognito

import (
	"context"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

const attributeVerificationCodeTTL = 15 * time.Minute

func applyUserAttributeUpdateSettings(pool *UserPool, settings *userAttributeUpdateSettingsWire) *protocol.AWSError {
	if settings == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, attr := range settings.AttributesRequireVerificationBeforeUpdate {
		if attr != "email" && attr != "phone_number" {
			return errAttributeInvalidParameter("Invalid AttributesRequireVerificationBeforeUpdate value.")
		}
		if seen[attr] {
			return errAttributeInvalidParameter("Duplicate AttributesRequireVerificationBeforeUpdate value.")
		}
		seen[attr] = true
	}
	pool.UserAttributeUpdateSettings = &UserAttributeUpdateSettings{AttributesRequireVerificationBeforeUpdate: settings.AttributesRequireVerificationBeforeUpdate}
	return nil
}

func errAttributeInvalidParameter(message string) *protocol.AWSError {
	return &protocol.AWSError{Code: "InvalidParameterException", Message: message, HTTPStatus: 400}
}

func requiresVerificationBeforeUpdate(pool *UserPool, attrName string) bool {
	if pool == nil || pool.UserAttributeUpdateSettings == nil {
		return false
	}
	for _, configured := range pool.UserAttributeUpdateSettings.AttributesRequireVerificationBeforeUpdate {
		if configured == attrName {
			return true
		}
	}
	return false
}

func verifiedAttributeName(attrName string) string {
	switch attrName {
	case "email":
		return "email_verified"
	case "phone_number":
		return "phone_number_verified"
	default:
		return ""
	}
}

func hasVerificationBypass(attrs []UserAttribute, attrName string) bool {
	verifiedName := verifiedAttributeName(attrName)
	if verifiedName == "" {
		return false
	}
	for _, attr := range attrs {
		if attr.Name == verifiedName && attr.Value == "true" {
			return true
		}
	}
	return false
}

func setPendingAttributeUpdate(u *User, attrName, value, code string, expiresAt time.Time) {
	for i, pending := range u.PendingAttributeUpdates {
		if pending.Name == attrName {
			u.PendingAttributeUpdates[i] = PendingAttributeUpdate{Name: attrName, Value: value, Code: code, ExpiresAt: expiresAt}
			return
		}
	}
	u.PendingAttributeUpdates = append(u.PendingAttributeUpdates, PendingAttributeUpdate{Name: attrName, Value: value, Code: code, ExpiresAt: expiresAt})
}

func removePendingAttributeUpdate(u *User, attrName string) {
	for i, pending := range u.PendingAttributeUpdates {
		if pending.Name == attrName {
			u.PendingAttributeUpdates = append(u.PendingAttributeUpdates[:i], u.PendingAttributeUpdates[i+1:]...)
			return
		}
	}
}

func pendingAttributeUpdate(u *User, attrName string) (PendingAttributeUpdate, bool) {
	for _, pending := range u.PendingAttributeUpdates {
		if pending.Name == attrName {
			return pending, true
		}
	}
	return PendingAttributeUpdate{}, false
}

func (s *Service) updateAttributesWithVerification(ctx context.Context, pool *UserPool, u *User, attrs []UserAttribute, admin bool) ([]codeDeliveryDetails, *protocol.AWSError) {
	details := make([]codeDeliveryDetails, 0, 2)
	for _, attr := range attrs {
		verifiedName := verifiedAttributeName(attr.Name)
		if verifiedName != "" && requiresVerificationBeforeUpdate(pool, attr.Name) && !hasVerificationBypass(attrs, attr.Name) {
			code := generateCode()
			setPendingAttributeUpdate(u, attr.Name, attr.Value, code, s.clk.Now().Add(attributeVerificationCodeTTL))
			details = append(details, s.sendAttributeVerification(pool, u.Username, attr.Name, attr.Value, code))
			continue
		}
		if verifiedName != "" && hasVerificationBypass(attrs, attr.Name) {
			removePendingAttributeUpdate(u, attr.Name)
		}

		if attr.Value == "true" && (attr.Name == "email_verified" || attr.Name == "phone_number_verified") {
			pendingName := "email"
			if attr.Name == "phone_number_verified" {
				pendingName = "phone_number"
			}
			if pending, ok := pendingAttributeUpdate(u, pendingName); ok {
				u.setAttr(pendingName, pending.Value)
				removePendingAttributeUpdate(u, pendingName)
			}
		}
		u.setAttr(attr.Name, attr.Value)
	}
	return details, nil
}

func (s *Service) sendAttributeVerification(pool *UserPool, username, attrName, value, code string) codeDeliveryDetails {
	if attrName == "phone_number" {
		s.sendVerificationSMS(pool, value, username, code)
		return codeDeliveryDetails{AttributeName: attrName, DeliveryMedium: "SMS", Destination: value}
	}
	s.sendVerificationEmail(pool, value, username, code)
	return codeDeliveryDetails{AttributeName: attrName, DeliveryMedium: "EMAIL", Destination: value}
}

func (s *Service) verifyPendingAttribute(ctx context.Context, pool *UserPool, u *User, attrName, code string) *protocol.AWSError {
	pending, ok := pendingAttributeUpdate(u, attrName)
	if !ok || pending.Code != code {
		return errCodeMismatch()
	}
	if !pending.ExpiresAt.IsZero() && s.clk.Now().After(pending.ExpiresAt) {
		removePendingAttributeUpdate(u, attrName)
		return errExpiredCode()
	}
	attrs := attributesAfterUpdate(u, []UserAttribute{
		{Name: attrName, Value: pending.Value},
		{Name: verifiedAttributeName(attrName), Value: "true"},
	})
	aliasOwner, _, err := s.findVerifiedAliasOwner(ctx, pool, attrs)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if aliasOwner != nil && aliasOwner.Username != u.Username {
		return errAliasExists()
	}
	u.setAttr(attrName, pending.Value)
	u.setAttr(verifiedAttributeName(attrName), "true")
	removePendingAttributeUpdate(u, attrName)
	return nil
}

func (s *Service) resendAttributeVerificationCode(pool *UserPool, u *User, attrName string) (codeDeliveryDetails, *protocol.AWSError) {
	if attrName != "email" && attrName != "phone_number" {
		return codeDeliveryDetails{}, errAttributeInvalidParameter("AttributeName must be email or phone_number.")
	}
	value := u.getAttr(attrName)
	if pending, ok := pendingAttributeUpdate(u, attrName); ok {
		value = pending.Value
	}
	if value == "" {
		return codeDeliveryDetails{}, errAttributeInvalidParameter("Attribute does not have a value to verify.")
	}
	code := generateCode()
	setPendingAttributeUpdate(u, attrName, value, code, s.clk.Now().Add(attributeVerificationCodeTTL))
	return s.sendAttributeVerification(pool, u.Username, attrName, value, code), nil
}
