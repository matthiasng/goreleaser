// Package artifactory provides a Pipe that push to artifactory
package artifactory

// artifactoryBuild contains information for an artifactoy build
// See: https://www.jfrog.com/confluence/display/JFROG/Build+Integration
type artifactoryBuild struct {
	Version        string
	Name           string // #todo go mod name oder einen eigenen mit template vars
	Number         string // #todo Was erlaubt artifactory hier ? am besten den tag verwenden ?
	BuildAgent     artifactoryBuildAgent
	Agent          artifactoryAgent
	Started        string // #todo format: yyyy-MM-dd'T'HH:mm:ss.SSSZ
	DurationMillis string
	URL            string `json:"url"`
	VcsRevision    string
	VcsURL         string `json:"vcsUrl"`
	Modules        []artifactoryModule
}

// artifactoryBuildAgent contains information about the build tool
type artifactoryBuildAgent struct {
	Name    string
	Version string // #todo goreleaser version
}

// artifactoryAgent contains information about the build server
type artifactoryAgent struct {
	Name    string
	Version string // #todo goreleaser version
}

type artifactoryModule struct {
	Id           string
	Properties   map[string]string
	Artifacts    []artifactoryArtifact
	Dependencies []artifactoryDependency
}

type artifactoryArtifact struct {
	Type string
	Sha1 string
	Md5  string
	Name string
}

type artifactoryDependency struct {
	Type   string
	Sha1   string
	Md5    string
	Id     string
	Scopes string
}
