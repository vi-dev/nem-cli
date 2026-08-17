package fetch

import "fmt"

// ArtifactNotFoundError reports that some part of resolving or downloading
// an artifact could not be found upstream. Missing identifies which part:
// "repo", "tag", "platform", or "url". URL, when known, names the exact
// location that failed.
type ArtifactNotFoundError struct{ Name, Version, Platform, Missing, URL string }

func (e *ArtifactNotFoundError) Error() string {
	msg := fmt.Sprintf("artifact for %s@%s (%s) not found: missing %s", e.Name, e.Version, e.Platform, e.Missing)
	if e.URL != "" {
		msg += " (" + e.URL + ")"
	}
	return msg
}

// ChecksumMismatchError reports that a downloaded artifact's sha256 did not
// match the pinned checksum.
type ChecksumMismatchError struct{ Name, Version, Platform, Got, Want string }

func (e *ChecksumMismatchError) Error() string {
	return fmt.Sprintf("checksum mismatch for %s@%s (%s): got %s, want %s", e.Name, e.Version, e.Platform, e.Got, e.Want)
}
