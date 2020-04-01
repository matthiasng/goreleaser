// Package upload provides a Pipe that push using HTTP
package upload

import (
	h "net/http"

	"github.com/goreleaser/goreleaser/internal/deprecate"
	"github.com/goreleaser/goreleaser/internal/http"
	"github.com/goreleaser/goreleaser/internal/pipe"
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

// Publish uploads
func (Pipe) Publish(ctx *context.Context) error {
	if len(ctx.Config.Uploads) == 0 {
		return pipe.Skip("uploads section is not configured")
	}

	configs := []http.Config{}
	for _, upload := range ctx.Config.Uploads {
		configs = append(configs, http.Config{
			Name:               upload.Name,
			IDs:                upload.IDs,
			Target:             upload.Target,
			Username:           upload.Username,
			Mode:               upload.Mode,
			Method:             upload.Method,
			ChecksumHeader:     upload.ChecksumHeader,
			TrustedCerts:       upload.TrustedCerts,
			Checksum:           upload.Checksum,
			Signature:          upload.Signature,
			CustomArtifactName: upload.CustomArtifactName,
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
