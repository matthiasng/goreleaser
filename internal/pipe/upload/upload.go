// Package upload provides a Pipe that push using HTTP
package upload

import (
	"fmt"
	h "net/http"
	"strings"

	"github.com/goreleaser/goreleaser/internal/artifact"
	"github.com/goreleaser/goreleaser/internal/deprecate"
	"github.com/goreleaser/goreleaser/internal/http"
	"github.com/goreleaser/goreleaser/internal/pipe"
	"github.com/goreleaser/goreleaser/pkg/config"
	"github.com/goreleaser/goreleaser/pkg/context"
	"github.com/pkg/errors"
)

// Pipe for http publishing
type Pipe struct{}

// String returns the description of the pipe
func (Pipe) String() string {
	return "http upload"
}

// Default sets the pipe defaults
func (Pipe) Default(ctx *context.Context) error {
	if len(ctx.Config.Puts) > 0 {
		deprecate.Notice("puts")
		ctx.Config.Uploads = append(ctx.Config.Uploads, ctx.Config.Puts...)
	}

	for i := range ctx.Config.Uploads {
		if ctx.Config.Uploads[i].Mode == "" {
			ctx.Config.Uploads[i].Mode = http.ModeArchive
		}

		if ctx.Config.Uploads[i].Method == "" {
			ctx.Config.Uploads[i].Method = h.MethodPut
		}
	}

	return nil
}

func targetURLResolver(upload *config.Upload) http.TargetURLResolver {
	return func(ctx *context.Context, config *http.Config, artifact *artifact.Artifact) string {
		targetURL := upload.Target

		// target url need to contain the artifact name unless the custom artifact name is used
		if !upload.CustomArtifactName {
			if !strings.HasSuffix(targetURL, "/") {
				targetURL += "/"
			}
			targetURL += artifact.Name
		}

		return targetURL
	}
}

func header(upload *config.Upload) http.HeaderGenerator {
	return func(artifact *artifact.Artifact) (map[string]string, error) {
		var headers = map[string]string{}
		if upload.ChecksumHeader != "" {
			sum, err := artifact.Checksum("sha256")
			if err != nil {
				return nil, err
			}
			headers[upload.ChecksumHeader] = sum
		}

		return headers, nil
	}
}

// #todo Gibt es jetzt 3 mal, in Artifactory, Upload und Http
func misconfigured(upload *config.Upload, reason string) error {
	return pipe.Skip(fmt.Sprintf("upload section '%s' is not configured properly (%s)", upload.Name, reason))
}

// Publish uploads
func (Pipe) Publish(ctx *context.Context) error {
	if len(ctx.Config.Uploads) == 0 {
		return pipe.Skip("uploads section is not configured")
	}

	configs := []http.Config{}
	for _, upload := range ctx.Config.Uploads {
		upload := upload

		if upload.Target == "" {
			return misconfigured(&upload, "missing target")
		}

		configs = append(configs, http.Config{
			Name:              upload.Name,
			IDs:               upload.IDs,
			Username:          upload.Username,
			Mode:              upload.Mode,
			Method:            upload.Method,
			TrustedCerts:      upload.TrustedCerts,
			Checksum:          upload.Checksum,
			Signature:         upload.Signature,
			TargetURLResolver: targetURLResolver(&upload),
			Header:            header(&upload),
		})
	}

	// Check requirements for every upload we have configured.
	// If not fulfilled, we can skip this pipeline
	for _, config := range configs {
		config := config
		if skip := http.CheckConfig(ctx, &config, "upload"); skip != nil {
			return pipe.Skip(skip.Error())
		}
	}

	return http.Upload(ctx, configs, "upload", func(res *h.Response) error {
		if c := res.StatusCode; c < 200 || 299 < c {
			return errors.Errorf("unexpected http response status: %s", res.Status)
		}
		return nil
	})
}
