package envx

import "testing"

func TestIsReserved(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Exact reserved names (case-insensitive)
		{"PATH uppercase", "PATH", true},
		{"path lowercase", "path", true},
		{"Path mixed case", "Path", true},
		{"PROMPT_COMMAND", "PROMPT_COMMAND", true},
		{"BASH_ENV", "BASH_ENV", true},
		{"ENV", "ENV", true},
		{"IFS", "IFS", true},
		{"PS1", "PS1", true},
		{"Ps1 lowercase", "ps1", true},
		{"CDPATH", "CDPATH", true},
		{"FPATH", "FPATH", true},
		{"MANPATH", "MANPATH", true},
		{"MAILPATH", "MAILPATH", true},
		{"MODULE_PATH", "MODULE_PATH", true},
		{"FIGNORE", "FIGNORE", true},
		{"PSVAR", "PSVAR", true},
		{"WATCH", "WATCH", true},
		{"GCONV_PATH", "GCONV_PATH", true},
		{"GLIBC_TUNABLES", "GLIBC_TUNABLES", true},
		{"LOCPATH", "LOCPATH", true},
		{"NLSPATH", "NLSPATH", true},
		{"GETCONF_DIR", "GETCONF_DIR", true},
		{"HOSTALIASES", "HOSTALIASES", true},
		{"RESOLV_HOST_CONF", "RESOLV_HOST_CONF", true},

		// NEM_ prefix (case-insensitive)
		{"NEM_HOME", "NEM_HOME", true},
		{"nem_home lowercase", "nem_home", true},
		{"Nem_Home mixed", "Nem_Home", true},
		{"NEM_ANYTHING", "NEM_ANYTHING", true},

		// LD_ prefix (case-insensitive)
		{"LD_PRELOAD", "LD_PRELOAD", true},
		{"ld_preload lowercase", "ld_preload", true},
		{"LD_LIBRARY_PATH", "LD_LIBRARY_PATH", true},

		// DYLD_ prefix (case-insensitive)
		{"DYLD_LIBRARY_PATH", "DYLD_LIBRARY_PATH", true},
		{"dyld_library_path", "dyld_library_path", true},

		// _SET suffix (case-insensitive)
		{"FOO_SET", "FOO_SET", true},
		{"foo_set lowercase", "foo_set", true},
		{"Foo_Set mixed", "Foo_Set", true},
		{"MY_VAR_SET", "MY_VAR_SET", true},

		// Not reserved
		{"KUBECONFIG", "KUBECONFIG", false},
		{"HOME", "HOME", false},
		{"USER", "USER", false},
		{"foo", "foo", false},
		{"FOO", "FOO", false},
		{"my_var", "my_var", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsReserved(tt.input)
			if result != tt.expected {
				t.Errorf("IsReserved(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
