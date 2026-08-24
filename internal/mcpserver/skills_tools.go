package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/equaltoai/lesser-body/internal/lesserapi"
	mcpruntime "github.com/theory-cloud/apptheory/v4/runtime/mcp"
)

const (
	skillCatalogMaxLimit          = 100
	skillLocalFileMaxDecodedBytes = 1 << 20
	skillLocalFilesMaxCount       = 200

	skillInstallStateNotInstalled  = "not_installed"
	skillInstallStateVerifiedMatch = "verified_match"
	skillInstallStateModifiedCopy  = "modified_local_copy"
	skillInstallStateUnknownLocal  = "unknown_local_state"
)

type skillCatalogArgs struct {
	Exposure string `json:"exposure,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

type skillBundleArgs struct {
	SkillID        string          `json:"skill_id"`
	RevisionNumber int             `json:"revision_number"`
	BundleID       string          `json:"bundle_id,omitempty"`
	IncludeContent bool            `json:"include_content,omitempty"`
	LocalFiles     json.RawMessage `json:"local_files,omitempty"`
}

type skillLocalFileObservation struct {
	Path        string
	InstallPath string
	Content     string
	Encoding    string
	HasContent  bool
}

type skillBundleContentSummary struct {
	Mode              string `json:"mode"`
	FilesTotal        int    `json:"files_total"`
	InlineFiles       int    `json:"inline_files"`
	MetadataOnlyFiles int    `json:"metadata_only_files"`
}

type skillBundleVerification struct {
	State             string                        `json:"state"`
	Reason            string                        `json:"reason,omitempty"`
	Source            string                        `json:"source"`
	BundleDigest      string                        `json:"bundle_digest,omitempty"`
	PublicationDigest string                        `json:"publication_digest,omitempty"`
	CheckedFiles      int                           `json:"checked_files"`
	Files             []skillBundleFileVerification `json:"files,omitempty"`
}

type skillBundleFileVerification struct {
	Path            string `json:"path"`
	InstallPath     string `json:"install_path,omitempty"`
	ExpectedDigest  string `json:"expected_digest,omitempty"`
	ComputedDigest  string `json:"computed_digest,omitempty"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	ContentCompared bool   `json:"content_compared"`
}

func registerSkillsTools(r *mcpruntime.ToolRegistry) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}

	for _, tool := range []struct {
		Def     mcpruntime.ToolDef
		Handler mcpruntime.ToolHandler
	}{
		{Def: skillsCatalogDef(), Handler: handleSkillsCatalog},
		{Def: skillBundleGetDef(), Handler: handleSkillBundleGet},
	} {
		if err := registerTool(r, tool.Def, tool.Handler); err != nil {
			return err
		}
	}

	return nil
}

func skillsCatalogDef() mcpruntime.ToolDef {
	return mcpruntime.ToolDef{
		Name:         "skills_catalog",
		Description:  "List approved skill bundles from Lesser's authoritative skills catalog without mutating the client workspace.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: skillsCatalogOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"exposure":{"type":"string","description":"Optional Lesser exposure filter."},
				"limit":{"type":"integer","minimum":1,"maximum":100,"description":"Maximum catalog entries to ask Lesser for."},
				"cursor":{"type":"string","description":"Opaque Lesser pagination cursor from a previous catalog response."}
			}
		}`),
	}
}

func skillBundleGetDef() mcpruntime.ToolDef {
	def := mcpruntime.ToolDef{
		Name:         "skill_bundle_get",
		Description:  "Fetch a selected approved Lesser skill bundle and optionally compare caller-supplied local file bytes to the published digests.",
		Annotations:  readOnlyToolAnnotations(),
		OutputSchema: skillBundleGetOutputSchema(),
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"skill_id":{"type":"string","description":"Lesser skill id from skills_catalog."},
				"revision_number":{"type":"integer","minimum":1,"description":"Approved revision number to fetch."},
				"bundle_id":{"type":"string","description":"Optional catalog bundle.bundle_id selection such as skill:<skillId>:revision:00000001. Provide either bundle_id or both skill_id and revision_number; Body validates that requirement at runtime."},
				"include_content":{"type":"boolean","description":"Ask Lesser to include inline bundle file content when available. Defaults to false."},
				"local_files":{
					"type":"array",
					"description":"Optional caller-observed local files for read-only verification. Omit when the client cannot inspect local files; an empty array means the client inspected and found no installed files.",
					"items":{
						"type":"object",
						"properties":{
							"path":{"type":"string","description":"Bundle-relative file path."},
							"install_path":{"type":"string","description":"Suggested install path from the bundle."},
							"content":{"type":"string","description":"Observed local file bytes as UTF-8 text or base64, used only for digest comparison and not echoed back."},
							"encoding":{"type":"string","enum":["utf-8","text","base64"],"description":"Encoding for content. Defaults to utf-8."}
						}
					}
				}
			}
		}`),
	}
	if TaskRuntimeConfiguredFromEnv() {
		def.Execution = &mcpruntime.ToolExecution{TaskSupport: mcpruntime.TaskSupportOptional}
	}
	return def
}

func handleSkillsCatalog(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in skillCatalogArgs
	if err := unmarshalOptionalObject(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}

	token, err := requireOAuthBearer(ctx)
	if err != nil {
		return skillToolResultFromError(err, "/api/v1/skills/catalog")
	}
	client, err := lesser(ctx)
	if err != nil {
		return skillToolResultFromError(err, "/api/v1/skills/catalog")
	}

	query := url.Values{}
	if exposure := strings.TrimSpace(in.Exposure); exposure != "" {
		query.Set("exposure", exposure)
	}
	if limit := boundedSkillCatalogLimit(in.Limit); limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	if cursor := strings.TrimSpace(in.Cursor); cursor != "" {
		query.Set("cursor", cursor)
	}

	raw, err := client.DoJSON(ctx, http.MethodGet, "/api/v1/skills/catalog", query, token, nil)
	if err != nil {
		return skillToolResultFromError(err, "/api/v1/skills/catalog")
	}

	payload := objectFromRaw(raw)
	payload["authority"] = map[string]any{
		"source":                "lesser",
		"endpoint":              "/api/v1/skills/catalog",
		"catalog_authoritative": true,
		"body_cache_authority":  false,
	}

	return toolJSONResult(payload, nil)
}

func boundedSkillCatalogLimit(limit int) int {
	switch {
	case limit <= 0:
		return 0
	case limit > skillCatalogMaxLimit:
		return skillCatalogMaxLimit
	default:
		return limit
	}
}

func handleSkillBundleGet(ctx context.Context, args json.RawMessage) (*mcpruntime.ToolResult, error) {
	var in skillBundleArgs
	if err := unmarshalOptionalObject(args, &in); err != nil {
		return nil, invalidParams("invalid args: " + err.Error())
	}
	in.SkillID = strings.TrimSpace(in.SkillID)
	in.BundleID = strings.TrimSpace(in.BundleID)
	if in.BundleID != "" {
		parsedSkillID, parsedRevision, parseErr := parseSkillBundleID(in.BundleID)
		if parseErr != nil {
			return nil, invalidParams(parseErr.Error())
		}
		if in.SkillID != "" && in.SkillID != parsedSkillID {
			return nil, invalidParams("bundle_id does not match skill_id")
		}
		if in.RevisionNumber > 0 && in.RevisionNumber != parsedRevision {
			return nil, invalidParams("bundle_id does not match revision_number")
		}
		if in.SkillID == "" {
			in.SkillID = parsedSkillID
		}
		if in.RevisionNumber <= 0 {
			in.RevisionNumber = parsedRevision
		}
	}
	if in.SkillID == "" {
		return nil, invalidParams("skill_id is required unless bundle_id is provided")
	}
	if in.RevisionNumber <= 0 {
		return nil, invalidParams("revision_number must be greater than zero unless bundle_id is provided")
	}
	localFiles, localFilesProvided, err := parseSkillLocalFiles(in.LocalFiles)
	if err != nil {
		return nil, invalidParams(err.Error())
	}

	token, err := requireOAuthBearer(ctx)
	endpointPath := skillBundleEndpointPath(in.SkillID, in.RevisionNumber)
	if err != nil {
		return skillToolResultFromError(err, endpointPath)
	}
	client, err := lesser(ctx)
	if err != nil {
		return skillToolResultFromError(err, endpointPath)
	}

	query := url.Values{}
	if in.IncludeContent {
		query.Set("include_content", "true")
	}
	raw, err := client.DoJSON(ctx, http.MethodGet, endpointPath, query, token, nil)
	if err != nil {
		return skillToolResultFromError(err, endpointPath)
	}

	payload := objectFromRaw(raw)
	bundle, _ := payload["bundle"].(map[string]any)
	content := summarizeSkillBundleContent(bundle)
	verification := verifySkillBundleLocalState(bundle, localFiles, localFilesProvided)
	payload["content"] = content
	payload["verification"] = verification
	payload["authority"] = map[string]any{
		"source":               "lesser",
		"endpoint":             endpointPath,
		"bundle_authoritative": true,
		"body_cache_authority": false,
		"workspace_mutated":    false,
	}

	return toolJSONResult(payload, nil)
}

func unmarshalOptionalObject(args json.RawMessage, out any) error {
	raw := bytes.TrimSpace(args)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if len(raw) > 0 && raw[0] != '{' {
		return fmt.Errorf("arguments must be an object")
	}
	return json.Unmarshal(raw, out)
}

func skillBundleEndpointPath(skillID string, revisionNumber int) string {
	return fmt.Sprintf("/api/v1/skills/%s/revisions/%d/bundle", url.PathEscape(strings.TrimSpace(skillID)), revisionNumber)
}

func parseSkillBundleID(bundleID string) (string, int, error) {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return "", 0, fmt.Errorf("skill_id and revision_number are required unless bundle_id is provided")
	}
	const revisionMarker = ":revision:"
	if !strings.HasPrefix(bundleID, "skill:") || !strings.Contains(bundleID, revisionMarker) {
		return "", 0, fmt.Errorf("bundle_id must use skill:<skillId>:revision:<revisionNumber>")
	}
	parts := strings.SplitN(strings.TrimPrefix(bundleID, "skill:"), revisionMarker, 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", 0, fmt.Errorf("bundle_id must include skill id and revision number")
	}
	revision, err := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(parts[1]), "0"))
	if err != nil || revision <= 0 {
		// A revision of all zeros is not valid; Atoi("") is also invalid.
		return "", 0, fmt.Errorf("bundle_id revision number is invalid")
	}
	return strings.TrimSpace(parts[0]), revision, nil
}

func objectFromRaw(raw any) map[string]any {
	if obj, ok := raw.(map[string]any); ok && obj != nil {
		out := make(map[string]any, len(obj)+2)
		for k, v := range obj {
			out[k] = v
		}
		return out
	}
	return map[string]any{"data": raw}
}

func parseSkillLocalFiles(raw json.RawMessage) ([]skillLocalFileObservation, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}

	var entries []map[string]any
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, true, fmt.Errorf("local_files must be an array of objects")
	}
	if len(entries) > skillLocalFilesMaxCount {
		return nil, true, fmt.Errorf("local_files cannot contain more than %d entries", skillLocalFilesMaxCount)
	}

	out := make([]skillLocalFileObservation, 0, len(entries))
	for i, entry := range entries {
		if entry == nil {
			return nil, true, fmt.Errorf("local_files[%d] must be an object", i)
		}
		path, err := optionalStringField(entry, "path")
		if err != nil {
			return nil, true, fmt.Errorf("local_files[%d].path must be a string", i)
		}
		installPath, err := optionalStringField(entry, "install_path")
		if err != nil {
			return nil, true, fmt.Errorf("local_files[%d].install_path must be a string", i)
		}
		content, hasContent, err := optionalPresentStringField(entry, "content")
		if err != nil {
			return nil, true, fmt.Errorf("local_files[%d].content must be a string", i)
		}
		encoding, err := optionalStringField(entry, "encoding")
		if err != nil {
			return nil, true, fmt.Errorf("local_files[%d].encoding must be a string", i)
		}
		if strings.TrimSpace(path) == "" && strings.TrimSpace(installPath) == "" {
			return nil, true, fmt.Errorf("local_files[%d] requires path or install_path", i)
		}
		if hasContent {
			if _, err := decodeSkillLocalContent(content, encoding); err != nil {
				return nil, true, fmt.Errorf("local_files[%d]: %w", i, err)
			}
		}
		out = append(out, skillLocalFileObservation{
			Path:        strings.TrimSpace(path),
			InstallPath: strings.TrimSpace(installPath),
			Content:     content,
			Encoding:    strings.TrimSpace(encoding),
			HasContent:  hasContent,
		})
	}
	return out, true, nil
}

func optionalStringField(entry map[string]any, field string) (string, error) {
	value, ok := entry[field]
	if !ok || value == nil {
		return "", nil
	}
	out, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("not a string")
	}
	return out, nil
}

func optionalPresentStringField(entry map[string]any, field string) (string, bool, error) {
	value, ok := entry[field]
	if !ok || value == nil {
		return "", false, nil
	}
	out, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("not a string")
	}
	return out, true, nil
}

func decodeSkillLocalContent(content string, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "text":
		decoded := []byte(content)
		if len(decoded) > skillLocalFileMaxDecodedBytes {
			return nil, fmt.Errorf("decoded local file content exceeds %d bytes", skillLocalFileMaxDecodedBytes)
		}
		return decoded, nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content))
		if err != nil {
			return nil, fmt.Errorf("local file content is invalid base64")
		}
		if len(decoded) > skillLocalFileMaxDecodedBytes {
			return nil, fmt.Errorf("decoded local file content exceeds %d bytes", skillLocalFileMaxDecodedBytes)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("local file encoding must be utf-8, text, or base64")
	}
}

func summarizeSkillBundleContent(bundle map[string]any) skillBundleContentSummary {
	files := skillBundleFiles(bundle)
	summary := skillBundleContentSummary{FilesTotal: len(files)}
	for _, file := range files {
		if boolValue(file["content_included"]) {
			summary.InlineFiles++
		} else {
			summary.MetadataOnlyFiles++
		}
	}
	switch {
	case summary.FilesTotal == 0:
		summary.Mode = "no_files"
	case summary.InlineFiles == summary.FilesTotal:
		summary.Mode = "inline"
	case summary.MetadataOnlyFiles == summary.FilesTotal:
		summary.Mode = "metadata_only"
	default:
		summary.Mode = "mixed"
	}
	return summary
}

func verifySkillBundleLocalState(bundle map[string]any, localFiles []skillLocalFileObservation, localFilesProvided bool) skillBundleVerification {
	verification := skillBundleVerification{
		State:  skillInstallStateUnknownLocal,
		Reason: "local_files_not_provided",
		Source: "caller_supplied_local_file_bytes",
	}
	if bundle == nil {
		verification.Reason = "bundle_missing"
		return verification
	}
	if digests, _ := bundle["digests"].(map[string]any); digests != nil {
		verification.BundleDigest = stringValue(digests["bundle_digest"])
		verification.PublicationDigest = stringValue(digests["publication_digest"])
	}

	files := skillBundleFiles(bundle)
	if len(files) == 0 {
		verification.Reason = "bundle_has_no_files"
		return verification
	}
	if !localFilesProvided {
		return verification
	}
	if len(localFiles) == 0 {
		verification.State = skillInstallStateNotInstalled
		verification.Reason = "no_local_files_reported"
		verification.Files = make([]skillBundleFileVerification, 0, len(files))
		for _, file := range files {
			verification.Files = append(verification.Files, skillBundleFileVerification{
				Path:           stringValue(file["path"]),
				InstallPath:    stringValue(file["install_path"]),
				ExpectedDigest: strings.ToLower(strings.TrimSpace(stringValue(file["digest"]))),
				State:          skillInstallStateNotInstalled,
				Reason:         "local_file_not_observed",
			})
		}
		return verification
	}

	localByPath := map[string]skillLocalFileObservation{}
	for _, file := range localFiles {
		if path := normalizedSkillPath(file.Path); path != "" {
			localByPath[path] = file
		}
		if installPath := normalizedSkillPath(file.InstallPath); installPath != "" {
			localByPath[installPath] = file
		}
	}

	verification.Reason = "verified_against_local_file_bytes"
	allVerified := true
	allMissing := true
	anyDrift := false
	anyUnknown := false
	verification.Files = make([]skillBundleFileVerification, 0, len(files))
	for _, bundleFile := range files {
		path := stringValue(bundleFile["path"])
		installPath := stringValue(bundleFile["install_path"])
		expectedDigest := strings.ToLower(strings.TrimSpace(stringValue(bundleFile["digest"])))
		fileVerification := skillBundleFileVerification{
			Path:           path,
			InstallPath:    installPath,
			ExpectedDigest: expectedDigest,
			State:          skillInstallStateUnknownLocal,
		}

		local, ok := localByPath[normalizedSkillPath(path)]
		if !ok {
			local, ok = localByPath[normalizedSkillPath(installPath)]
		}
		if !ok {
			fileVerification.State = skillInstallStateNotInstalled
			fileVerification.Reason = "local_file_not_observed"
			allVerified = false
			anyDrift = true
			verification.Files = append(verification.Files, fileVerification)
			continue
		}
		allMissing = false
		if !local.HasContent {
			fileVerification.Reason = "local_file_content_not_provided"
			allVerified = false
			anyUnknown = true
			verification.Files = append(verification.Files, fileVerification)
			continue
		}
		localBytes, err := decodeSkillLocalContent(local.Content, local.Encoding)
		if err != nil {
			fileVerification.Reason = err.Error()
			allVerified = false
			anyUnknown = true
			verification.Files = append(verification.Files, fileVerification)
			continue
		}
		computedDigest := skillSHA256Digest(localBytes)
		fileVerification.ComputedDigest = computedDigest
		fileVerification.ContentCompared = true
		verification.CheckedFiles++
		if expectedDigest == "" {
			fileVerification.Reason = "bundle_file_digest_missing"
			allVerified = false
			anyUnknown = true
		} else if computedDigest == expectedDigest {
			fileVerification.State = skillInstallStateVerifiedMatch
		} else {
			fileVerification.State = skillInstallStateModifiedCopy
			fileVerification.Reason = "digest_mismatch"
			allVerified = false
			anyDrift = true
		}
		verification.Files = append(verification.Files, fileVerification)
	}

	switch {
	case allVerified:
		verification.State = skillInstallStateVerifiedMatch
	case allMissing:
		verification.State = skillInstallStateNotInstalled
		verification.Reason = "no_bundle_files_observed"
	case anyDrift:
		verification.State = skillInstallStateModifiedCopy
		verification.Reason = "local_copy_differs_from_bundle"
	case anyUnknown:
		verification.State = skillInstallStateUnknownLocal
		verification.Reason = "local_files_not_fully_inspectable"
	default:
		verification.State = skillInstallStateUnknownLocal
		verification.Reason = "unknown_local_state"
	}
	return verification
}

func skillBundleFiles(bundle map[string]any) []map[string]any {
	if bundle == nil {
		return nil
	}
	rawFiles, _ := bundle["files"].([]any)
	out := make([]map[string]any, 0, len(rawFiles))
	for _, rawFile := range rawFiles {
		file, ok := rawFile.(map[string]any)
		if !ok || file == nil {
			continue
		}
		out = append(out, file)
	}
	return out
}

func skillSHA256Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedSkillPath(value string) string {
	return strings.Trim(strings.TrimSpace(value), "/")
}

func stringValue(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func boolValue(value any) bool {
	b, _ := value.(bool)
	return b
}

func skillToolResultFromError(err error, endpoint string) (*mcpruntime.ToolResult, error) {
	if err == nil {
		return toolErrorResult("upstream_error", "error", 500, nil)
	}
	if res, authErr := lesserToolResultFromError(err); authErr == nil && res != nil {
		return res, nil
	}

	details := map[string]any{
		"source":   "lesser_skills",
		"endpoint": endpoint,
	}
	var apiErr *lesserapi.APIError
	if errors.As(err, &apiErr) {
		message, parsed := commExtractAPIErrorMessage(apiErr.Body)
		if parsed != nil {
			details["apiError"] = parsed
		}
		return toolErrorResult(skillToolErrorCodeForStatus(apiErr.Status), message, apiErr.Status, details)
	}

	lowerErr := strings.ToLower(err.Error())
	if strings.Contains(lowerErr, "lesser_api_base_url") || strings.Contains(lowerErr, "mcp_endpoint") {
		return toolErrorResult("not_configured", err.Error(), http.StatusInternalServerError, details)
	}
	return toolErrorResult("upstream_error", err.Error(), 0, details)
}

func skillToolErrorCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if status >= 500 {
			return "upstream_error"
		}
		return "upstream_error"
	}
}
