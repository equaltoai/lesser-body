package lambdaentry

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

// NewAPIGatewayHandler adapts an AppTheory app to API Gateway REST API v1 in a way
// that supports response-streaming integrations on the /mcp route.
//
// Why: API Gateway "response-streaming-invocations" expects an
// APIGatewayProxyStreamingResponse, even when the response is not SSE. AppTheory
// only emits streaming responses for text/event-stream, so we wrap /mcp to always
// return a streaming response type when possible.
func NewAPIGatewayHandler(app *apptheory.App) func(context.Context, events.APIGatewayProxyRequest) (any, error) {
	return func(ctx context.Context, event events.APIGatewayProxyRequest) (any, error) {
		if ctx == nil {
			ctx = context.Background()
		}

		req := requestFromAPIGatewayProxy(event)
		resp := app.Serve(ctx, req)

		// /mcp POST + GET are wired as response-streaming integrations. Always return
		// a streaming response type for those methods so non-SSE JSON-RPC calls
		// (e.g. initialize/tools/list) don't fail with an API Gateway integration error.
		if isMcpPath(req.Path) && isStreamingMcpMethod(req.Method) && !resp.IsBase64 {
			return apigatewayProxyStreamingResponseFromResponse(resp), nil
		}

		return apigatewayProxyResponseFromResponse(resp), nil
	}
}

func isMcpPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	path = strings.TrimSuffix(path, "/")
	return path == "/mcp" || strings.HasSuffix(path, "/mcp")
}

func isStreamingMcpMethod(method string) bool {
	method = strings.TrimSpace(method)
	return strings.EqualFold(method, "POST") || strings.EqualFold(method, "GET")
}

func requestFromAPIGatewayProxy(event events.APIGatewayProxyRequest) apptheory.Request {
	path := event.Path
	if path == "" {
		path = event.RequestContext.Path
	}

	method := event.HTTPMethod
	if method == "" {
		method = event.RequestContext.HTTPMethod
	}

	return apptheory.Request{
		Method:   method,
		Path:     path,
		Query:    queryFromProxyEvent(event.QueryStringParameters, event.MultiValueQueryStringParameters),
		Headers:  headersFromProxyEvent(event.Headers, event.MultiValueHeaders),
		Body:     []byte(event.Body),
		IsBase64: event.IsBase64Encoded,
	}
}

func headersFromProxyEvent(single map[string]string, multi map[string][]string) map[string][]string {
	out := map[string][]string{}
	for key, values := range multi {
		out[key] = append([]string(nil), values...)
	}
	for key, value := range single {
		if _, ok := out[key]; ok {
			continue
		}
		out[key] = []string{value}
	}
	return out
}

func queryFromProxyEvent(single map[string]string, multi map[string][]string) map[string][]string {
	out := map[string][]string{}
	for key, values := range multi {
		out[key] = append([]string(nil), values...)
	}
	for key, value := range single {
		if _, ok := out[key]; ok {
			continue
		}
		out[key] = []string{value}
	}
	return out
}

func apigatewayProxyResponseFromResponse(resp apptheory.Response) events.APIGatewayProxyResponse {
	out := events.APIGatewayProxyResponse{
		StatusCode:        resp.Status,
		Headers:           map[string]string{},
		MultiValueHeaders: map[string][]string{},
		IsBase64Encoded:   resp.IsBase64,
		Body:              string(resp.Body),
	}

	for key, values := range resp.Headers {
		if len(values) == 0 {
			continue
		}
		out.Headers[key] = values[0]
		out.MultiValueHeaders[key] = append([]string(nil), values...)
	}

	if len(resp.Cookies) > 0 {
		out.Headers["set-cookie"] = resp.Cookies[0]
		out.MultiValueHeaders["set-cookie"] = append([]string(nil), resp.Cookies...)
	}

	if resp.IsBase64 {
		out.Body = base64.StdEncoding.EncodeToString(resp.Body)
	}

	return out
}

func apigatewayProxyStreamingResponseFromResponse(resp apptheory.Response) *events.APIGatewayProxyStreamingResponse {
	body := io.Reader(bytes.NewReader(resp.Body))
	if resp.BodyReader != nil {
		if len(resp.Body) > 0 {
			body = io.MultiReader(bytes.NewReader(resp.Body), resp.BodyReader)
		} else {
			body = resp.BodyReader
		}
	}

	out := &events.APIGatewayProxyStreamingResponse{
		StatusCode:        resp.Status,
		Headers:           map[string]string{},
		MultiValueHeaders: map[string][]string{},
		Cookies:           append([]string(nil), resp.Cookies...),
		Body:              body,
	}

	for key, values := range resp.Headers {
		if len(values) == 0 {
			continue
		}
		out.Headers[key] = values[0]
		out.MultiValueHeaders[key] = append([]string(nil), values...)
	}

	return out
}
