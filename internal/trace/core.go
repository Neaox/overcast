package trace

import (
	"math"

	"go.uber.org/zap/zapcore"
)

// ZapFieldsToMap converts a slice of zapcore.Field into a plain map, making
// structured log entries usable outside the zap ecosystem. Duplicate code
// in middleware/logger.go is removed in favour of this shared function.
func ZapFieldsToMap(fields []zapcore.Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		m[f.Key] = ZapFieldValue(f)
	}
	return m
}

// ZapFieldValue converts a single zapcore.Field into its Go value,
// handling all zap field types including nested arrays and objects.
func ZapFieldValue(f zapcore.Field) any {
	switch f.Type {
	case zapcore.StringType:
		return f.String
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return f.Integer
	case zapcore.Uint64Type, zapcore.Uint32Type, zapcore.Uint16Type, zapcore.Uint8Type, zapcore.UintptrType:
		return f.Integer
	case zapcore.DurationType:
		return f.Integer
	case zapcore.BoolType:
		return f.Integer == 1
	case zapcore.Float64Type:
		return math.Float64frombits(uint64(f.Integer))
	case zapcore.Float32Type:
		return float32(math.Float64frombits(uint64(f.Integer)))
	case zapcore.TimeType, zapcore.TimeFullType:
		if f.Interface != nil {
			return f.Interface
		}
		return f.Integer
	default:
		if f.Interface != nil {
			return f.Interface
		}
		return f.String
	}
}
