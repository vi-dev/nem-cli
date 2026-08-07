package fetch

import "fmt"

// ArtifactNotFoundError reports that some part of resolving or downloading
// an artifact could not be found upstream. Missing identifies which part:
// "repo", "tag", "platform", or "url".
type ArtifactNotFoundError struct{ Name, Version, Platform, Missing string }

func (e *ArtifactNotFoundError) Error() string {
	return fmt.Sprintf("artifact for %s@%s (%s) not found: missing %s", e.Name, e.Version, e.Platform, e.Missing)
}

// ChecksumMismatchError reports that a downloaded artifact's sha256 did not
// match the pinned checksum.
type ChecksumMismatchError struct{ Name, Version, Platform, Got, Want string }

func (e *ChecksumMismatchError) Error() string {
	return fmt.Sprintf("checksum mismatch for %s@%s (%s): got %s, want %s", e.Name, e.Version, e.Platform, e.Got, e.Want)
}
