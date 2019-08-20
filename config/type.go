package config

import (
	"strconv"
	"strings"
)

// ValueType is a list of known value type
type ValueType int

// Possible values for the ValueType enum.
const (
	TypeUnknown ValueType = iota
	TypeString
	TypeStringList
	TypeInteger
	TypeBoolean
)

func convertBoolean(value string) (bool, error) {
	value = strings.ToLower(value)
	result, err := strconv.ParseBool(value)
	if err != nil {
		// We also support "yes" and "no"
		if value == "yes" {
			result = true
			err = nil
		} else if value == "no" {
			result = false
			err = nil
		}
	}
	return result, err
}
