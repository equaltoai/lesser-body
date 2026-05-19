package runtimepolicy

import (
	"context"
	"os"
	"strings"

	"github.com/equaltoai/lesser-body/internal/soulbinding"
)

type Profile string

const (
	ProfileDrone  Profile = "drone"
	ProfileSouled Profile = "souled"
)

const envLesserTableName = "LESSER_TABLE_NAME"

type Contract struct {
	Profile               Profile  `json:"profile"`
	Description           string   `json:"description"`
	CommunicationsEnabled bool     `json:"communicationsEnabled"`
	WalletAccessEnabled   bool     `json:"walletAccessEnabled"`
	Tools                 []string `json:"tools"`
	Resources             []string `json:"resources"`
	Prompts               []string `json:"prompts"`
}

type Resolved struct {
	Profile               Profile `json:"profile"`
	Determined            bool    `json:"determined"`
	DeterminationSource   string  `json:"determinationSource,omitempty"`
	BoundSoul             bool    `json:"boundSoul"`
	SoulAgentID           string  `json:"soulAgentId,omitempty"`
	CommunicationsEnabled bool    `json:"communicationsEnabled"`
	WalletAccessEnabled   bool    `json:"walletAccessEnabled"`
}

type contextKey int

const resolvedContextKey contextKey = iota

var profileOrder = []Profile{
	ProfileDrone,
	ProfileSouled,
}

var profileContracts = map[Profile]Contract{
	ProfileDrone: {
		Profile:               ProfileDrone,
		Description:           "Lightweight body runtime before soul promotion. Social and memory surfaces stay available, while communication and wallet-backed surfaces stay disabled.",
		CommunicationsEnabled: false,
		WalletAccessEnabled:   false,
		Tools: []string{
			"echo",
			"profile_read",
			"timeline_read",
			"post_search",
			"post_get",
			"followers_list",
			"following_list",
			"conversations_read",
			"conversation_get",
			"direct_messages_read",
			"notifications_read",
			"notification_get",
			"notification_dismiss",
			"post_create",
			"post_boost",
			"post_favorite",
			"follow",
			"unfollow",
			"profile_update",
			"memory_append",
			"memory_query",
			"skills_catalog",
			"skill_bundle_get",
			"soul_read",
		},
		Resources: []string{
			"agent://profile",
			"agent://timeline/home",
			"agent://timeline/local",
			"agent://followers",
			"agent://following",
			"agent://notifications",
			"agent://memory/recent",
			"agent://capabilities",
			"agent://config",
		},
		Prompts: []string{
			"compose_post",
			"summarize_timeline",
			"draft_reply",
			"reputation_report",
			"memory_reflect",
		},
	},
	ProfileSouled: {
		Profile:               ProfileSouled,
		Description:           "Souled runtime with the full lesser-body MCP surface, including communication flows and wallet-backed product semantics.",
		CommunicationsEnabled: true,
		WalletAccessEnabled:   true,
		Tools: []string{
			"echo",
			"profile_read",
			"timeline_read",
			"post_search",
			"post_get",
			"followers_list",
			"following_list",
			"conversations_read",
			"conversation_get",
			"direct_messages_read",
			"notifications_read",
			"notification_get",
			"notification_dismiss",
			"post_create",
			"post_boost",
			"post_favorite",
			"follow",
			"unfollow",
			"profile_update",
			"memory_append",
			"memory_query",
			"skills_catalog",
			"skill_bundle_get",
			"email_send",
			"email_read",
			"email_get",
			"email_get_content",
			"email_search",
			"email_reply",
			"email_delete",
			"email_mark_read",
			"email_mark_unread",
			"sms_send",
			"sms_read",
			"voicemail_read",
			"identity_whoami",
			"soul_read",
			"identity_lookup",
			"identity_verify",
		},
		Resources: []string{
			"agent://profile",
			"agent://timeline/home",
			"agent://timeline/local",
			"agent://followers",
			"agent://following",
			"agent://notifications",
			"agent://channels",
			"agent://channels/preferences",
			"agent://email/inbox",
			"agent://email/sent",
			"agent://sms/messages",
			"agent://voicemail",
			"agent://memory/recent",
			"agent://capabilities",
			"agent://config",
		},
		Prompts: []string{
			"compose_post",
			"summarize_timeline",
			"draft_reply",
			"reputation_report",
			"memory_reflect",
			"compose_email",
			"handle_inbound",
			"respect_preferences",
		},
	},
}

var profileToolSets = buildNameSetMap(func(c Contract) []string { return c.Tools })
var profileResourceSets = buildNameSetMap(func(c Contract) []string { return c.Resources })
var profilePromptSets = buildNameSetMap(func(c Contract) []string { return c.Prompts })

func ResolveForActor(ctx context.Context, actor string) Resolved {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return resolvedForProfile(ProfileDrone, false, "actor_unset", "", false)
	}
	if strings.TrimSpace(os.Getenv(envLesserTableName)) == "" {
		return resolvedForProfile(ProfileDrone, false, "soulbinding_unconfigured", "", false)
	}

	agentID, err := soulbinding.ResolveAgentID(ctx, actor)
	switch {
	case err != nil:
		return resolvedForProfile(ProfileDrone, false, "soulbinding_lookup_error", "", false)
	case strings.TrimSpace(agentID) == "":
		return resolvedForProfile(ProfileDrone, true, "soulbinding_absent", "", false)
	default:
		return resolvedForProfile(ProfileSouled, true, "soulbinding_present", agentID, true)
	}
}

func WithContext(ctx context.Context, resolved Resolved) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, resolvedContextKey, resolved)
}

func FromContext(ctx context.Context) (Resolved, bool) {
	if ctx == nil {
		return Resolved{}, false
	}
	resolved, ok := ctx.Value(resolvedContextKey).(Resolved)
	return resolved, ok
}

func ContractFor(profile Profile) Contract {
	contract, ok := profileContracts[normalizeProfile(profile)]
	if !ok {
		contract = profileContracts[ProfileSouled]
	}
	return Contract{
		Profile:               contract.Profile,
		Description:           contract.Description,
		CommunicationsEnabled: contract.CommunicationsEnabled,
		WalletAccessEnabled:   contract.WalletAccessEnabled,
		Tools:                 append([]string(nil), contract.Tools...),
		Resources:             append([]string(nil), contract.Resources...),
		Prompts:               append([]string(nil), contract.Prompts...),
	}
}

func Contracts() map[string]Contract {
	out := make(map[string]Contract, len(profileOrder))
	for _, profile := range profileOrder {
		contract := ContractFor(profile)
		out[string(profile)] = contract
	}
	return out
}

func ToolsForProfile(profile Profile) []string {
	return append([]string(nil), ContractFor(profile).Tools...)
}

func ResourcesForProfile(profile Profile) []string {
	return append([]string(nil), ContractFor(profile).Resources...)
}

func PromptsForProfile(profile Profile) []string {
	return append([]string(nil), ContractFor(profile).Prompts...)
}

func ToolAllowed(profile Profile, name string) bool {
	return profileToolSets[normalizeProfile(profile)][strings.TrimSpace(name)]
}

func ResourceAllowed(profile Profile, uri string) bool {
	return profileResourceSets[normalizeProfile(profile)][strings.TrimSpace(uri)]
}

func PromptAllowed(profile Profile, name string) bool {
	return profilePromptSets[normalizeProfile(profile)][strings.TrimSpace(name)]
}

func resolvedForProfile(profile Profile, determined bool, source string, agentID string, boundSoul bool) Resolved {
	contract := ContractFor(profile)
	return Resolved{
		Profile:               contract.Profile,
		Determined:            determined,
		DeterminationSource:   strings.TrimSpace(source),
		BoundSoul:             boundSoul,
		SoulAgentID:           strings.TrimSpace(agentID),
		CommunicationsEnabled: contract.CommunicationsEnabled,
		WalletAccessEnabled:   contract.WalletAccessEnabled,
	}
}

func normalizeProfile(profile Profile) Profile {
	switch strings.ToLower(strings.TrimSpace(string(profile))) {
	case string(ProfileDrone):
		return ProfileDrone
	default:
		return ProfileSouled
	}
}

func buildNameSetMap(selectNames func(Contract) []string) map[Profile]map[string]bool {
	out := make(map[Profile]map[string]bool, len(profileContracts))
	for profile, contract := range profileContracts {
		names := selectNames(contract)
		set := make(map[string]bool, len(names))
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			set[name] = true
		}
		out[profile] = set
	}
	return out
}
