// Package types defines the internal Provider contract and the minimal
// OpenAI/Anthropic wire types shared across the gateway.
package types

import (
	"context"
	"net/http"
	"time"

	"apiforge/internal/pool"
)

// Capability is an optional surface a provider can serve beyond OpenAI chat.
type Capability string

const (
	CapResponses Capability = "responses" // OpenAI Responses API (/v1/responses)
	CapImages    Capability = "images"    // image generation (/v1/images/generations)
	CapAnthropic Capability = "anthropic" // native Anthropic Messages (/v1/messages)
)

// ModelObject is one entry of the OpenAI /v1/models list.
type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"` // always "model"
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// RequestContext carries per-request state to a provider.
type RequestContext struct {
	RequestID string
	Ctx       context.Context
	// AccountPin is a manual account id from the `x-apiforge-account` header.
	AccountPin string
	// Session is an optional sticky-session key (`x-apiforge-session` header):
	// requests sharing it route to the same account when healthy.
	Session string
}

// ModelObjects builds OpenAI /v1/models entries from a list of ids.
func ModelObjects(ids []string, ownedBy string) []ModelObject {
	now := time.Now().Unix()
	out := make([]ModelObject, len(ids))
	for i, id := range ids {
		out[i] = ModelObject{ID: id, Object: "model", Created: now, OwnedBy: ownedBy}
	}
	return out
}

// ImageInput is one uploaded image (edits / img2img), base64-encoded so the
// normalized image request stays a plain JSON []byte across the provider API.
type ImageInput struct {
	B64         string `json:"b64"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
}

// ImageRequest is the gateway's normalized image request. The server builds it
// from either a JSON /images/generations body or a multipart /images/edits form;
// a non-empty Images slice marks it as an edit (image-to-image).
type ImageRequest struct {
	Model        string       `json:"model"`
	Prompt       string       `json:"prompt"`
	N            int          `json:"n,omitempty"`
	Size         string       `json:"size,omitempty"`
	Quality      string       `json:"quality,omitempty"`
	Background   string       `json:"background,omitempty"`
	OutputFormat string       `json:"output_format,omitempty"`
	Images       []ImageInput `json:"images,omitempty"`
	Mask         *ImageInput  `json:"mask,omitempty"`
}

// Provider is the core contract every upstream implements. OpenAI Chat
// Completions is the lingua franca; extra surfaces are opt-in via the
// capability interfaces below (checked by type assertion in the router).
//
// Methods that serve a request return the upstream *http.Response (real or
// synthesized via io.Pipe for translating providers); the server streams it
// back to the client with io.Copy so memory stays flat regardless of payload.
type Provider interface {
	ID() string
	Capabilities() []Capability
	Init(ctx context.Context) error
	IsReady() bool
	ListModels() []ModelObject
	OwnsModel(model string) bool
	ChatCompletion(rctx RequestContext, body []byte) (*http.Response, error)
}

// ResponsesProvider natively serves the OpenAI Responses API.
type ResponsesProvider interface {
	Provider
	Responses(rctx RequestContext, body []byte) (*http.Response, error)
}

// ImagesProvider serves image generation.
type ImagesProvider interface {
	Provider
	Images(rctx RequestContext, body []byte) (*http.Response, error)
}

// AnthropicProvider natively serves the Anthropic Messages API.
type AnthropicProvider interface {
	Provider
	Messages(rctx RequestContext, body []byte) (*http.Response, error)
	CountTokens(rctx RequestContext, body []byte) (*http.Response, error)
}

// Pooled is implemented by providers backed by an account pool, exposing the
// type-erased pool surface for the /admin account-control API.
type Pooled interface {
	AccountPool() pool.Admin
}

// HasCapability reports whether p declares c.
func HasCapability(p Provider, c Capability) bool {
	for _, x := range p.Capabilities() {
		if x == c {
			return true
		}
	}
	return false
}
