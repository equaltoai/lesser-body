package instanceapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/agentcontent"
	"github.com/equaltoai/lesser-body/internal/baserver"
	"github.com/equaltoai/lesser-body/internal/downloadgrant"
	"github.com/equaltoai/lesser-body/internal/installpack"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

const (
	installerGrantPathPattern = "/instance/downloads/installer-grants/{grantId}"
	installerGrantBoundRoute  = baserver.InstallerGrantBoundRoute

	// AWS Lambda's synchronous response payload ceiling is 6 MB. Keep the
	// installer-pack body below that ceiling before the Lambda adapters serialize
	// the response envelope.
	lambdaResponsePayloadCeilingBytes = 6 * 1024 * 1024
)

// DownloadGrantStore is the minimal Store.Consume seam used by the public Ba
// installer grant redemption route. Production supplies internal/downloadgrant.Store;
// tests may inject a TableTheory-backed test store.
type DownloadGrantStore interface {
	Consume(context.Context, downloadgrant.ConsumeInput) (*downloadgrant.ConsumeResult, error)
}

// InstallerPackProvider is the minimal pack-rendering seam for the grant route.
// The default provider re-renders deterministic Ba install packs from the
// consumed grant binding and current account-scoped agentcontent records.
type InstallerPackProvider interface {
	BuildInstallerPack(context.Context, InstallerPackRequest) (*InstallerPack, error)
}

// InstallerPackRequest identifies the consumed grant and normalized binding for
// an install-pack render.
type InstallerPackRequest struct {
	GrantID string
	Binding downloadgrant.Binding
}

// InstallerPack is the binary ZIP payload returned for a consumed grant.
type InstallerPack struct {
	ZIPBytes []byte
}

type downloadGrantStoreFactory func() (DownloadGrantStore, error)

type options struct {
	downloadGrantStoreFactory downloadGrantStoreFactory
	installerPackProvider     InstallerPackProvider
	baToolOptions             []baserver.Option
	baContentStoreFactory     func() (baserver.AgentContentStore, error)
	baInstanceEndpoint        string
	baNamespace               string
}

// Option customizes the instance-plane app composition.
type Option func(*options)

// WithDownloadGrantStore injects a concrete download grant store. It is intended
// for tests and tightly-scoped composition; production uses downloadgrant.Default.
func WithDownloadGrantStore(store DownloadGrantStore) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.downloadGrantStoreFactory = func() (DownloadGrantStore, error) {
			if store == nil {
				return nil, fmt.Errorf("download grant store is required")
			}
			return store, nil
		}
		if issuer, ok := store.(baserver.DownloadGrantIssuer); ok {
			opts.baToolOptions = append(opts.baToolOptions, baserver.WithDownloadGrantIssuer(issuer))
		}
	}
}

// WithDownloadGrantStoreFactory injects a lazy store factory for tests or
// alternate instanceapp composition while preserving the Store.Consume contract.
func WithDownloadGrantStoreFactory(factory func() (DownloadGrantStore, error)) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.downloadGrantStoreFactory = factory
	}
}

// WithInstallerPackProvider injects the install-pack ZIP provider used after a
// grant is consumed. Production uses the Ba installpack-backed default provider.
func WithInstallerPackProvider(provider InstallerPackProvider) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.installerPackProvider = provider
	}
}

// WithBaContentStore injects the account-scoped content store used by the Ba
// plan tool and the default grant redemption provider.
func WithBaContentStore(store baserver.AgentContentStore) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.baContentStoreFactory = func() (baserver.AgentContentStore, error) {
			if store == nil {
				return nil, fmt.Errorf("agent content store is required")
			}
			return store, nil
		}
		opts.baToolOptions = append(opts.baToolOptions, baserver.WithAgentContentStore(store))
	}
}

// WithBaInstanceEndpoint injects the canonical CDK-derived instance endpoint
// template used to derive pack stage domains and grant download URLs.
func WithBaInstanceEndpoint(endpoint string) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.baInstanceEndpoint = strings.TrimSpace(endpoint)
		opts.baToolOptions = append(opts.baToolOptions, baserver.WithInstanceEndpoint(endpoint))
	}
}

// WithBaNamespace injects the namespace used in Ba plan metadata and grant bindings.
func WithBaNamespace(namespace string) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.baNamespace = strings.TrimSpace(namespace)
		opts.baToolOptions = append(opts.baToolOptions, baserver.WithNamespace(namespace))
	}
}

// WithBaToolOptions injects additional baserver options, for example a test
// rate limiter.
func WithBaToolOptions(toolOpts ...baserver.Option) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.baToolOptions = append(opts.baToolOptions, toolOpts...)
	}
}

func defaultOptions() options {
	return options{
		downloadGrantStoreFactory: func() (DownloadGrantStore, error) {
			return downloadgrant.Default()
		},
		baContentStoreFactory: func() (baserver.AgentContentStore, error) {
			return agentcontent.Default()
		},
		baInstanceEndpoint: strings.TrimSpace(os.Getenv(baserver.EnvInstanceMCPEndpoint)),
		baNamespace:        baserver.DefaultNamespace,
	}
}

func applyOptions(custom []Option) options {
	opts := defaultOptions()
	for _, opt := range custom {
		if opt != nil {
			opt(&opts)
		}
	}
	if opts.installerPackProvider == nil {
		opts.installerPackProvider = &baInstallerPackProvider{
			contentStoreFactory: opts.baContentStoreFactory,
			instanceEndpoint:    opts.baInstanceEndpoint,
			namespace:           opts.baNamespace,
			renderer:            installpack.NewRenderer(),
		}
	}
	return opts
}

type baInstallerPackProvider struct {
	contentStoreFactory func() (baserver.AgentContentStore, error)
	instanceEndpoint    string
	namespace           string
	renderer            baserver.Renderer
}

// NewBaInstallerPackProvider builds the default Ba grant redemption provider
// around an injected content store. It is intended for tests and narrow
// composition paths; production uses the lazy default provider from New.
func NewBaInstallerPackProvider(store baserver.AgentContentStore, instanceEndpoint string, namespace string) InstallerPackProvider {
	return &baInstallerPackProvider{
		contentStoreFactory: func() (baserver.AgentContentStore, error) {
			if store == nil {
				return nil, fmt.Errorf("agent content store is required")
			}
			return store, nil
		},
		instanceEndpoint: strings.TrimSpace(instanceEndpoint),
		namespace:        strings.TrimSpace(namespace),
		renderer:         installpack.NewRenderer(),
	}
}

func (p *baInstallerPackProvider) BuildInstallerPack(ctx context.Context, req InstallerPackRequest) (*InstallerPack, error) {
	if p == nil {
		return nil, fmt.Errorf("installer pack provider is nil")
	}
	if p.contentStoreFactory == nil {
		return nil, fmt.Errorf("agent content store is not configured")
	}
	store, err := p.contentStoreFactory()
	if err != nil {
		return nil, err
	}
	agentID, err := baserver.AgentIDFromPackID(req.Binding.PackID)
	if err != nil {
		return nil, err
	}
	namespace := strings.TrimSpace(req.Binding.Namespace)
	if namespace == "" {
		namespace = p.namespace
	}
	renderer := p.renderer
	if renderer == nil {
		renderer = installpack.NewRenderer()
	}
	packInput, err := baserver.BuildPackInput(ctx, baserver.PackInputRequest{
		ContentStore:     store,
		InstanceEndpoint: p.instanceEndpoint,
		Namespace:        namespace,
		Account:          req.Binding.Account,
		AgentID:          agentID,
		Actor:            req.Binding.Actor,
		Client:           req.Binding.Client,
		Profile:          installpack.Profile(req.Binding.Profile),
		PackID:           req.Binding.PackID,
		PackDigest:       req.Binding.PackDigest,
	})
	if err != nil {
		return nil, err
	}
	pack, err := renderer.Render(ctx, packInput.RenderRequest)
	if err != nil {
		return nil, err
	}
	if pack == nil || len(pack.ZIPBytes) == 0 {
		return nil, fmt.Errorf("installer pack render returned empty zip")
	}
	return &InstallerPack{ZIPBytes: pack.ZIPBytes}, nil
}

func installerGrantHandler(opts options) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		provider := opts.installerPackProvider
		if provider == nil {
			return noStoreJSON(http.StatusNotImplemented, "installer_pack_provider_not_configured", "installer pack rendering is not configured"), nil
		}
		if opts.downloadGrantStoreFactory == nil {
			return noStoreJSON(http.StatusServiceUnavailable, "download_grant_store_unavailable", "download grant store is unavailable"), nil
		}

		grantID := strings.TrimSpace(ctx.Param("grantId"))
		binding := installerGrantBindingFromQuery(ctx)
		token := strings.TrimSpace(ctx.Query("token"))

		store, err := opts.downloadGrantStoreFactory()
		if err != nil || store == nil {
			return noStoreJSON(http.StatusServiceUnavailable, "download_grant_store_unavailable", "download grant store is unavailable"), nil
		}

		consume, err := store.Consume(ctx.Context(), downloadgrant.ConsumeInput{
			GrantID: grantID,
			Token:   token,
			Binding: binding,
		})
		if err != nil {
			if errors.Is(err, downloadgrant.ErrGrantIDRequired) || errors.Is(err, downloadgrant.ErrRawTokenRequired) || errors.Is(err, downloadgrant.ErrInvalidBinding) {
				return downloadGrantNotFoundResponse(), nil
			}
			return noStoreJSON(http.StatusInternalServerError, "download_grant_consume_failed", "download grant could not be consumed"), nil
		}
		if consume == nil {
			return downloadGrantNotFoundResponse(), nil
		}

		switch consume.Outcome {
		case downloadgrant.ConsumeOutcomeConsumed:
			// Continue below.
		case downloadgrant.ConsumeOutcomeReplay:
			return noStoreJSON(http.StatusGone, "download_grant_consumed", "download grant has already been consumed"), nil
		case downloadgrant.ConsumeOutcomeNotFound, downloadgrant.ConsumeOutcomeExpired, downloadgrant.ConsumeOutcomeTokenMismatch, downloadgrant.ConsumeOutcomeBindingMismatch:
			return downloadGrantNotFoundResponse(), nil
		default:
			return downloadGrantNotFoundResponse(), nil
		}
		if consume.Grant == nil {
			return noStoreJSON(http.StatusInternalServerError, "download_grant_consume_failed", "download grant could not be consumed"), nil
		}

		pack, err := provider.BuildInstallerPack(ctx.Context(), InstallerPackRequest{
			GrantID: consume.GrantID,
			Binding: consume.Grant.Binding,
		})
		if err != nil || pack == nil || len(pack.ZIPBytes) == 0 {
			return noStoreJSON(http.StatusInternalServerError, "installer_pack_render_failed", "installer pack could not be rendered"), nil
		}
		if len(pack.ZIPBytes) >= lambdaResponsePayloadCeilingBytes {
			return noStoreJSON(http.StatusInternalServerError, "installer_pack_too_large", "installer pack exceeds the response payload limit"), nil
		}

		resp := apptheory.Binary(http.StatusOK, pack.ZIPBytes, "application/zip")
		resp.SetHeader("Cache-Control", "no-store")
		resp.SetHeader("Content-Disposition", "attachment; filename=\""+safeInstallerFilename(consume.Grant.Binding.PackID)+"\"")
		return resp, nil
	}
}

func installerGrantBindingFromQuery(ctx *apptheory.Context) downloadgrant.Binding {
	if ctx == nil {
		return downloadgrant.Binding{}
	}
	return downloadgrant.Binding{
		Account:    ctx.Query("account"),
		Actor:      ctx.Query("actor"),
		Namespace:  ctx.Query("namespace"),
		Route:      installerGrantBoundRoute,
		Client:     ctx.Query("client"),
		Profile:    ctx.Query("profile"),
		PackID:     ctx.Query("pack_id"),
		PackDigest: ctx.Query("pack_digest"),
	}
}

func downloadGrantNotFoundResponse() *apptheory.Response {
	return noStoreJSON(http.StatusNotFound, "download_grant_not_found", "download grant is not available")
}

func noStoreJSON(status int, code string, message string) *apptheory.Response {
	resp := apptheory.MustJSON(status, map[string]any{
		"error": map[string]any{
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
	resp.SetHeader("Cache-Control", "no-store")
	return resp
}

func safeInstallerFilename(packID string) string {
	stem := sanitizeFilenameStem(packID)
	if stem == "" {
		stem = "installer-pack"
	}
	return "installer-" + stem + ".zip"
}

func sanitizeFilenameStem(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range in {
		keep := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if len(out) > 96 {
		out = strings.Trim(out[:96], ".-_")
	}
	return out
}
