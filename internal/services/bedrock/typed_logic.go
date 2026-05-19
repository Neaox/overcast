package bedrock

import (
	"context"

	"github.com/Neaox/overcast/internal/protocol"
)

// --- InvokeModel ---

type invokeModelRequest struct{}

type invokeModelResponse struct {
	Output     outputType `json:"output" cbor:"output"`
	StopReason string     `json:"stopReason" cbor:"stopReason"`
	Usage      usageType  `json:"usage" cbor:"usage"`
}

type outputType struct {
	Text string `json:"text" cbor:"text"`
}

type usageType struct {
	InputTokens  int `json:"inputTokens" cbor:"inputTokens"`
	OutputTokens int `json:"outputTokens" cbor:"outputTokens"`
}

func (s *Service) invokeModelTyped(ctx context.Context, _ *invokeModelRequest) (*invokeModelResponse, *protocol.AWSError) {
	return &invokeModelResponse{
		Output: outputType{
			Text: "This is a canned response from the Overcast Bedrock emulator.",
		},
		StopReason: "end_turn",
		Usage: usageType{
			InputTokens:  10,
			OutputTokens: 12,
		},
	}, nil
}

// --- Converse ---

type converseRequest struct{}

type converseResponse struct {
	Output     converseOutput  `json:"output" cbor:"output"`
	StopReason string          `json:"stopReason" cbor:"stopReason"`
	Usage      usageType       `json:"usage" cbor:"usage"`
	Metrics    converseMetrics `json:"metrics" cbor:"metrics"`
}

type converseOutput struct {
	Message converseMessage `json:"message" cbor:"message"`
}

type converseMessage struct {
	Role    string            `json:"role" cbor:"role"`
	Content []converseContent `json:"content" cbor:"content"`
}

type converseContent struct {
	Text string `json:"text" cbor:"text"`
}

type converseMetrics struct {
	LatencyMs int `json:"latencyMs" cbor:"latencyMs"`
}

func (s *Service) converseTyped(ctx context.Context, _ *converseRequest) (*converseResponse, *protocol.AWSError) {
	return &converseResponse{
		Output: converseOutput{
			Message: converseMessage{
				Role: "assistant",
				Content: []converseContent{
					{Text: "This is a canned response from the Overcast Bedrock emulator."},
				},
			},
		},
		StopReason: "end_turn",
		Usage: usageType{
			InputTokens:  10,
			OutputTokens: 12,
		},
		Metrics: converseMetrics{
			LatencyMs: 1,
		},
	}, nil
}
