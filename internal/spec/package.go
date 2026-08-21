package spec

// Package is the parsed shape of a pkg.yaml (schema v2). Parse produces it;
// schema validation beyond shape lives in Validate.
type Package struct {
	Schema           int
	Name             string
	Description      string
	Homepage         string
	License          string
	Platforms        []Platform // empty = all supported
	Deps             []Dep
	Versions         []VersionEntry // newest first; [0] = latest
	VersionDiscovery *Discovery     // nil when absent
	Artifact         Artifact
	Install          []Action
	Bins             []string // dirs or files exposed on PATH; default ["bin"]
	Libs             []string // dirs contributing to the loader path; nil = none
	Env              []EnvExport
	Build            *Build // nil when absent
}

// DepKind is how a dependent consumes a dependency: run (execute its
// binaries) or link (link against its libraries).
type DepKind string

const (
	DepKindRun  DepKind = "run"
	DepKindLink DepKind = "link"
)

// Dep is a dependency reference, optionally version-pinned and
// platform-constrained.
type Dep struct {
	Name, Version string
	Platforms     []Platform
	Kind          DepKind // "" parses to DepKindRun
	Compat        string  // dotted-numeric soname range; link deps only
}

// VersionEntry is one entry in a package's versions list.
type VersionEntry struct {
	Version      string
	Sha256       map[string]string // key "os/arch"; nil for bare-scalar (oci) entries
	SourceSha256 string
}

// Discovery configures automatic version discovery for a package.
type Discovery struct {
	GitHub *GitHubDiscovery
	GitLab *GitLabDiscovery
	Git    *GitDiscovery
	HTTP   *HTTPDiscovery
	OCI    string
}

// GitHubDiscovery discovers versions from GitHub tags: filter regex on raw
// tag names, then the prefix and suffix are stripped.
type GitHubDiscovery struct{ Repo, Filter, Prefix, Suffix string }

// GitLabDiscovery discovers versions from a gitlab.com project's tags;
// same tag handling as GitHubDiscovery.
type GitLabDiscovery struct{ Repo, Filter, Prefix, Suffix string }

// GitDiscovery discovers versions from the tags of any git repository
// served over smart HTTP; URL is the clone URL.
type GitDiscovery struct{ URL, Filter, Prefix, Suffix string }

// HTTPDiscovery discovers versions by fetching URL and matching Filter
// against the body: each match's first capture group (or the full match
// when the regex has no groups) is a version.
type HTTPDiscovery struct{ URL, Filter string }

// Artifact is the download source for a package's versions.
type Artifact struct {
	URL    string
	GitHub *GitHubAsset
	OCI    string
}

// GitHubAsset locates a release asset within a GitHub repo.
type GitHubAsset struct{ Repo, Asset string }

// Action is one step of a package's install sequence, optionally
// platform-constrained. Exactly one action field is set.
type Action struct {
	Extract   *ExtractAction
	Copy      *CopyAction
	Move      *MoveAction
	Mkdir     string
	Platforms []Platform
}

// ExtractAction extracts a downloaded archive, stripping Strip leading
// path components.
type ExtractAction struct{ Strip int }

// CopyAction copies Src to Dst, optionally setting Dst's file mode.
type CopyAction struct {
	Src, Dst string
	Mode     uint32
}

// MoveAction moves Src to Dst.
type MoveAction struct{ Src, Dst string }

// EnvExport is an environment variable a package exports, optionally
// platform-constrained.
type EnvExport struct {
	Name, Value string
	Platforms   []Platform
}

// Build describes how to build a package from source when no prebuilt
// artifact is available.
type Build struct {
	Deps      []Dep
	Source    struct{ URL string }
	Steps     []BuildStep
	Output    string
	Normalize *bool // nil = true: run the standard output normalization
}

// BuildStep is one shell command of a source build, optionally
// platform-constrained.
type BuildStep struct {
	Run       string
	Platforms []Platform
}
