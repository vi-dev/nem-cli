package envx

// Expand performs POSIX $VAR / ${VAR} expansion against lookup.
// No command substitution / arithmetic / globbing / tilde.
// $$ expands to literal $.
// An unknown var (isSet=false) expands to "". lookup returns (value, isSet).
// A $ not followed by a valid name-start char or { stays literal.
// An unterminated ${NAME (no closing brace) is treated as literal (lenient).
// A ${NAME} whose body is not a valid name is also treated as literal.
// Name chars: [A-Za-z_][A-Za-z0-9_]*
func Expand(s string, lookup func(name string) (string, bool)) string {
	var result []rune
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' {
			result = append(result, runes[i])
			continue
		}

		// We have a $, check what follows
		if i+1 >= len(runes) {
			// Trailing $
			result = append(result, '$')
			continue
		}

		next := runes[i+1]

		// Handle $$
		if next == '$' {
			result = append(result, '$')
			i++
			continue
		}

		// Handle ${NAME}
		if next == '{' {
			// Find the closing brace and extract the variable name
			closeIdx := -1
			for j := i + 2; j < len(runes); j++ {
				if runes[j] == '}' {
					closeIdx = j
					break
				}
			}

			if closeIdx == -1 {
				// Unterminated ${..., treat as literal
				result = append(result, runes[i:]...)
				break
			}

			varName := string(runes[i+2 : closeIdx])

			// Validate that varName matches grammar [A-Za-z_][A-Za-z0-9_]*
			if !isValidName(varName) {
				// Invalid name, emit ${<body>} as literal
				result = append(result, '$')
				result = append(result, '{')
				result = append(result, []rune(varName)...)
				result = append(result, '}')
				i = closeIdx
				continue
			}

			value, ok := lookup(varName)
			if !ok {
				value = ""
			}
			result = append(result, []rune(value)...)
			i = closeIdx
			continue
		}

		// Handle $NAME
		if isNameStart(next) {
			nameStart := i + 1
			nameEnd := nameStart

			for nameEnd < len(runes) && isNameChar(runes[nameEnd]) {
				nameEnd++
			}

			varName := string(runes[nameStart:nameEnd])
			value, ok := lookup(varName)
			if !ok {
				value = ""
			}
			result = append(result, []rune(value)...)
			i = nameEnd - 1
			continue
		}

		// $ followed by non-name-start char stays literal
		result = append(result, '$')
	}

	return string(result)
}

// isNameStart checks if a rune is a valid variable name start character.
func isNameStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// isNameChar checks if a rune is a valid variable name character.
func isNameChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// isValidName checks if a string is a valid variable name [A-Za-z_][A-Za-z0-9_]*.
func isValidName(s string) bool {
	if len(s) == 0 {
		return false
	}
	if !isNameStart(rune(s[0])) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isNameChar(rune(s[i])) {
			return false
		}
	}
	return true
}
