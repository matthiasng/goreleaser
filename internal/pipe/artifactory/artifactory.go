// Package artifactory provides a Pipe that push to artifactory
package artifactory

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	h "net/http"

	"github.com/goreleaser/goreleaser/internal/http"
	"github.com/goreleaser/goreleaser/internal/pipe"
	"github.com/goreleaser/goreleaser/pkg/context"
)

// artifactoryResponse reflects the response after an upload request
// to Artifactory.
type artifactoryResponse struct {
	Repo              string               `json:"repo,omitempty"`
	Path              string               `json:"path,omitempty"`
	Created           string               `json:"created,omitempty"`
	CreatedBy         string               `json:"createdBy,omitempty"`
	DownloadURI       string               `json:"downloadUri,omitempty"`
	MimeType          string               `json:"mimeType,omitempty"`
	Size              string               `json:"size,omitempty"`
	Checksums         artifactoryChecksums `json:"checksums,omitempty"`
	OriginalChecksums artifactoryChecksums `json:"originalChecksums,omitempty"`
	URI               string               `json:"uri,omitempty"`
}

// artifactoryChecksums reflects the checksums generated by
// Artifactory
type artifactoryChecksums struct {
	SHA1   string `json:"sha1,omitempty"`
	MD5    string `json:"md5,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Pipe for Artifactory
type Pipe struct{}

// String returns the description of the pipe
func (Pipe) String() string {
	return "artifactory"
}

// Default sets the pipe defaults
func (Pipe) Default(ctx *context.Context) error {
	for i := range ctx.Config.Artifactories {
		if ctx.Config.Artifactories[i].Mode == "" {
			ctx.Config.Artifactories[i].Mode = http.ModeArchive
		}
	}

	return nil
}

// Publish artifacts to artifactory
//
// Docs: https://www.jfrog.com/confluence/display/RTF/Artifactory+REST+API#ArtifactoryRESTAPI-Example-DeployinganArtifact
func (Pipe) Publish(ctx *context.Context) error {
	if len(ctx.Config.Artifactories) == 0 {
		return pipe.Skip("artifactory section is not configured")
	}

	configs := []http.Config{}
	for _, instance := range ctx.Config.Artifactories {
		configs = append(configs, http.Config{
			Name:               instance.Name,
			IDs:                instance.IDs,
			Target:             instance.Target,
			Username:           instance.Username,
			Mode:               instance.Mode,
			Method:             h.MethodPut,
			TrustedCerts:       instance.TrustedCerts,
			Checksum:           instance.Checksum,
			Signature:          instance.Signature,
			CustomArtifactName: false,
			ChecksumHeader:     "", // #todo
		})
	}

	// Check requirements for every instance we have configured.
	// If not fulfilled, we can skip this pipeline
	for _, config := range configs {
		config := config
		if skip := http.CheckConfig(ctx, &config, "artifactory"); skip != nil {
			return pipe.Skip(skip.Error())
		}
	}

	return http.Upload(ctx, configs, "artifactory", func(res *h.Response) error {
		if err := checkResponse(res); err != nil {
			return err
		}
		var r artifactoryResponse
		err := json.NewDecoder(res.Body).Decode(&r)
		return err
	})
}

// An ErrorResponse reports one or more errors caused by an API request.
type errorResponse struct {
	Response *h.Response // HTTP response that caused this error
	Errors   []Error     `json:"errors"` // more detail on individual errors
}

func (r *errorResponse) Error() string {
	return fmt.Sprintf("%v %v: %d %+v",
		r.Response.Request.Method, r.Response.Request.URL,
		r.Response.StatusCode, r.Errors)
}

// An Error reports more details on an individual error in an ErrorResponse.
type Error struct {
	Status  int    `json:"status"`  // Error code
	Message string `json:"message"` // Message describing the error.
}

// checkResponse checks the API response for errors, and returns them if
// present. A response is considered an error if it has a status code outside
// the 200 range.
// API error responses are expected to have either no response
// body, or a JSON response body that maps to ErrorResponse. Any other
// response body will be silently ignored.
func checkResponse(r *h.Response) error {
	if c := r.StatusCode; 200 <= c && c <= 299 {
		return nil
	}
	errorResponse := &errorResponse{Response: r}
	data, err := ioutil.ReadAll(r.Body)
	if err == nil && data != nil {
		err := json.Unmarshal(data, errorResponse)
		if err != nil {
			return err
		}
	}
	return errorResponse
}
