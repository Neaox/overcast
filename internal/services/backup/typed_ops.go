package backup

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateBackupVault": op.NewTyped[createBackupVaultRequest, createBackupVaultResponse](
			"CreateBackupVault", s.createBackupVaultTyped,
		),
		"DeleteBackupVault": op.NewTyped[deleteBackupVaultRequest, deleteBackupVaultResponse](
			"DeleteBackupVault", s.deleteBackupVaultTyped,
		),
		"DescribeBackupVault": op.NewTyped[describeBackupVaultRequest, backupVault](
			"DescribeBackupVault", s.describeBackupVaultTyped,
		),
		"ListBackupVaults": op.NewTyped[listBackupVaultsRequest, listBackupVaultsResponse](
			"ListBackupVaults", s.listBackupVaultsTyped,
		),
		"CreateBackupPlan": op.NewTyped[createBackupPlanRequest, createBackupPlanResponse](
			"CreateBackupPlan", s.createBackupPlanTyped,
		),
		"GetBackupPlan": op.NewTyped[getBackupPlanRequest, getBackupPlanResponse](
			"GetBackupPlan", s.getBackupPlanTyped,
		),
		"UpdateBackupPlan": op.NewTyped[updateBackupPlanRequest, updateBackupPlanResponse](
			"UpdateBackupPlan", s.updateBackupPlanTyped,
		),
		"DeleteBackupPlan": op.NewTyped[deleteBackupPlanRequest, deleteBackupPlanResponse](
			"DeleteBackupPlan", s.deleteBackupPlanTyped,
		),
		"ListBackupPlans": op.NewTyped[listBackupPlansRequest, listBackupPlansResponse](
			"ListBackupPlans", s.listBackupPlansTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
