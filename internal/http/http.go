// Package http implements functionality common to HTTP uploading pipelines.
package http

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	h "net/http"
	"os"
	"runtime"
	"strings"

	"github.com/apex/log"
	"github.com/pkg/errors"

	"github.com/goreleaser/goreleaser/internal/artifact"
	"github.com/goreleaser/goreleaser/internal/pipe"
	"github.com/goreleaser/goreleaser/internal/semerrgroup"
	"github.com/goreleaser/goreleaser/internal/tmpl"
	"github.com/goreleaser/goreleaser/pkg/context"
)

const (
	// ModeBinary uploads only compiled binaries
	ModeBinary = "binary"
	// ModeArchive uploads release archives
	ModeArchive = "archive"
)

type asset struct {
	ReadCloser io.ReadCloser
	Size       int64
}

type assetOpenFunc func(string, *artifact.Artifact) (*asset, error)

// nolint: gochecknoglobals
var assetOpen assetOpenFunc

// TODO: fix this.
// nolint: gochecknoinits
func init() {
	assetOpenReset()
}

func assetOpenReset() {
	assetOpen = assetOpenDefault
}

func assetOpenDefault(kind string, a *artifact.Artifact) (*asset, error) {
	f, err := os.Open(a.Path)
	if err != nil {
		return nil, err
	}
	s, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if s.IsDir() {
		return nil, errors.Errorf("%s: upload failed: the asset to upload can't be a directory", kind)
	}
	return &asset{
		ReadCloser: f,
		Size:       s.Size(),
	}, nil
}

// #todo
type TargetUrlBuild func(*h.Response) (string, error)

type Config struct {
	Name               string
	IDs                []string
	Target             string
	Username           string
	Mode               string
	Method             string
	ChecksumHeader     string
	TrustedCerts       string
	Checksum           bool
	Signature          bool
	CustomArtifactName bool
}

// CheckConfig validates an upload configuration returning a descriptive error when appropriate
func CheckConfig(ctx *context.Context, config *Config, kind string) error {
	if config.Target == "" {
		return misconfigured(kind, config, "missing target")
	}

	if config.Name == "" {
		return misconfigured(kind, config, "missing name")
	}

	if config.Mode != ModeArchive && config.Mode != ModeBinary {
		return misconfigured(kind, config, "mode must be 'binary' or 'archive'")
	}

	if _, err := getUsername(ctx, config, kind); err != nil {
		return err
	}

	if _, err := getPassword(ctx, config, kind); err != nil {
		return err
	}

	if config.TrustedCerts != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(config.TrustedCerts)) {
		return misconfigured(kind, config, "no certificate could be added from the specified trusted_certificates configuration")
	}

	return nil
}

func getUsername(ctx *context.Context, config *Config, kind string) (string, error) {
	if config.Username != "" {
		return config.Username, nil
	}
	var key = fmt.Sprintf("%s_%s_USERNAME", strings.ToUpper(kind), strings.ToUpper(config.Name))
	user, ok := ctx.Env[key]
	if !ok {
		return "", misconfigured(kind, config, fmt.Sprintf("missing username or %s environment variable", key))
	}
	return user, nil
}

func getPassword(ctx *context.Context, config *Config, kind string) (string, error) {
	var key = fmt.Sprintf("%s_%s_SECRET", strings.ToUpper(kind), strings.ToUpper(config.Name))
	pwd, ok := ctx.Env[key]
	if !ok {
		return "", misconfigured(kind, config, fmt.Sprintf("missing %s environment variable", key))
	}
	return pwd, nil
}

func misconfigured(kind string, config *Config, reason string) error {
	return pipe.Skip(fmt.Sprintf("%s section '%s' is not configured properly (%s)", kind, config.Name, reason))
}

// ResponseChecker is a function capable of validating an http server response.
// It must return and error when the response must be considered a failure.
type ResponseChecker func(*h.Response) error

// Upload does the actual uploading work
func Upload(ctx *context.Context, configs []Config, kind string, check ResponseChecker) error {
	if ctx.SkipPublish {
		return pipe.ErrSkipPublishEnabled
	}

	// Handle every configured upload
	for _, config := range configs {
		config := config
		filter, err := filter(config, kind)
		if err != nil {
			return err
		}

		if err := uploadWithFilter(ctx, &config, filter, kind, check); err != nil {
			return err
		}
	}

	return nil
}

// filter create the filter for the upload
func filter(config Config, kind string) (artifact.Filter, error) {
	filters := []artifact.Filter{}
	if config.Checksum {
		filters = append(filters, artifact.ByType(artifact.Checksum))
	}
	if config.Signature {
		filters = append(filters, artifact.ByType(artifact.Signature))
	}
	// We support two different modes
	//	- "archive": Upload all artifacts
	//	- "binary": Upload only the raw binaries
	switch v := strings.ToLower(config.Mode); v {
	case ModeArchive:
		filters = append(filters,
			artifact.ByType(artifact.UploadableArchive),
			artifact.ByType(artifact.LinuxPackage),
		)
	case ModeBinary:
		filters = append(filters, artifact.ByType(artifact.UploadableBinary))
	default:
		err := fmt.Errorf("%s: mode \"%s\" not supported", kind, v)
		log.WithFields(log.Fields{
			kind:   config.Name,
			"mode": v,
		}).Error(err.Error())
		return nil, err
	}

	var filter = artifact.Or(filters...)
	if len(config.IDs) > 0 {
		filter = artifact.And(filter, artifact.ByIDs(config.IDs...))
	}

	return filter, nil
}

func uploadWithFilter(ctx *context.Context, config *Config, filter artifact.Filter, kind string, check ResponseChecker) error {
	var artifacts = ctx.Artifacts.Filter(filter).List()
	log.Debugf("will upload %d artifacts", len(artifacts))
	var g = semerrgroup.New(ctx.Parallelism)
	for _, artifact := range artifacts {
		artifact := artifact
		g.Go(func() error {
			return uploadAsset(ctx, config, artifact, kind, check)
		})
	}
	return g.Wait()
}

// uploadAsset uploads file to target and logs all actions
func uploadAsset(ctx *context.Context, config *Config, artifact *artifact.Artifact, kind string, check ResponseChecker) error {
	username, err := getUsername(ctx, config, kind)
	if err != nil {
		return err
	}

	secret, err := getPassword(ctx, config, kind)
	if err != nil {
		return err
	}

	// Generate the target url
	targetURL, err := resolveTargetTemplate(ctx, config, artifact)
	if err != nil {
		msg := fmt.Sprintf("%s: error while building the target url", kind)
		log.WithField("instance", config.Name).WithError(err).Error(msg)
		return errors.Wrap(err, msg)
	}

	// Handle the artifact
	asset, err := assetOpen(kind, artifact)
	if err != nil {
		return err
	}
	defer asset.ReadCloser.Close() // nolint: errcheck

	// target url need to contain the artifact name unless the custom
	// artifact name is used
	if !config.CustomArtifactName {
		if !strings.HasSuffix(targetURL, "/") {
			targetURL += "/"
		}
		targetURL += artifact.Name
	}
	log.Debugf("generated target url: %s", targetURL)

	var headers = map[string]string{}
	if config.ChecksumHeader != "" {
		sum, err := artifact.Checksum("sha256")
		if err != nil {
			return err
		}
		headers[config.ChecksumHeader] = sum
	}

	res, err := uploadAssetToServer(ctx, config, targetURL, username, secret, headers, asset, check)
	if err != nil {
		msg := fmt.Sprintf("%s: upload failed", kind)
		log.WithError(err).WithFields(log.Fields{
			"instance": config.Name,
			"username": username,
		}).Error(msg)
		return errors.Wrap(err, msg)
	}
	if err := res.Body.Close(); err != nil {
		log.WithError(err).Warn("failed to close response body")
	}

	log.WithFields(log.Fields{
		"instance": config.Name,
		"mode":     config.Mode,
	}).Info("uploaded successful")

	return nil
}

// uploadAssetToServer uploads the asset file to target
func uploadAssetToServer(ctx *context.Context, config *Config, target, username, secret string, headers map[string]string, a *asset, check ResponseChecker) (*h.Response, error) {
	req, err := newUploadRequest(config.Method, target, username, secret, headers, a)
	if err != nil {
		return nil, err
	}

	return executeHTTPRequest(ctx, config, req, check)
}

// newUploadRequest creates a new h.Request for uploading
func newUploadRequest(method, target, username, secret string, headers map[string]string, a *asset) (*h.Request, error) {
	req, err := h.NewRequest(method, target, a.ReadCloser)
	if err != nil {
		return nil, err
	}
	req.ContentLength = a.Size
	req.SetBasicAuth(username, secret)

	for k, v := range headers {
		req.Header.Add(k, v)
	}

	return req, err
}

func getHTTPClient(config *Config) (*h.Client, error) {
	if config.TrustedCerts == "" {
		return h.DefaultClient, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		if runtime.GOOS == "windows" {
			// on windows ignore errors until golang issues #16736 & #18609 get fixed
			pool = x509.NewCertPool()
		} else {
			return nil, err
		}
	}
	pool.AppendCertsFromPEM([]byte(config.TrustedCerts)) // already validated certs checked by CheckConfig
	return &h.Client{
		Transport: &h.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
			},
		},
	}, nil
}

// executeHTTPRequest processes the http call with respect of context ctx
func executeHTTPRequest(ctx *context.Context, config *Config, req *h.Request, check ResponseChecker) (*h.Response, error) {
	client, err := getHTTPClient(config)
	if err != nil {
		return nil, err
	}
	log.Debugf("executing request: %s %s (headers: %v)", req.Method, req.URL, req.Header)
	resp, err := client.Do(req)
	if err != nil {
		// If we got an error, and the context has been canceled,
		// the context's error is probably more useful.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		return nil, err
	}

	defer resp.Body.Close() // nolint: errcheck

	err = check(resp)
	if err != nil {
		// even though there was an error, we still return the response
		// in case the caller wants to inspect it further
		return resp, err
	}

	return resp, err
}

// resolveTargetTemplate returns the resolved target template with replaced variables
// Those variables can be replaced by the given context, goos, goarch, goarm and more
func resolveTargetTemplate(ctx *context.Context, config *Config, artifact *artifact.Artifact) (string, error) {
	var replacements = map[string]string{}
	if config.Mode == ModeBinary {
		// TODO: multiple archives here
		replacements = ctx.Config.Archives[0].Replacements
	}
	return tmpl.New(ctx).
		WithArtifact(artifact, replacements).
		Apply(config.Target)
}
