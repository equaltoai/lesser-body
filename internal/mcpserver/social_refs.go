package mcpserver

import "strings"

const socialStatusContentPreviewRunes = 500

type AccountRef struct {
	ID            string   `json:"id,omitempty"`
	Acct          string   `json:"acct,omitempty"`
	DisplayName   string   `json:"displayName,omitempty"`
	URL           string   `json:"url,omitempty"`
	MissingFields []string `json:"missingFields,omitempty"`
}

type StatusRef struct {
	ID               string              `json:"id,omitempty"`
	URL              string              `json:"url,omitempty"`
	AuthorRef        *AccountRef         `json:"authorRef,omitempty"`
	CreatedAt        string              `json:"createdAt,omitempty"`
	Visibility       string              `json:"visibility,omitempty"`
	ContentPreview   string              `json:"contentPreview,omitempty"`
	ContentTruncated bool                `json:"contentTruncated"`
	Expand           *SocialExpansionRef `json:"expand,omitempty"`
	Omitted          []SocialOmittedRef  `json:"omitted,omitempty"`
	MissingFields    []string            `json:"missingFields,omitempty"`
}

type SocialExpansionRef struct {
	Tool       string         `json:"tool"`
	Arguments  map[string]any `json:"arguments"`
	ResultPath string         `json:"resultPath,omitempty"`
}

type SocialOmittedRef struct {
	Path   string             `json:"path"`
	Reason string             `json:"reason"`
	Expand SocialExpansionRef `json:"expand"`
}

func compactSocialAccountRef(raw map[string]any) *AccountRef {
	if raw == nil {
		return nil
	}
	ref := &AccountRef{
		ID:          firstNonEmptyStringMap(raw, "id"),
		Acct:        firstNonEmptyStringMap(raw, "acct"),
		DisplayName: firstNonEmptyStringMap(raw, "display_name", "displayName"),
		URL:         firstNonEmptyStringMap(raw, "url"),
	}
	ref.MissingFields = missingSocialRefFields(map[string]string{
		"id":          ref.ID,
		"acct":        ref.Acct,
		"displayName": ref.DisplayName,
		"url":         ref.URL,
	})
	if ref.ID == "" && ref.Acct == "" && ref.DisplayName == "" && ref.URL == "" && len(ref.MissingFields) == 0 {
		return nil
	}
	return ref
}

func compactSocialStatusRef(raw map[string]any) *StatusRef {
	return compactSocialStatusRefWithPreview(raw, socialStatusContentPreviewRunes)
}

func compactSocialStatusRefWithPreview(raw map[string]any, previewRunes int) *StatusRef {
	if raw == nil {
		return nil
	}
	if previewRunes <= 0 {
		previewRunes = socialStatusContentPreviewRunes
	}

	id := firstNonEmptyStringMap(raw, "id")
	content := rawSocialStatusContent(raw)
	preview, truncated := compactStringWithTruncation(content, previewRunes)

	ref := &StatusRef{
		ID:               id,
		URL:              firstNonEmptyStringMap(raw, "url", "uri"),
		AuthorRef:        compactSocialAccountRefValue(firstMap(raw, "account", "author")),
		CreatedAt:        firstNonEmptyStringMap(raw, "created_at", "createdAt"),
		Visibility:       firstNonEmptyStringMap(raw, "visibility"),
		ContentPreview:   preview,
		ContentTruncated: truncated,
	}
	if ref.AuthorRef == nil {
		ref.AuthorRef = compactSocialAccountRefValue(raw["authorRef"])
	}
	ref.MissingFields = missingSocialRefFields(map[string]string{
		"id":         ref.ID,
		"url":        ref.URL,
		"authorRef":  accountRefStableValue(ref.AuthorRef),
		"createdAt":  ref.CreatedAt,
		"visibility": ref.Visibility,
	})
	if id != "" {
		ref.Expand = socialPostGetExpansion(id, readViewStandard, "structuredContent.data.status")
		if content != "" {
			ref.Omitted = []SocialOmittedRef{{
				Path:   "content",
				Reason: "content_preview",
				Expand: *socialPostGetExpansion(id, readViewStandard, "structuredContent.data.status.content"),
			}}
		}
	}
	return ref
}

func compactSocialAccountRefValue(raw any) *AccountRef {
	switch typed := raw.(type) {
	case nil:
		return nil
	case *AccountRef:
		return typed
	case AccountRef:
		return &typed
	case map[string]any:
		return compactSocialAccountRef(typed)
	default:
		return nil
	}
}

func socialStatusStandardPayload(raw map[string]any) map[string]any {
	status := map[string]any{}
	putIfNotEmpty(status, "id", firstNonEmptyStringMap(raw, "id"))
	putIfNotEmpty(status, "url", firstNonEmptyStringMap(raw, "url", "uri"))
	putIfNotEmpty(status, "content", rawSocialStatusContent(raw))
	putIfNotEmpty(status, "createdAt", firstNonEmptyStringMap(raw, "created_at", "createdAt"))
	putIfNotEmpty(status, "inReplyToId", firstNonEmptyStringMap(raw, "in_reply_to_id", "inReplyToId"))
	putIfNotEmpty(status, "visibility", firstNonEmptyStringMap(raw, "visibility"))
	if author := compactSocialAccountRef(firstMap(raw, "account", "author")); author != nil {
		status["authorRef"] = author
	}
	if len(status) == 0 {
		return map[string]any{}
	}
	return status
}

func socialPostGetExpansion(id string, view string, resultPath string) *SocialExpansionRef {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	view = strings.ToLower(strings.TrimSpace(view))
	if view == "" {
		view = readViewStandard
	}
	return &SocialExpansionRef{
		Tool: "post_get",
		Arguments: map[string]any{
			"id":   id,
			"view": view,
		},
		ResultPath: strings.TrimSpace(resultPath),
	}
}

func compactStringWithTruncation(value string, maxRunes int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return value, false
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value, false
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes]), true
	}
	return string(runes[:maxRunes-1]) + "…", true
}

func rawSocialStatusContent(raw map[string]any) string {
	return firstNonEmptyStringMap(raw, "content", "text")
}

func accountRefStableValue(ref *AccountRef) string {
	if ref == nil {
		return ""
	}
	return firstNonEmpty(ref.ID, ref.Acct, ref.URL)
}

func missingSocialRefFields(values map[string]string) []string {
	order := []string{"id", "acct", "displayName", "url", "authorRef", "createdAt", "visibility"}
	missing := make([]string, 0, len(values))
	for _, name := range order {
		value, ok := values[name]
		if !ok {
			continue
		}
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
