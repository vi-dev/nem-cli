#!/usr/bin/env bash
# Validate conventional commit subjects.
#
#   lint-commits.sh --message <file>    one message (git commit-msg hook)
#   lint-commits.sh --range <a>..<b>    every non-merge commit in a range (CI)
set -euo pipefail

# Types accepted by release-please-config.json, plus the ones it ignores.
readonly TYPES='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
readonly SUBJECT_RE="^(${TYPES})(\([a-z0-9._/-]+\))?!?: [A-Z]"
readonly MAX_SUBJECT=72

# release-please authors its own release commits and always lowercases them;
# rejecting those would deadlock the release PR.
is_exempt() {
	case "$1" in
	"chore(main): release "*) return 0 ;;
	Revert\ *) return 0 ;;
	esac
	return 1
}

fail() {
	printf 'invalid commit subject: %s\n  %s\n\n' "$2" "$1" >&2
}

check_subject() {
	local subject="$1" ok=0
	is_exempt "${subject}" && return 0

	if ! printf '%s' "${subject}" | grep -Eq "${SUBJECT_RE}"; then
		if printf '%s' "${subject}" | grep -Eq "^(${TYPES})(\([a-z0-9._/-]+\))?!?: [a-z]"; then
			fail "${subject}" "description must start with a capital letter"
		else
			fail "${subject}" "expected '<type>(<scope>)!: <Description>' with type in ${TYPES}"
		fi
		ok=1
	fi
	if [ "${#subject}" -gt "${MAX_SUBJECT}" ]; then
		fail "${subject}" "subject is ${#subject} chars, limit is ${MAX_SUBJECT}"
		ok=1
	fi
	return "${ok}"
}

main() {
	local mode="${1:-}" arg="${2:-}" rc=0

	case "${mode}" in
	--message)
		[ -n "${arg}" ] || { echo "usage: $0 --message <file>" >&2; exit 2; }
		# A commit-msg file carries the body and comment lines too.
		check_subject "$(head -n 1 "${arg}")" || rc=1
		;;
	--range)
		[ -n "${arg}" ] || { echo "usage: $0 --range <base>..<head>" >&2; exit 2; }
		while IFS= read -r subject; do
			[ -n "${subject}" ] || continue
			check_subject "${subject}" || rc=1
		done < <(git log --no-merges --format=%s "${arg}")
		;;
	*)
		echo "usage: $0 --message <file> | --range <base>..<head>" >&2
		exit 2
		;;
	esac

	if [ "${rc}" -ne 0 ]; then
		cat >&2 <<'EOF'
Commit subjects must read: <type>(<scope>)!: <Capitalized description>
  feat: Add release pipeline and install script
  fix(catalog): Skip unsynced catalogs during resolution
EOF
	fi
	return "${rc}"
}

main "$@"
