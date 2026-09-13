package mcputil

import (
	"fmt"
	"math"

	"github.com/mark3labs/mcp-go/mcp"
)

// Integers validates supplied arguments as integers between minimum and math.MaxInt32.
// Missing arguments are allowed so handlers can apply their defaults.
func Integers(request mcp.CallToolRequest, minimum int, names ...string) error {
	for _, name := range names {
		value, ok := request.GetArguments()[name]
		if !ok {
			continue
		}
		var number float64
		switch value := value.(type) {
		case int:
			number = float64(value)
		case float64:
			number = value
		default:
			return fmt.Errorf("%s must be an integer", name)
		}
		if math.IsNaN(number) || number < float64(minimum) || number > math.MaxInt32 || number != math.Trunc(number) {
			return fmt.Errorf("%s must be an integer between %d and %d", name, minimum, math.MaxInt32)
		}
	}
	return nil
}

// Strings extracts an array of strings, returning nil when the argument is absent.
func Strings(request mcp.CallToolRequest, name string) ([]string, error) {
	value, ok := request.GetArguments()[name]
	if !ok {
		return nil, nil
	}
	switch value := value.(type) {
	case []string:
		return value, nil
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of strings", name)
			}
			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
}
