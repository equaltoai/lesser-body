package instanceapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-body/internal/downloadgrant"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const (
	installerGrantPathPattern = "/instance/downloads/installer-grants/{grantId}"
	installerGrantBoundRoute  = "/instance/ba/mcp"

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
// The real install-pack renderer lands in #356; this route consumes grants and
// returns the ZIP bytes supplied by this seam without implementing renderer
// layout, manifest, or client-specific pack generation here.
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
// grant is consumed. The #356 renderer will provide the production implementation.
func WithInstallerPackProvider(provider InstallerPackProvider) Option {
	return func(opts *options) {
		if opts == nil {
			return
		}
		opts.installerPackProvider = provider
	}
}

func defaultOptions() options {
	return options{
		downloadGrantStoreFactory: func() (DownloadGrantStore, error) {
			return downloadgrant.Default()
		},
	}
}

func applyOptions(custom []Option) options {
	opts := defaultOptions()
	for _, opt := range custom {
		if opt != nil {
			opt(&opts)
		}
	}
	return opts
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
