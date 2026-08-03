package iampolicy

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// conditionReport collects what a condition evaluation could not resolve:
// context keys the request did not carry (AWS reports these as
// MissingContextValues) and operators this evaluator does not implement.
type conditionReport struct {
	missing     []string
	unsupported []string
}

func (r *conditionReport) addMissing(key string) {
	for _, existing := range r.missing {
		if strings.EqualFold(existing, key) {
			return
		}
	}
	r.missing = append(r.missing, key)
}

func (r *conditionReport) addUnsupported(msg string) {
	for _, existing := range r.unsupported {
		if existing == msg {
			return
		}
	}
	r.unsupported = append(r.unsupported, msg)
}

// evaluateConditions evaluates all condition blocks against ctx.
// All operator blocks must be satisfied (AND across operators). Within an
// operator, all key→values pairs must be satisfied (AND across keys; OR across
// the values for each key).
//
// An unknown operator never resolves the statement: it is recorded in the
// report and the condition is reported unmet, leaving the caller to decide
// (the simulator raises AWS's PolicyEvaluation error, enforcement fails
// closed).
func evaluateConditions(cond map[string]map[string][]string, ctx map[string]string, rep *conditionReport) bool {
	if len(cond) == 0 {
		return true
	}
	met := true
	for operator, keyValues := range cond {
		for key, values := range keyValues {
			expandedValues := expandPolicyVariablesList(values, ctx)
			ctxVal, present := ctx[strings.ToLower(key)]

			effectiveOperator := operator
			if strings.HasSuffix(operator, "IfExists") {
				effectiveOperator = strings.TrimSuffix(operator, "IfExists")
				if !present {
					// IfExists: missing context keys satisfy the condition.
					continue
				}
			}

			if effectiveOperator == "Null" {
				if !applyNullCondition(present, expandedValues) {
					met = false
				}
				continue
			}

			if !present {
				rep.addMissing(key)
				met = false
				continue
			}

			matched, known := applyConditionOperator(effectiveOperator, ctxVal, expandedValues)
			if !known {
				rep.addUnsupported(fmt.Sprintf("condition operator %q is not implemented by the Overcast policy evaluator", operator))
				met = false
				continue
			}
			if !matched {
				met = false
			}
		}
	}
	return met
}

// applyConditionOperator tests a single condition key value against the
// provided set of policy values using the given operator.
// Returns (matched, knownOperator).
func applyConditionOperator(operator, ctxVal string, policyVals []string) (bool, bool) {
	switch operator {
	case "StringEquals":
		for _, v := range policyVals {
			if ctxVal == v {
				return true, true
			}
		}
		return false, true

	case "StringNotEquals":
		for _, v := range policyVals {
			if ctxVal == v {
				return false, true
			}
		}
		return true, true

	case "StringEqualsIgnoreCase":
		for _, v := range policyVals {
			if strings.EqualFold(ctxVal, v) {
				return true, true
			}
		}
		return false, true

	case "StringNotEqualsIgnoreCase":
		for _, v := range policyVals {
			if strings.EqualFold(ctxVal, v) {
				return false, true
			}
		}
		return true, true

	case "StringLike":
		for _, v := range policyVals {
			if globMatch(v, ctxVal, false) {
				return true, true
			}
		}
		return false, true

	case "StringNotLike":
		for _, v := range policyVals {
			if globMatch(v, ctxVal, false) {
				return false, true
			}
		}
		return true, true

	case "ArnEquals", "ArnLike":
		for _, v := range policyVals {
			if globMatch(v, ctxVal, false) {
				return true, true
			}
		}
		return false, true

	case "ArnNotEquals", "ArnNotLike":
		for _, v := range policyVals {
			if globMatch(v, ctxVal, false) {
				return false, true
			}
		}
		return true, true

	case "Bool":
		for _, v := range policyVals {
			if strings.EqualFold(ctxVal, v) {
				return true, true
			}
		}
		return false, true

	case "IpAddress":
		for _, v := range policyVals {
			if ipMatchesCIDR(ctxVal, v) {
				return true, true
			}
		}
		return false, true

	case "NotIpAddress":
		for _, v := range policyVals {
			if ipMatchesCIDR(ctxVal, v) {
				return false, true
			}
		}
		return true, true

	case "NumericEquals", "NumericNotEquals", "NumericLessThan", "NumericLessThanEquals", "NumericGreaterThan", "NumericGreaterThanEquals":
		return applyNumericCondition(operator, ctxVal, policyVals)

	case "DateEquals", "DateNotEquals", "DateLessThan", "DateLessThanEquals", "DateGreaterThan", "DateGreaterThanEquals":
		return applyDateCondition(operator, ctxVal, policyVals)

	default:
		return false, false // unknown operator — the caller reports it
	}
}

func applyNumericCondition(operator, ctxVal string, policyVals []string) (bool, bool) {
	ctxNum, err := strconv.ParseFloat(ctxVal, 64)
	if err != nil {
		return false, true
	}
	if operator == "NumericNotEquals" {
		for _, v := range policyVals {
			pv, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue
			}
			if ctxNum == pv {
				return false, true
			}
		}
		return true, true
	}
	for _, v := range policyVals {
		pv, err := strconv.ParseFloat(v, 64)
		if err != nil {
			continue
		}
		if numericCompareMatches(operator, ctxNum, pv) {
			return true, true
		}
	}
	return false, true
}

func applyDateCondition(operator, ctxVal string, policyVals []string) (bool, bool) {
	ctxTime, ok := parseIAMTime(ctxVal)
	if !ok {
		return false, true
	}
	if operator == "DateNotEquals" {
		for _, v := range policyVals {
			policyTime, ok := parseIAMTime(v)
			if !ok {
				continue
			}
			if ctxTime.Equal(policyTime) {
				return false, true
			}
		}
		return true, true
	}
	for _, v := range policyVals {
		policyTime, ok := parseIAMTime(v)
		if !ok {
			continue
		}
		if dateCompareMatches(operator, ctxTime, policyTime) {
			return true, true
		}
	}
	return false, true
}

func parseIAMTime(raw string) (time.Time, bool) {
	ts := strings.TrimSpace(raw)
	if ts == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func dateCompareMatches(operator string, left, right time.Time) bool {
	switch operator {
	case "DateEquals", "DateNotEquals":
		return left.Equal(right)
	case "DateLessThan":
		return left.Before(right)
	case "DateLessThanEquals":
		return left.Before(right) || left.Equal(right)
	case "DateGreaterThan":
		return left.After(right)
	case "DateGreaterThanEquals":
		return left.After(right) || left.Equal(right)
	default:
		return false
	}
}

func numericCompareMatches(operator string, left, right float64) bool {
	switch operator {
	case "NumericEquals":
		return left == right
	case "NumericLessThan":
		return left < right
	case "NumericLessThanEquals":
		return left <= right
	case "NumericGreaterThan":
		return left > right
	case "NumericGreaterThanEquals":
		return left >= right
	default:
		return false
	}
}

func applyNullCondition(keyPresent bool, policyVals []string) bool {
	for _, v := range policyVals {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			if !keyPresent {
				return true
			}
		case "false":
			if keyPresent {
				return true
			}
		default:
			// Ignore unsupported literal values in a mixed array.
		}
	}
	return false
}

// ipMatchesCIDR reports whether ipStr is contained within cidrOrIP.
// cidrOrIP may be a CIDR block (e.g. "10.0.0.0/8") or a plain IP address.
func ipMatchesCIDR(ipStr, cidrOrIP string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if strings.Contains(cidrOrIP, "/") {
		_, ipNet, err := net.ParseCIDR(cidrOrIP)
		if err != nil {
			return false
		}
		return ipNet.Contains(ip)
	}
	peer := net.ParseIP(cidrOrIP)
	if peer == nil {
		return false
	}
	return ip.Equal(peer)
}
