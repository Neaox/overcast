package transfer

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateServer": op.NewTyped[createServerRequest, createServerResponse](
			"CreateServer", s.createServerTyped,
		),
		"DescribeServer": op.NewTyped[describeServerRequest, describeServerResponse](
			"DescribeServer", s.describeServerTyped,
		),
		"ListServers": op.NewTyped[listServersRequest, listServersResponse](
			"ListServers", s.listServersTyped,
		),
		"UpdateServer": op.NewTyped[updateServerRequest, struct{}](
			"UpdateServer", s.updateServerTyped,
		),
		"DeleteServer": op.NewTyped[deleteServerRequest, struct{}](
			"DeleteServer", s.deleteServerTyped,
		),
		"CreateUser": op.NewTyped[createUserRequest, createUserResponse](
			"CreateUser", s.createUserTyped,
		),
		"DescribeUser": op.NewTyped[describeUserRequest, describeUserResponse](
			"DescribeUser", s.describeUserTyped,
		),
		"ListUsers": op.NewTyped[listUsersRequest, listUsersResponse](
			"ListUsers", s.listUsersTyped,
		),
		"UpdateUser": op.NewTyped[updateUserRequest, updateUserResponse](
			"UpdateUser", s.updateUserTyped,
		),
		"DeleteUser": op.NewTyped[deleteUserRequest, struct{}](
			"DeleteUser", s.deleteUserTyped,
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
