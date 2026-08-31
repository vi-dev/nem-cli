#!/usr/bin/env bash
# Validate commit messages.
#
#   lint-commits.sh --message <file>    one message (git commit-msg hook)
#   lint-commits.sh --range <a>..<b>    every non-merge commit in a range (CI)
set -euo pipefail

# Types accepted by release-please-config.json, plus the ones it ignores.
readonly TYPES='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
readonly SUBJECT_RE="^(${TYPES})(\([a-z0-9._/-]+\))?!?: [A-Z]"
readonly WRONG_CASE_RE="^(${TYPES})(\([a-z0-9._/-]+\))?!?: [a-z]"
readonly CASE_HINT='description must start with a capital letter'
readonly MAX_SUBJECT=72
readonly ATTRIBUTION_RE='co-authored-by:.*claude|anthropic|claude\.ai/code|claude-session:'
readonly EXAMPLES='  feat: Add release pipeline and install script
  fix(catalog): Skip unsynced catalogs during resolution'

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
	printf 'invalid commit message: %s\n  %s\n\n' "$2" "$1" >&2
}

check_subject() {
	local subject="$1" ok=0
	is_exempt "${subject}" && return 0

	if ! printf '%s' "${subject}" | grep -Eq "${SUBJECT_RE}"; then
		if printf '%s' "${subject}" | grep -Eq "${WRONG_CASE_RE}"; then
			fail "${subject}" "${CASE_HINT}"
		else
			fail "${subject}" "expected '<type>(<scope>)!: <description>' with type in ${TYPES}"
		fi
		ok=1
	fi
	if [ "${#subject}" -gt "${MAX_SUBJECT}" ]; then
		fail "${subject}" "subject is ${#subject} chars, limit is ${MAX_SUBJECT}"
		ok=1
	fi
	return "${ok}"
}

check_attribution() {
	local message="$1" match
	if match=$(printf '%s' "${message}" | grep -iE "${ATTRIBUTION_RE}" | head -n 1) && [ -n "${match}" ]; then
		fail "${match}" "AI attribution is not allowed in commit messages"
		return 1
	fi
	return 0
}

main() {
	local mode="${1:-}" arg="${2:-}" rc=0 message

	case "${mode}" in
	--message)
		[ -n "${arg}" ] || { echo "usage: $0 --message <file>" >&2; exit 2; }
		message=$(sed -n '/^# -\{1,\} >8 -\{1,\}$/q;p' "${arg}" | grep -v '^#' || true)
		check_subject "$(printf '%s' "${message}" | head -n 1)" || rc=1
		check_attribution "${message}" || rc=1
		;;
	--range)
		[ -n "${arg}" ] || { echo "usage: $0 --range <base>..<head>" >&2; exit 2; }
		local hash
		while IFS= read -r hash; do
			[ -n "${hash}" ] || continue
			message=$(git log -1 --format=%B "${hash}")
			check_subject "$(printf '%s' "${message}" | head -n 1)" || rc=1
			check_attribution "${message}" || rc=1
		done < <(git log --no-merges --format=%H "${arg}")
		;;
	*)
		echo "usage: $0 --message <file> | --range <base>..<head>" >&2
		exit 2
		;;
	esac

	if [ "${rc}" -ne 0 ]; then
		cat >&2 <<EOF
Commit subjects must read: <type>(<scope>)!: <description>
${EXAMPLES}
Messages must carry no AI attribution or session links.
EOF
	fi
	return "${rc}"
}

main "$@"
