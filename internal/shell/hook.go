package shell

import "fmt"

const (
	// BeginMarker opens the nem-managed block inside an rc file.
	BeginMarker = "# >>> nem >>>"
	// EndMarker closes the nem-managed block inside an rc file.
	EndMarker = "# <<< nem <<<"
)

// hookBlockBody is the shared shape of the bash and zsh hook blocks; %[1]s
// is the shell name passed to `nem env --shell` / `nem completion`, %[2]s
// is the dialect-specific directory-change hook registration.
const hookBlockBody = `export NEM_ORIGINAL_PATH="${NEM_ORIGINAL_PATH-$PATH}"

nem() {
  command nem "$@"
  local __nem_rc=$?
  case "$1" in
    use|unuse|lock|sync)
      eval "$(command nem env --shell %[1]s)"
      ;;
  esac
  return $__nem_rc
}

__nem_hook() {
  eval "$(command nem env --shell %[1]s)"
}

%[2]s

__nem_hook

source <(command nem completion %[1]s 2>/dev/null) 2>/dev/null || true
`

const bashChpwdHook = `case "$PROMPT_COMMAND" in
  *__nem_hook*) ;;
  *) PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND$'\n'}__nem_hook" ;;
esac`

const zshChpwdHook = `autoload -Uz add-zsh-hook
add-zsh-hook chpwd __nem_hook`

// HookBlock renders the shell code nem installs into an rc file to
// activate ambient day-to-day use. It saves NEM_ORIGINAL_PATH once,
// registers a directory-change hook that re-evaluates `nem env` on
// every cd (chpwd for zsh; a PROMPT_COMMAND entry for bash, appended
// without clobbering whatever PROMPT_COMMAND already held), runs that
// hook once immediately so the current directory activates without
// waiting for the next cd, wraps `nem` in a function so a
// `use`/`unuse`/`lock`/`sync` subcommand also re-evaluates the environment,
// and sources shell completions on a best-effort basis — a failing
// completion command must not break the rest of the block. The
// returned string carries no markers; InstallBlock adds those when
// splicing it into an rc file.
//
// HookBlock is only ever called for Bash or Zsh: the cmd layer rejects
// Fish before reaching here. An unrecognized dialect returns an empty
// string rather than guess at a shell it was never asked to support.
func HookBlock(d Dialect) string {
	switch d {
	case Bash:
		return fmt.Sprintf(hookBlockBody, "bash", bashChpwdHook)
	case Zsh:
		return fmt.Sprintf(hookBlockBody, "zsh", zshChpwdHook)
	default:
		return ""
	}
}
