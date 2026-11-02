package servers

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"

	connect "connectrpc.com/connect/v2"
	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	tenantIdentityProviderAttribute = "ai-grid/identity-provider"
	defaultOIDCScopes               = "openid profile email"
	// keycloakGroupAttributeMax is Keycloak's GROUP_ATTRIBUTE.VALUE column
	// (varchar(255)). Organization attributes longer than this must be split
	// across multiple values of the same attribute name.
	keycloakGroupAttributeMax = 255
)

var (
	idpSecretNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	idpHTTPURLRE    = regexp.MustCompile(`^https?://`)
)

// normalizeIdentityProvider validates and normalizes an IdP for create/compare.
// Empty title becomes tenantName; enabled and validate_signature default to true;
// empty default_scopes becomes "openid profile email".
func normalizeIdentityProvider(tenantName string, idp *aigridv1.IdentityProvider) (*aigridv1.IdentityProvider, error) {
	if idp == nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "spec.identity_provider is required")
	}
	out := proto.Clone(idp).(*aigridv1.IdentityProvider)

	title, err := trimIDPString("spec.identity_provider.title", out.GetTitle(), 253)
	if err != nil {
		return nil, err
	}
	if title == "" {
		title = tenantName
	}
	out.Title = title

	if err := maxIDPLen("spec.identity_provider.description", out.GetDescription(), 4000); err != nil {
		return nil, err
	}

	if out.Enabled == nil {
		enabled := true
		out.Enabled = &enabled
	} else if !out.GetEnabled() {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "spec.identity_provider.enabled must be true")
	}

	cfg := out.GetConfig()
	if cfg == nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "spec.identity_provider.config is required")
	}

	clientID, err := trimIDPString("spec.identity_provider.config.client_id", cfg.GetClientId(), 253)
	if err != nil {
		return nil, err
	}
	if clientID == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "spec.identity_provider.config.client_id is required")
	}
	cfg.ClientId = clientID

	secretName, err := trimIDPString("spec.identity_provider.config.client_secret_secret_name", cfg.GetClientSecretSecretName(), 253)
	if err != nil {
		return nil, err
	}
	if secretName == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "spec.identity_provider.config.client_secret_secret_name is required")
	}
	if !idpSecretNameRE.MatchString(secretName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "spec.identity_provider.config.client_secret_secret_name %q is not a valid DNS subdomain name", secretName)
	}
	cfg.ClientSecretSecretName = secretName

	issuer, err := normalizeIDPURL("spec.identity_provider.config.issuer", cfg.GetIssuer(), false)
	if err != nil {
		return nil, err
	}
	cfg.Issuer = issuer

	authURL, err := normalizeIDPURL("spec.identity_provider.config.authorization_url", cfg.GetAuthorizationUrl(), false)
	if err != nil {
		return nil, err
	}
	cfg.AuthorizationUrl = authURL

	tokenURL, err := normalizeIDPURL("spec.identity_provider.config.token_url", cfg.GetTokenUrl(), false)
	if err != nil {
		return nil, err
	}
	cfg.TokenUrl = tokenURL

	scopes, err := trimIDPString("spec.identity_provider.config.default_scopes", cfg.GetDefaultScopes(), 2048)
	if err != nil {
		return nil, err
	}
	if scopes == "" {
		scopes = defaultOIDCScopes
	}
	cfg.DefaultScopes = scopes

	userInfo, err := normalizeIDPURL("spec.identity_provider.config.user_info_url", cfg.GetUserInfoUrl(), true)
	if err != nil {
		return nil, err
	}
	cfg.UserInfoUrl = userInfo

	jwks, err := normalizeIDPURL("spec.identity_provider.config.jwks_url", cfg.GetJwksUrl(), true)
	if err != nil {
		return nil, err
	}
	cfg.JwksUrl = jwks

	logout, err := normalizeIDPURL("spec.identity_provider.config.logout_url", cfg.GetLogoutUrl(), true)
	if err != nil {
		return nil, err
	}
	cfg.LogoutUrl = logout

	if cfg.ValidateSignature == nil {
		validate := true
		cfg.ValidateSignature = &validate
	}

	out.Config = cfg
	return out, nil
}

func identityProviderToAttribute(idp *aigridv1.IdentityProvider) (string, error) {
	if idp == nil {
		return "", fmt.Errorf("identity provider is nil")
	}
	b, err := protojson.Marshal(idp)
	if err != nil {
		return "", fmt.Errorf("marshal identity provider: %w", err)
	}
	return string(b), nil
}

// identityProviderToAttributeValues marshals the IdP and splits it into chunks
// that fit Keycloak's 255-character group attribute limit.
func identityProviderToAttributeValues(idp *aigridv1.IdentityProvider) ([]string, error) {
	raw, err := identityProviderToAttribute(idp)
	if err != nil {
		return nil, err
	}
	return chunkString(raw, keycloakGroupAttributeMax), nil
}

func identityProviderFromAttribute(value string) (*aigridv1.IdentityProvider, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	idp := &aigridv1.IdentityProvider{}
	if err := protojson.Unmarshal([]byte(value), idp); err != nil {
		return nil, fmt.Errorf("unmarshal identity provider attribute: %w", err)
	}
	return idp, nil
}

func identityProviderFromOrganizationAttrs(attrs map[string][]string) (*aigridv1.IdentityProvider, error) {
	values := attrs[tenantIdentityProviderAttribute]
	if len(values) == 0 {
		return nil, nil
	}
	return identityProviderFromAttribute(strings.Join(values, ""))
}

func chunkString(s string, size int) []string {
	if size <= 0 {
		return []string{s}
	}
	if s == "" {
		return nil
	}
	out := make([]string, 0, (len(s)+size-1)/size)
	for len(s) > size {
		out = append(out, s[:size])
		s = s[size:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func trimIDPString(field, s string, max int) (string, error) {
	s = strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF })
	if err := maxIDPLen(field, s, max); err != nil {
		return "", err
	}
	return s, nil
}

func maxIDPLen(field, s string, max int) error {
	if len(utf16.Encode([]rune(s))) > max {
		return connect.Errorf(connect.CodeInvalidArgument, "%s exceeds %d characters", field, max)
	}
	return nil
}

func normalizeIDPURL(field, s string, optional bool) (string, error) {
	if optional && s == "" {
		return "", nil
	}
	s, err := trimIDPString(field, s, 2048)
	if err != nil {
		return "", err
	}
	if optional && s == "" {
		return "", nil
	}
	if s == "" {
		return "", connect.Errorf(connect.CodeInvalidArgument, "%s is required", field)
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", connect.Errorf(connect.CodeInvalidArgument, "%s must be a valid URL", field)
	}
	if !idpHTTPURLRE.MatchString(s) {
		return "", connect.Errorf(connect.CodeInvalidArgument, "%s must use HTTP or HTTPS", field)
	}
	return s, nil
}
