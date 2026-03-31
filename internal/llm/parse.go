package llm

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ExtractJSON extracts the first JSON object or array from LLM output text.
// LLMs often wrap JSON in markdown code blocks or add explanation text.
func ExtractJSON(text string) string {
	// Try to find JSON in code blocks first: ```json ... ``` or ``` ... ```
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\n?(\\{.*?\\}|\\[.*?\\])\\s*\n?```")
	if m := re.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}

	// Try to find raw JSON object
	start := strings.Index(text, "{")
	if start == -1 {
		return ""
	}
	// Find matching closing brace
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// ParseJSONFromLLM extracts JSON from LLM output and unmarshals into target.
// Returns false if no valid JSON found (target is left unchanged).
func ParseJSONFromLLM(text string, target any) bool {
	jsonStr := ExtractJSON(text)
	if jsonStr == "" {
		return false
	}
	return json.Unmarshal([]byte(jsonStr), target) == nil
}
