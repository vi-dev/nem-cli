package envx

import "testing"

func TestExpand(t *testing.T) {
	// Create a lookup function for testing
	lookup := func(name string) (string, bool) {
		vars := map[string]string{
			"HOME":  "/home/user",
			"USER":  "alice",
			"A":     "hello",
			"B":     "world",
			"EMPTY": "",
		}
		val, ok := vars[name]
		return val, ok
	}

	tests := []struct {
		name   string
		input  string
		lookup func(name string) (string, bool)
		want   string
	}{
		// Basic $NAME expansion
		{"dollar_name", "$HOME/x", lookup, "/home/user/x"},
		{"dollar_name_user", "$USER", lookup, "alice"},

		// ${NAME} expansion
		{"brace_name", "${HOME}", lookup, "/home/user"},
		{"brace_with_suffix", "${A}b", lookup, "hellob"},

		// $$ → literal $
		{"double_dollar", "$$", lookup, "$"},
		{"double_dollar_text", "x$$y", lookup, "x$y"},

		// Unknown variables expand to ""
		{"unknown_var", "$UNKNOWN", lookup, ""},
		{"unknown_brace", "${UNKNOWN}", lookup, ""},

		// $ followed by non-name-start chars stays literal
		{"dollar_digit", "$1", lookup, "$1"},
		{"dollar_dash", "$-", lookup, "$-"},
		{"dollar_paren", "$(foo)", lookup, "$(foo)"},

		// Trailing $ stays literal
		{"trailing_dollar", "text$", lookup, "text$"},

		// Adjacent expansions
		{"adjacent_brace", "${A}${B}", lookup, "helloworld"},

		// Unterminated ${ - treat as literal
		{"unterminated_brace", "${A", lookup, "${A"},
		{"unterminated_brace_2", "${UNKNOWN", lookup, "${UNKNOWN"},

		// Empty variable
		{"empty_var", "$EMPTY", lookup, ""},
		{"empty_brace", "${EMPTY}", lookup, ""},

		// Mixed cases
		{"mixed_1", "$HOME/$USER", lookup, "/home/user/alice"},
		{"mixed_2", "prefix_${A}_suffix", lookup, "prefix_hello_suffix"},
		{"no_vars", "no variables here", lookup, "no variables here"},

		// Complex cases
		{"dollar_at_end", "text$", lookup, "text$"},
		{"dollar_space", "$ foo", lookup, "$ foo"},

		// isSet contract: unset vars (isSet=false) should expand to ""
		{"unset_dollar", "$STALE", func(name string) (string, bool) {
			if name == "STALE" {
				return "stale_value", false
			}
			return "", false
		}, ""},
		{"unset_brace", "${STALE}", func(name string) (string, bool) {
			if name == "STALE" {
				return "stale_value", false
			}
			return "", false
		}, ""},

		// Invalid braced names - should emit literally
		{"invalid_braced_nested", "${${A}}", lookup, "${${A}}"},
		{"invalid_braced_digit_first", "${1A}", lookup, "${1A}"},
		{"invalid_braced_space", "${A B}", lookup, "${A B}"},
		{"invalid_braced_dash", "${A-B}", lookup, "${A-B}"},
		{"invalid_braced_empty", "${}", lookup, "${}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Expand(tt.input, tt.lookup)
			if result != tt.want {
				t.Errorf("Expand(%q, lookup) = %q, want %q", tt.input, result, tt.want)
			}
		})
	}
}
